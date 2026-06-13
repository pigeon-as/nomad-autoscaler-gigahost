// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"

	"github.com/pigeon-as/nomad-autoscaler-gigahost/internal/gigahost"
)

const (
	nodeAttrHostname = "unique.hostname"

	envGigahostAPIToken = "GIGAHOST_API_TOKEN"

	hostnameSuffixLen = 6

	maxDeployPollErrors = 4
)

// Vars so tests can poll fast and bound the deploy wait.
var (
	serverDeployTimeout      = 30 * time.Minute
	serverDeployPollInterval = 5 * time.Second
	defaultRetryInterval     = 10 * time.Second
)

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

// serverNameIndex returns the live server names and a name -> srv_id index.
// Ambiguous (duplicate) names are excluded so a wrong server can never be
// cancelled.
func (t *TargetPlugin) serverNameIndex(servers []gigahost.Server) ([]string, map[string]string) {
	srvIDFor := make(map[string]string, len(servers))
	ambiguous := make(map[string]bool)
	for _, s := range servers {
		if s.Cancelled() || s.SrvName == "" {
			continue
		}
		if _, dup := srvIDFor[s.SrvName]; dup {
			ambiguous[s.SrvName] = true
			continue
		}
		srvIDFor[s.SrvName] = s.SrvID
	}

	names := make([]string, 0, len(srvIDFor))
	for name := range srvIDFor {
		if ambiguous[name] {
			t.logger.Warn("server name is ambiguous, excluding from scale-in", "name", name)
			delete(srvIDFor, name)
			continue
		}
		names = append(names, name)
	}
	return names, srvIDFor
}

func hostnamesFor(prefix string, count int64) ([]string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	names := make([]string, count)
	for i := range names {
		b := make([]byte, hostnameSuffixLen)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("generating hostname suffix: %v", err)
		}
		for j := range b {
			b[j] = alphabet[int(b[j])%len(alphabet)]
		}
		names[i] = prefix + "-" + string(b)
	}
	return names, nil
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

// waitForServers polls the deploy status until want servers reach a terminal
// status, mirroring terraform-provider-gigahost's waitForServer. Observed
// srv_ids are kept so a deploy that never finishes names them in the error.
// A deploy that never reaches a final status — including one the deploy status
// API stops reporting — surfaces as a timeout.
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

	idsOf := func(m map[string]bool) []string {
		ids := make([]string, 0, len(m))
		for id := range m {
			ids = append(ids, id)
		}
		return ids
	}

	for {
		status, err := t.client.GetDeployStatus(ctx, orderIDs)
		if err != nil {
			pollErrors++
			if pollErrors > maxDeployPollErrors {
				return nil, fmt.Errorf("polling deploy status for orders %v failed %d times in a row (observed server ids %v): %v", orderIDs, pollErrors, idsOf(observed), err)
			}
		} else {
			pollErrors = 0
			for _, s := range status.Servers {
				if !orderSet[s.Order()] {
					continue
				}
				id := s.Server()
				if id != 0 {
					observed[strconv.FormatInt(id, 10)] = true
				}
				switch s.Status {
				case "ready", "rescue", "iso":
					if id != 0 {
						ready[strconv.FormatInt(id, 10)] = true
					}
				case "error", "failed", "cancelled", "suspended":
					return nil, fmt.Errorf("server (order %d) failed to deploy: status %q", s.Order(), s.Status)
				default:
					t.logger.Debug("waiting for Gigahost server to deploy",
						"order_id", s.Order(), "status", s.Status)
				}
			}

			if int64(len(ready)) >= want {
				return idsOf(ready), nil
			}
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for %d servers (orders %v) to be ready; observed server ids %v — cancel manually if they materialize: %v", want, orderIDs, idsOf(observed), ctx.Err())
		case <-ticker.C:
		}
	}
}

func (t *TargetPlugin) deleteServer(ctx context.Context, serverID string) error {
	err := t.client.CancelServer(ctx, serverID)
	if err == nil || errors.Is(err, gigahost.ErrNotFound) {
		return nil
	}

	// Cancelling a server that died during provisioning returns 400, not 404,
	// so a refusal only counts as fatal if the server still exists.
	if gone, goneErr := t.serverGone(ctx, serverID); goneErr == nil && gone {
		t.logger.Warn("cancellation refused but server is already gone",
			"server_id", serverID, "error", err)
		return nil
	}
	return err
}

// serverGone reports whether the server 404s or is cancelled.
func (t *TargetPlugin) serverGone(ctx context.Context, serverID string) (bool, error) {
	s, err := t.client.GetServer(ctx, serverID)
	if errors.Is(err, gigahost.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return s.Cancelled(), nil
}

// ensureServersGone waits for every cancelled server to be gone; callers
// treat failure as non-fatal.
func (t *TargetPlugin) ensureServersGone(ctx context.Context, srvIDs []string) error {
	pending := make(map[string]bool, len(srvIDs))
	for _, id := range srvIDs {
		pending[id] = true
	}

	f := func(ctx context.Context) (bool, error) {
		for id := range pending {
			gone, err := t.serverGone(ctx, id)
			if err != nil {
				return true, err
			}
			if gone {
				delete(pending, id)
			}
		}
		if len(pending) == 0 {
			return true, nil
		}
		return false, fmt.Errorf("waiting for %d cancelled servers to terminate", len(pending))
	}

	return retry(ctx, defaultRetryInterval, t.retryAttempts, f)
}

// gigahostNodeIDMap identifies a node by its fingerprinted hostname, which
// equals the server name set at deploy.
func gigahostNodeIDMap(n *nomadapi.Node) (string, error) {
	val, ok := n.Attributes[nodeAttrHostname]
	if !ok || val == "" {
		return "", fmt.Errorf("attribute %q not found", nodeAttrHostname)
	}
	return val, nil
}
