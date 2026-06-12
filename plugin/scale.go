// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/nomad-autoscaler/sdk/helper/scaleutils"

	"github.com/pigeon-as/nomad-autoscaler-gigahost/internal/gigahost"
)

func (t *TargetPlugin) scaleIn(ctx context.Context, num int64, config map[string]string) error {
	servers, err := t.client.ListServers(ctx)
	if err != nil {
		return fmt.Errorf("failed to list Gigahost servers: %v", err)
	}
	names, srvIDFor := t.serverNameIndex(servers)

	ids, err := t.clusterUtils.RunPreScaleInTasksWithRemoteCheck(ctx, config, names, int(num))
	if err != nil {
		return fmt.Errorf("failed to perform pre-scale Nomad scale in tasks: %v", err)
	}

	var successes, failures []scaleutils.NodeResourceID
	var cancelled []string
	for _, node := range ids {
		srvID := srvIDFor[node.RemoteResourceID]
		t.logger.Info("cancelling Gigahost server",
			"node_id", node.NomadNodeID, "name", node.RemoteResourceID, "server_id", srvID)
		if err := t.deleteServer(ctx, srvID); err != nil {
			t.logger.Error("failed to cancel server", "server_id", srvID, "error", err)
			failures = append(failures, node)
			continue
		}
		successes = append(successes, node)
		cancelled = append(cancelled, srvID)
	}

	var failedTaskErr error
	if len(failures) > 0 {
		failedTaskErr = t.clusterUtils.RunPostScaleInTasksOnFailure(failures)
	}

	if len(successes) > 0 {
		// Non-fatal, but a cancelled server that keeps running is a billing
		// problem.
		if err := t.ensureServersGone(ctx, cancelled); err != nil {
			t.logger.Error("failed to confirm cancelled servers terminated", "error", err)
		}

		if err := t.clusterUtils.RunPostScaleInTasks(ctx, config, successes); err != nil {
			t.logger.Error("failed to perform post-scale Nomad scale in tasks", "error", err)
		}
	}

	if len(failures) > 0 {
		t.logger.Warn("partial scale-in",
			"success_num", len(successes), "failed_num", len(failures))
		if failedTaskErr != nil {
			return failedTaskErr
		}
		return fmt.Errorf("failed to cancel %d of %d servers",
			len(failures), len(successes)+len(failures))
	}
	return nil
}

// scaleOut deploys the missing servers as one batch order, then waits for the
// new nodes to join the Nomad pool before Scale returns.
func (t *TargetPlugin) scaleOut(ctx context.Context, desired, current int64, config map[string]string) error {
	productName, err := requireString(config, configKeyProductName)
	if err != nil {
		return err
	}
	region, err := requireString(config, configKeyRegion)
	if err != nil {
		return err
	}
	osDistro, err := requireString(config, configKeyOSDistro)
	if err != nil {
		return err
	}
	osVersion, err := requireString(config, configKeyOSVersion)
	if err != nil {
		return err
	}

	sshKeys, err := parseInt64List(getConfigValue(config, configKeySSHKeys, ""))
	if err != nil {
		return fmt.Errorf("config param %s: %v", configKeySSHKeys, err)
	}

	backups, err := strconv.ParseBool(getConfigValue(config, configKeyBackups, "false"))
	if err != nil {
		return fmt.Errorf("config param %s is not a valid boolean: %v", configKeyBackups, err)
	}

	catalog, err := t.client.GetDeployCatalog(ctx)
	if err != nil {
		return fmt.Errorf("failed to read Gigahost server catalog: %v", err)
	}
	productID, priceID, err := resolveProduct(catalog, productName)
	if err != nil {
		return err
	}
	regionID, err := resolveRegion(catalog, region)
	if err != nil {
		return err
	}
	if !productOffersRegion(catalog, productID, regionID) {
		return fmt.Errorf("product %q is not available in region %q", productName, region)
	}

	osCatalog, err := t.client.GetOSCatalog(ctx)
	if err != nil {
		return fmt.Errorf("failed to read Gigahost OS catalog: %v", err)
	}
	osID, err := resolveOS(osCatalog, osDistro, osVersion)
	if err != nil {
		return err
	}

	toCreate := desired - current

	// Server names become OS hostnames, which is how scale-in identifies nodes.
	prefix, err := requireString(config, configKeyHostnamePrefix)
	if err != nil {
		return err
	}
	hostnames, err := hostnamesFor(prefix, toCreate)
	if err != nil {
		return err
	}

	in := gigahost.DeployInput{
		ProductID: productID,
		PriceID:   priceID,
		RegionID:  regionID,
		OSID:      &osID,
		Hostnames: hostnames,
		SSHKeys:   sshKeys,
		Backups:   backups,
	}

	log := t.logger.With("action", "scale_out",
		"desired_count", desired, "current_count", current, "create_count", toCreate)

	log.Info("deploying Gigahost servers")
	srvIDs, err := t.createServers(ctx, in, toCreate)
	if err != nil {
		return fmt.Errorf("failed to create Gigahost servers: %v", err)
	}
	log.Info("servers deployed and ready", "server_ids", srvIDs)

	// A server that deploys but never joins Nomad fails here and is invisible
	// to scale-in — cancel it manually.
	if err := t.ensurePoolNodesCount(ctx, config, desired); err != nil {
		return fmt.Errorf("failed to confirm scale out: waiting for Nomad pool to reach %d nodes: %v", desired, err)
	}

	log.Info("successfully performed and verified scaling out")
	return nil
}
