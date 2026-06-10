// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"context"
	"fmt"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad-autoscaler/plugins/base"
	"github.com/hashicorp/nomad-autoscaler/plugins/target"
	"github.com/hashicorp/nomad-autoscaler/sdk"
	"github.com/hashicorp/nomad-autoscaler/sdk/helper/nomad"
	"github.com/hashicorp/nomad-autoscaler/sdk/helper/scaleutils"
	"github.com/hashicorp/nomad-autoscaler/sdk/helper/scaleutils/nodepool"
	nomadapi "github.com/hashicorp/nomad/api"

	"github.com/pigeon-as/nomad-autoscaler-gigahost/internal/gigahost"
)

const pluginName = "gigahost"

var (
	pluginInfo = &base.PluginInfo{
		Name:       pluginName,
		PluginType: sdk.PluginTypeTarget,
	}

	_ target.Target = (*TargetPlugin)(nil)
)

type TargetPlugin struct {
	config       map[string]string
	logger       hclog.Logger
	client       *gigahost.Client
	nomad        *nomadapi.Client
	clusterUtils *scaleutils.ClusterScaleUtils
}

func NewGigahostPlugin(log hclog.Logger) *TargetPlugin {
	return &TargetPlugin{
		logger: log,
	}
}

func (t *TargetPlugin) SetConfig(config map[string]string) error {
	t.config = config

	client, err := t.setupGigahostClient(config)
	if err != nil {
		return err
	}
	t.client = client

	nomadConfig := nomad.ConfigFromNamespacedMap(config)
	nomadClient, err := nomadapi.NewClient(nomadConfig)
	if err != nil {
		return fmt.Errorf("failed to instantiate Nomad client: %v", err)
	}
	t.nomad = nomadClient

	clusterUtils, err := scaleutils.NewClusterScaleUtils(nomadConfig, t.logger)
	if err != nil {
		return err
	}

	t.clusterUtils = clusterUtils
	t.clusterUtils.ClusterNodeIDLookupFunc = gigahostNodeIDMap

	return nil
}

func (t *TargetPlugin) PluginInfo() (*base.PluginInfo, error) {
	return pluginInfo, nil
}

func (t *TargetPlugin) Scale(action sdk.ScalingAction, config map[string]string) error {
	if action.Count == sdk.StrategyActionMetaValueDryRunCount {
		return nil
	}

	ctx := context.Background()

	current, err := t.countPoolNodes(config)
	if err != nil {
		return fmt.Errorf("failed to count Nomad pool nodes: %v", err)
	}

	num, direction := t.calculateDirection(current, action.Count)

	switch direction {
	case "in":
		err = t.scaleIn(ctx, num, config)
	case "out":
		err = t.scaleOut(ctx, num, current, config)
	default:
		t.logger.Info("scaling not required",
			"current_count", current, "strategy_count", action.Count)
		return nil
	}

	if err != nil {
		err = fmt.Errorf("failed to perform scaling action: %v", err)
	}
	return err
}

func (t *TargetPlugin) Status(config map[string]string) (*sdk.TargetStatus, error) {
	ready, err := t.clusterUtils.IsPoolReady(config)
	if err != nil {
		return nil, fmt.Errorf("failed to run Nomad node readiness check: %v", err)
	}
	if !ready {
		return &sdk.TargetStatus{Ready: ready}, nil
	}

	count, err := t.countPoolNodes(config)
	if err != nil {
		return nil, fmt.Errorf("failed to count Nomad pool nodes: %v", err)
	}

	resp := sdk.TargetStatus{
		Ready: true,
		Count: count,
		Meta:  make(map[string]string),
	}

	return &resp, nil
}

// Identical to aws-asg/gce-mig: delta for scale-in, absolute desired total for scale-out.
func (t *TargetPlugin) calculateDirection(current, strategyDesired int64) (int64, string) {
	if strategyDesired < current {
		return current - strategyDesired, "in"
	}
	if strategyDesired > current {
		return strategyDesired, "out"
	}
	return 0, ""
}

// Gigahost has no cloud-side group; the ready Nomad pool count stands in for it.
func (t *TargetPlugin) countPoolNodes(config map[string]string) (int64, error) {
	poolID, err := nodepool.NewClusterNodePoolIdentifier(config)
	if err != nil {
		return 0, err
	}

	nodes, _, err := t.nomad.Nodes().List(nil)
	if err != nil {
		return 0, fmt.Errorf("failed to list Nomad nodes: %v", err)
	}

	var count int64
	for _, node := range nodes {
		if node.Status == nomadapi.NodeStatusReady && !node.Drain && poolID.IsPoolMember(node) {
			count++
		}
	}
	return count, nil
}
