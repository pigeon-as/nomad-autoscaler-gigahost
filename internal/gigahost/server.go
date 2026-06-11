// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package gigahost

import (
	"context"
	"net/http"
	"path"
	"strings"
)

type Server struct {
	SrvID            string      `json:"srv_id"`
	SrvStatus        flexBool    `json:"srv_status"`
	SrvStatusInstall flexBool    `json:"srv_status_install"`
	Order            ServerOrder `json:"order"`
}

type ServerOrder struct {
	OrderID     string `json:"order_id"`
	OrderStatus string `json:"order_status"`
}

func (s Server) Installing() bool { return bool(s.SrvStatusInstall) }

func (s Server) Running() bool { return bool(s.SrvStatus) }

// Cancelled servers linger in the server list; like the TF provider's Read,
// treat them as deleted.
func (s Server) Cancelled() bool { return strings.EqualFold(s.Order.OrderStatus, "cancelled") }

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
