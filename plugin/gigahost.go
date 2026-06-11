// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/hashicorp/nomad-autoscaler/sdk/helper/scaleutils"
	nomadapi "github.com/hashicorp/nomad/api"

	"github.com/pigeon-as/nomad-autoscaler-gigahost/internal/gigahost"
)

const (
	nodeAttrGigahostServerID = "unique.platform.gigahost.server_id"

	envGigahostAPIToken = "GIGAHOST_API_TOKEN"

	serverDeployTimeout = 30 * time.Minute
	maxDeployPollErrors = 4

	// The deploy status view only lists orders whose server exists or is
	// provisioning. The server list, matched by order id, is the durable
	// completion source: consulted every 3rd consecutive status miss; a seen
	// server absent from both views for 20 list checks (~5m) is gone. Mirrors
	// terraform-provider-gigahost's waitForServer (v0.3.1).
	listEveryMisses = 3
	maxGoneChecks   = 20

	defaultRetryInterval = 10 * time.Second
)

// serverDeployPollInterval is a var so tests can shorten it.
var serverDeployPollInterval = 5 * time.Second

func (t *TargetPlugin) setupGigahostClient(config map[string]string) (*gigahost.Client, error) {
	token := getConfigValue(config, configKeyAPIToken, "")
	if token == "" {
		token = os.Getenv(envGigahostAPIToken)
	}
	if token == "" {
		return nil, fmt.Errorf("required config param %s or env var %s not found", configKeyAPIToken, envGigahostAPIToken)
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

func (t *TargetPlugin) createServers(ctx context.Context, in gigahost.DeployInput, want int64) ([]string, error) {
	in.Quantity = want

	result, err := t.client.Deploy(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("deploying servers: %v", err)
	}
	if len(result.OrderIDs) == 0 {
		return nil, fmt.Errorf("deploy API did not return an order id")
	}

	return t.waitForServers(ctx, result.OrderIDs, want)
}

// waitForServers polls the deploy orders until want servers are ready,
// keyed by srv_id (robust to one order per batch or per server). The status
// view has no failure state and can omit in-flight orders, so the server
// list completes the wait when status misses; observed srv_ids are carried
// in every error so a late-materializing server is never anonymous.
func (t *TargetPlugin) waitForServers(ctx context.Context, orderIDs []int64, want int64) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, serverDeployTimeout)
	defer cancel()

	ticker := time.NewTicker(serverDeployPollInterval)
	defer ticker.Stop()

	orderSet := make(map[int64]bool, len(orderIDs))
	for _, id := range orderIDs {
		orderSet[id] = true
	}

	ready := make(map[string]bool)
	observed := make(map[string]bool)
	pollErrors := 0
	statusMisses := 0
	goneChecks := 0

	readyIDs := func() []string {
		ids := make([]string, 0, len(ready))
		for id := range ready {
			ids = append(ids, id)
		}
		return ids
	}
	observedIDs := func() []string {
		ids := make([]string, 0, len(observed))
		for id := range observed {
			ids = append(ids, id)
		}
		return ids
	}

	for {
		status, err := t.client.GetDeployStatus(ctx, orderIDs)
		if err != nil {
			pollErrors++
			if pollErrors > maxDeployPollErrors {
				return nil, fmt.Errorf("polling deploy status for orders %v failed %d times in a row (observed server ids %v): %v", orderIDs, pollErrors, observedIDs(), err)
			}
		} else {
			pollErrors = 0

			seen := 0
			for _, s := range status.Servers {
				if !orderSet[s.Order()] {
					continue
				}
				seen++
				if id := s.Server(); id != 0 {
					observed[strconv.FormatInt(id, 10)] = true
				}
				switch s.Status {
				case "ready":
					ready[strconv.FormatInt(s.Server(), 10)] = true
				case "error", "failed", "cancelled", "suspended":
					return nil, fmt.Errorf("server (order %d) failed to deploy: status %q", s.Order(), s.Status)
				default:
					t.logger.Debug("waiting for Gigahost server to deploy",
						"order_id", s.Order(), "status", s.Status)
				}
			}

			if seen > 0 {
				statusMisses = 0
				goneChecks = 0
			} else {
				statusMisses++
				if statusMisses%listEveryMisses == 0 {
					matched := t.collectFromServerList(ctx, orderSet, ready, observed)
					switch {
					case matched:
						goneChecks = 0
					case len(observed) > 0:
						goneChecks++
						if goneChecks >= maxGoneChecks {
							return nil, fmt.Errorf("servers for orders %v disappeared while provisioning: no longer reported by the deploy status or the server list (observed server ids %v)", orderIDs, observedIDs())
						}
					default:
						t.logger.Debug("orders not reported by deploy status or server list yet",
							"order_ids", orderIDs)
					}
				}
			}

			if int64(len(ready)) >= want {
				return readyIDs(), nil
			}
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for %d servers (orders %v) to be ready; observed server ids %v — cancel manually if they materialize: %v", want, orderIDs, observedIDs(), ctx.Err())
		case <-ticker.C:
		}
	}
}

func (t *TargetPlugin) collectFromServerList(ctx context.Context, orderSet map[int64]bool, ready, observed map[string]bool) bool {
	servers, err := t.client.ListServers(ctx)
	if err != nil {
		return false
	}

	matched := false
	for _, s := range servers {
		orderID, err := strconv.ParseInt(s.Order.OrderID, 10, 64)
		if err != nil || !orderSet[orderID] || s.Cancelled() {
			continue
		}
		matched = true
		observed[s.SrvID] = true
		if !s.Installing() && s.Running() {
			ready[s.SrvID] = true
		} else {
			t.logger.Debug("order missing from deploy status; server still provisioning per server list",
				"order_id", orderID, "server_id", s.SrvID)
		}
	}
	return matched
}

func (t *TargetPlugin) deleteServer(ctx context.Context, serverID string) error {
	if err := t.client.CancelServer(ctx, serverID); err != nil && !errors.Is(err, gigahost.ErrNotFound) {
		return err
	}
	return nil
}

// ensureServersGone is the scale-in settle-wait every builtin performs:
// cancelled servers must leave the list or report a cancelled order (they
// linger). Callers treat failure as non-fatal, per aws-asg.
func (t *TargetPlugin) ensureServersGone(ctx context.Context, ids []scaleutils.NodeResourceID) error {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id.RemoteResourceID] = true
	}

	f := func(ctx context.Context) (bool, error) {
		servers, err := t.client.ListServers(ctx)
		if err != nil {
			return true, err
		}
		remaining := 0
		for _, s := range servers {
			if want[s.SrvID] && !s.Cancelled() {
				remaining++
			}
		}
		if remaining == 0 {
			return true, nil
		}
		return false, fmt.Errorf("waiting for %d cancelled servers to terminate", remaining)
	}

	return retry(ctx, defaultRetryInterval, t.retryAttempts, f)
}

// Nomad has no Gigahost fingerprinter and operators cannot define custom node
// attributes, so workers set the id as node meta; attribute checked first with
// a meta fallback, mirroring azure-vmss.
func gigahostNodeIDMap(n *nomadapi.Node) (string, error) {
	if val, ok := n.Attributes[nodeAttrGigahostServerID]; ok && val != "" {
		return val, nil
	}

	if val, ok := n.Meta[nodeAttrGigahostServerID]; ok && val != "" {
		return val, nil
	}

	return "", fmt.Errorf("attribute %q not found", nodeAttrGigahostServerID)
}
