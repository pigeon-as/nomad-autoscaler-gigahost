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
	remoteIDs, err := t.listServerIDs(ctx)
	if err != nil {
		return fmt.Errorf("failed to list Gigahost servers: %v", err)
	}

	ids, err := t.clusterUtils.RunPreScaleInTasksWithRemoteCheck(ctx, config, remoteIDs, int(num))
	if err != nil {
		return fmt.Errorf("failed to perform pre-scale Nomad scale in tasks: %v", err)
	}

	var successes, failures []scaleutils.NodeResourceID
	for _, node := range ids {
		t.logger.Info("cancelling Gigahost server",
			"node_id", node.NomadNodeID, "server_id", node.RemoteResourceID)
		if err := t.deleteServer(ctx, node.RemoteResourceID); err != nil {
			t.logger.Error("failed to cancel server",
				"server_id", node.RemoteResourceID, "error", err)
			failures = append(failures, node)
			continue
		}
		successes = append(successes, node)
	}

	var failedTaskErr error
	if len(failures) > 0 {
		failedTaskErr = t.clusterUtils.RunPostScaleInTasksOnFailure(failures)
	}

	if len(successes) > 0 {
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

// Gigahost isn't a scale set — no resize API — so unlike aws-asg/gce-mig we
// create (desired-current) servers individually; the policy cooldown must
// prevent re-evaluation while they provision.
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

	// Resolve names to catalog ids once, before the create loop.
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

	osCatalog, err := t.client.GetOSCatalog(ctx)
	if err != nil {
		return fmt.Errorf("failed to read Gigahost OS catalog: %v", err)
	}
	osID, err := resolveOS(osCatalog, osDistro, osVersion)
	if err != nil {
		return err
	}

	in := gigahost.DeployInput{
		ProductID: productID,
		PriceID:   priceID,
		RegionID:  regionID,
		OSID:      &osID,
		Hostname:  getConfigValue(config, configKeyHostname, ""),
		SSHKeys:   sshKeys,
		Backups:   backups,
	}

	toCreate := desired - current
	log := t.logger.With("action", "scale_out",
		"desired_count", desired, "current_count", current, "create_count", toCreate)

	for i := int64(0); i < toCreate; i++ {
		log.Info("deploying Gigahost server", "count", fmt.Sprintf("%d/%d", i+1, toCreate))
		srvID, err := t.createServer(ctx, in)
		if err != nil {
			return fmt.Errorf("failed to create Gigahost server: %v", err)
		}
		log.Info("server deployed and ready", "server_id", srvID)
	}

	log.Info("successfully performed scaling out")
	return nil
}
