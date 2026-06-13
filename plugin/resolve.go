// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

// Name -> catalog id resolution, ported from terraform-provider-gigahost's
// server_resolvers.go.
package plugin

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pigeon-as/nomad-autoscaler-gigahost/internal/gigahost"
)

func resolveProduct(catalog *gigahost.DeployCatalog, productName string) (productID, priceID int64, err error) {
	var matches []gigahost.DeployProduct
	for _, t := range catalog.Tiers {
		for _, p := range t.Products {
			if strings.EqualFold(p.ProductName, productName) {
				matches = append(matches, p)
			}
		}
	}

	switch len(matches) {
	case 0:
		return 0, 0, fmt.Errorf("no product named %q in the catalog", productName)
	case 1:
		return matches[0].ProductID, matches[0].PriceID, nil
	default:
		return 0, 0, fmt.Errorf("%d products named %q in the catalog", len(matches), productName)
	}
}

func resolveRegion(catalog *gigahost.DeployCatalog, region string) (int64, error) {
	var matches []gigahost.DeployRegion
	for _, r := range catalog.Regions {
		if strings.EqualFold(r.RegionName, region) {
			matches = append(matches, r)
		}
	}

	switch len(matches) {
	case 0:
		return 0, fmt.Errorf("no region found named %q", region)
	case 1:
		if !matches[0].Active() {
			return 0, fmt.Errorf("region %q is not active", region)
		}
		id, err := strconv.ParseInt(matches[0].RegionID, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("region %q has an unparseable id %q: %w", region, matches[0].RegionID, err)
		}
		return id, nil
	default:
		return 0, fmt.Errorf("%d regions match %q", len(matches), region)
	}
}

// findOS looks up a deployable OS image by its catalog name or release
// codename — the os_name (e.g. "Ubuntu 24.04 LTS") or os_dist (e.g. "noble")
// the API returns, matched exactly.
func findOS(catalog []gigahost.OSCatalogEntry, os string) (*gigahost.OSCatalogEntry, error) {
	var matches []gigahost.OSCatalogEntry
	for _, e := range catalog {
		if strings.EqualFold(e.OS.OsName, os) || strings.EqualFold(e.OS.OsDist, os) {
			matches = append(matches, e)
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no OS image named %q in the catalog (use the os_name like %q or the codename like %q)", os, "Ubuntu 24.04 LTS", "noble")
	case 1:
		return &matches[0], nil
	default:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, m.OS.OsName)
		}
		return nil, fmt.Errorf("%d OS images match %q (%s)", len(matches), os, strings.Join(names, ", "))
	}
}

func resolveOS(catalog []gigahost.OSCatalogEntry, os string) (int64, error) {
	e, err := findOS(catalog, os)
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(e.OS.OsID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("OS %q has an unparseable id %q: %w", e.OS.OsName, e.OS.OsID, err)
	}
	return id, nil
}

func productOffersRegion(catalog *gigahost.DeployCatalog, productID, regionID int64) bool {
	for _, t := range catalog.Tiers {
		for _, p := range t.Products {
			if p.ProductID == productID {
				for _, id := range p.RegionIDs {
					if id == regionID {
						return true
					}
				}
				return false
			}
		}
	}
	return false
}
