// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package gigahost

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
)

type Server struct {
	SrvID            string      `json:"srv_id"`
	SrvName          string      `json:"srv_name"`
	SrvStatus        flexBool    `json:"srv_status"`
	SrvStatusInstall flexBool    `json:"srv_status_install"`
	SrvDateCreated   flexInt64   `json:"srv_date_created"`
	SrvDeletedDate   flexInt64   `json:"srv_deleted_date"`
	Order            ServerOrder `json:"order"`
}

// Created and Deleted are Unix-epoch seconds (0 when not applicable).
func (s Server) Created() int64 { return int64(s.SrvDateCreated) }

func (s Server) Deleted() int64 { return int64(s.SrvDeletedDate) }

type ServerOrder struct {
	OrderID     string `json:"order_id"`
	OrderStatus string `json:"order_status"`
}

func (s Server) Installing() bool { return bool(s.SrvStatusInstall) }

func (s Server) Running() bool { return bool(s.SrvStatus) }

// Cancelled servers linger in the server list; treat them as deleted.
func (s Server) Cancelled() bool { return strings.EqualFold(s.Order.OrderStatus, "cancelled") }

// GetServer reads one server by id; a 404 definitively means it is gone,
// unlike the list, which can transiently omit live servers. The API wraps
// the single server in an array.
func (c *Client) GetServer(ctx context.Context, id string) (*Server, error) {
	req, err := c.newRequest(ctx, http.MethodGet, path.Join("servers", id), nil, nil)
	if err != nil {
		return nil, err
	}

	var servers []Server
	if err := c.sendRequest(req, &servers); err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("gigahost: server %s: empty response", id)
	}
	return &servers[0], nil
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

type updateServerNameRequest struct {
	Name string `json:"name"`
}

func (c *Client) UpdateServerName(ctx context.Context, id, name string) error {
	req, err := c.newRequest(ctx, http.MethodPut, path.Join("servers", id, "name"), nil, updateServerNameRequest{Name: name})
	if err != nil {
		return err
	}
	return c.sendRequest(req, nil)
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
