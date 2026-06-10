// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package main

import (
	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad-autoscaler/plugins"

	"github.com/pigeon-as/nomad-autoscaler-gigahost/plugin"
)

func main() {
	plugins.Serve(factory)
}

func factory(log hclog.Logger) interface{} {
	return plugin.NewGigahostPlugin(log)
}
