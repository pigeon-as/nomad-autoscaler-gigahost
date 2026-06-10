// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"

	"github.com/pigeon-as/nomad-autoscaler-gigahost/internal/gigahost"
)

const (
	nodeAttrGigahostServerID = "unique.platform.gigahost.server_id"

	serverDeployTimeout      = 30 * time.Minute
	serverDeployPollInterval = 5 * time.Second
	maxDeployPollErrors      = 4
)

func (t *TargetPlugin) setupGigahostClient(config map[string]string) (*gigahost.Client, error) {
	token := getConfigValue(config, configKeyAPIToken, "")
	if token == "" {
		return nil, fmt.Errorf("required config param %s not found", configKeyAPIToken)
	}

	client, err := gigahost.NewClient(&gigahost.Config{
		Address:   getConfigValue(config, configKeyBaseURL, ""),
		Token:     token,
		UserAgent: "nomad-autoscaler-gigahost",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to setup Gigahost client: %v", err)
	}
	return client, nil
}

func (t *TargetPlugin) listServerIDs(ctx context.Context) ([]string, error) {
	servers, err := t.client.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(servers))
	for _, s := range servers {
		ids = append(ids, s.SrvID)
	}
	return ids, nil
}

func (t *TargetPlugin) createServer(ctx context.Context, in gigahost.DeployInput) (string, error) {
	result, err := t.client.Deploy(ctx, in)
	if err != nil {
		return "", fmt.Errorf("deploying server: %v", err)
	}
	if len(result.OrderIDs) == 0 {
		return "", fmt.Errorf("deploy API did not return an order id")
	}

	return t.waitForServer(ctx, result.OrderIDs[0])
}

func (t *TargetPlugin) waitForServer(ctx context.Context, orderID int64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, serverDeployTimeout)
	defer cancel()

	ticker := time.NewTicker(serverDeployPollInterval)
	defer ticker.Stop()

	pollErrors := 0

	for {
		status, err := t.client.GetDeployStatus(ctx, []int64{orderID})
		if err != nil {
			pollErrors++
			if pollErrors > maxDeployPollErrors {
				return "", fmt.Errorf("polling deploy status for order %d failed %d times in a row: %v", orderID, pollErrors, err)
			}
		} else {
			pollErrors = 0
			for _, s := range status.Servers {
				if s.Order() != orderID {
					continue
				}
				switch s.Status {
				case "ready":
					return strconv.FormatInt(s.Server(), 10), nil
				case "error", "failed", "cancelled", "suspended":
					return "", fmt.Errorf("server (order %d) failed to deploy: status %q", orderID, s.Status)
				default:
					t.logger.Debug("waiting for Gigahost server to deploy",
						"order_id", orderID, "status", s.Status)
				}
			}
		}

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timed out waiting for server (order %d) to be ready: %v", orderID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (t *TargetPlugin) deleteServer(ctx context.Context, serverID string) error {
	if err := t.client.CancelServer(ctx, serverID); err != nil && !errors.Is(err, gigahost.ErrNotFound) {
		return err
	}
	return nil
}

func gigahostNodeIDMap(n *nomadapi.Node) (string, error) {
	val, ok := n.Attributes[nodeAttrGigahostServerID]
	if !ok || val == "" {
		return "", fmt.Errorf("attribute %q not found", nodeAttrGigahostServerID)
	}
	return val, nil
}
