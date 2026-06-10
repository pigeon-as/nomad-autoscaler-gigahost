// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package gigahost

import (
	"context"
	"net/http"
	"path"
)

type DeployProduct struct {
	ProductID   int64   `json:"product_id"`
	ProductName string  `json:"product_name"`
	PriceID     int64   `json:"price_id"`
	RegionIDs   []int64 `json:"region_ids"`
}

type DeployTier struct {
	Products []DeployProduct `json:"products"`
}

type DeployRegion struct {
	RegionID        string   `json:"region_id"`
	RegionName      string   `json:"region_name"`
	RegionNameShort string   `json:"region_name_short"`
	RegionActive    flexBool `json:"region_active"`
}

func (r DeployRegion) Active() bool { return bool(r.RegionActive) }

type DeployCatalog struct {
	Tiers   []DeployTier   `json:"tiers"`
	Regions []DeployRegion `json:"regions"`
}

func (c *Client) GetDeployCatalog(ctx context.Context) (*DeployCatalog, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "deploy/servers", nil, nil)
	if err != nil {
		return nil, err
	}

	var catalog DeployCatalog
	if err := c.sendRequest(req, &catalog); err != nil {
		return nil, err
	}
	return &catalog, nil
}

type Distro struct {
	DistID    string `json:"dist_id"`
	DistName  string `json:"dist_name"`
	DistValue string `json:"dist_value"`
}

type OSImage struct {
	OsID   string `json:"os_id"`
	OsName string `json:"os_name"`
	OsDist string `json:"os_dist"`
}

type OSCatalogEntry struct {
	Distro Distro
	OS     OSImage
}

// GetOSCatalog flattens distros and their versions into one list (one GET per
// distro), mirroring terraform-provider-gigahost.
func (c *Client) GetOSCatalog(ctx context.Context) ([]OSCatalogEntry, error) {
	distros, err := c.getDistros(ctx)
	if err != nil {
		return nil, err
	}

	var catalog []OSCatalogEntry
	for _, d := range distros {
		versions, err := c.getDistroVersions(ctx, d.DistID)
		if err != nil {
			return nil, err
		}
		for _, img := range versions {
			catalog = append(catalog, OSCatalogEntry{Distro: d, OS: img})
		}
	}
	return catalog, nil
}

func (c *Client) getDistros(ctx context.Context) ([]Distro, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "reinstall/distro", nil, nil)
	if err != nil {
		return nil, err
	}

	var distros []Distro
	if err := c.sendRequest(req, &distros); err != nil {
		return nil, err
	}
	return distros, nil
}

func (c *Client) getDistroVersions(ctx context.Context, distID string) ([]OSImage, error) {
	req, err := c.newRequest(ctx, http.MethodGet, path.Join("reinstall", "distro", distID), nil, nil)
	if err != nil {
		return nil, err
	}

	var versions []OSImage
	if err := c.sendRequest(req, &versions); err != nil {
		return nil, err
	}
	return versions, nil
}
