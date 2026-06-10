// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package gigahost

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type DeployInput struct {
	ProductID int64
	PriceID   int64
	RegionID  int64
	OSID      *int64
	Hostname  string
	SSHKeys   []int64
	Backups   bool
}

type deployRequest struct {
	Pid       int64    `json:"pid"`
	PriceID   int64    `json:"price_id"`
	RegionID  int64    `json:"region_id"`
	Quantity  int64    `json:"quantity"`
	OSID      *int64   `json:"os_id,omitempty"`
	Hostnames []string `json:"hostnames,omitempty"`
	SSHKeys   []int64  `json:"ssh_keys,omitempty"`
	Backups   *int64   `json:"backups,omitempty"`
}

type DeployResult struct {
	OrderIDs []int64 `json:"order_ids"`
}

func (c *Client) Deploy(ctx context.Context, in DeployInput) (*DeployResult, error) {
	body := deployRequest{
		Pid:      in.ProductID,
		PriceID:  in.PriceID,
		RegionID: in.RegionID,
		Quantity: 1,
		OSID:     in.OSID,
		SSHKeys:  in.SSHKeys,
	}
	if in.Backups {
		v := int64(1)
		body.Backups = &v
	}
	if in.Hostname != "" {
		body.Hostnames = []string{in.Hostname}
	}

	req, err := c.newRequest(ctx, http.MethodPost, "deploy/servers", nil, body)
	if err != nil {
		return nil, err
	}

	var result DeployResult
	if err := c.sendRequest(req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type DeployStatusServer struct {
	OrderID flexInt64 `json:"order_id"`
	SrvID   flexInt64 `json:"srv_id"`
	Status  string    `json:"status"`
}

func (s DeployStatusServer) Order() int64  { return int64(s.OrderID) }
func (s DeployStatusServer) Server() int64 { return int64(s.SrvID) }

type DeployStatus struct {
	Servers []DeployStatusServer `json:"servers"`
}

func (c *Client) GetDeployStatus(ctx context.Context, orderIDs []int64) (*DeployStatus, error) {
	parts := make([]string, len(orderIDs))
	for i, id := range orderIDs {
		parts[i] = strconv.FormatInt(id, 10)
	}

	query := url.Values{"ids": {strings.Join(parts, ",")}}
	req, err := c.newRequest(ctx, http.MethodGet, "deploy/status", query, nil)
	if err != nil {
		return nil, err
	}

	var status DeployStatus
	if err := c.sendRequest(req, &status); err != nil {
		return nil, err
	}
	return &status, nil
}
