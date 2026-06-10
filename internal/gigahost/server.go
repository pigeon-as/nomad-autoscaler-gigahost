// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package gigahost

import (
	"context"
	"net/http"
	"path"
)

type Server struct {
	SrvID   string `json:"srv_id"`
	SrvName string `json:"srv_name"`
}

func (c *Client) ListServers(ctx context.Context) ([]Server, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "servers", nil, nil)
	if err != nil {
		return nil, err
	}

	var servers []Server
	if err := c.sendRequest(req, &servers); err != nil {
		return nil, err
	}
	return servers, nil
}

type cancelServerRequest struct {
	Reason           string `json:"reason"`
	EarlyTermination int64  `json:"early_termination"`
}

func (c *Client) CancelServer(ctx context.Context, id string) error {
	body := cancelServerRequest{Reason: "Scaled in by nomad-autoscaler", EarlyTermination: 1}
	req, err := c.newRequest(ctx, http.MethodPost, path.Join("servers", id, "cancel"), nil, body)
	if err != nil {
		return err
	}
	return c.sendRequest(req, nil)
}
