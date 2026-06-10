// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"testing"

	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/shoenig/test/must"

	"github.com/pigeon-as/nomad-autoscaler-gigahost/internal/gigahost"
)

func TestCalculateDirection(t *testing.T) {
	t.Parallel()
	tp := &TargetPlugin{}

	cases := map[string]struct {
		current, desired int64
		wantNum          int64
		wantDir          string
	}{
		"scale out returns total": {current: 2, desired: 5, wantNum: 5, wantDir: "out"},
		"scale in returns delta":  {current: 5, desired: 2, wantNum: 3, wantDir: "in"},
		"no change":               {current: 3, desired: 3, wantNum: 0, wantDir: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			num, dir := tp.calculateDirection(tc.current, tc.desired)
			must.Eq(t, tc.wantNum, num)
			must.Eq(t, tc.wantDir, dir)
		})
	}
}

func TestGigahostNodeIDMap(t *testing.T) {
	t.Parallel()

	t.Run("attribute present", func(t *testing.T) {
		n := &nomadapi.Node{Attributes: map[string]string{nodeAttrGigahostServerID: "42"}}
		id, err := gigahostNodeIDMap(n)
		must.NoError(t, err)
		must.Eq(t, "42", id)
	})

	t.Run("attribute missing", func(t *testing.T) {
		n := &nomadapi.Node{Attributes: map[string]string{}}
		_, err := gigahostNodeIDMap(n)
		must.Error(t, err)
	})

	t.Run("attribute empty", func(t *testing.T) {
		n := &nomadapi.Node{Attributes: map[string]string{nodeAttrGigahostServerID: ""}}
		_, err := gigahostNodeIDMap(n)
		must.Error(t, err)
	})
}

func TestRequireString(t *testing.T) {
	t.Parallel()

	t.Run("present", func(t *testing.T) {
		v, err := requireString(map[string]string{configKeyProductName: "KVM Value VPS 4GB"}, configKeyProductName)
		must.NoError(t, err)
		must.Eq(t, "KVM Value VPS 4GB", v)
	})
	t.Run("missing", func(t *testing.T) {
		_, err := requireString(map[string]string{}, configKeyProductName)
		must.Error(t, err)
	})
	t.Run("blank", func(t *testing.T) {
		_, err := requireString(map[string]string{configKeyProductName: "  "}, configKeyProductName)
		must.Error(t, err)
	})
}

func TestParseInt64List(t *testing.T) {
	t.Parallel()

	t.Run("comma separated with whitespace", func(t *testing.T) {
		ids, err := parseInt64List("1, 2 ,3")
		must.NoError(t, err)
		must.Eq(t, 3, len(ids))
		must.Eq(t, int64(1), ids[0])
		must.Eq(t, int64(2), ids[1])
		must.Eq(t, int64(3), ids[2])
	})
	t.Run("empty is nil", func(t *testing.T) {
		ids, err := parseInt64List("")
		must.NoError(t, err)
		must.Eq(t, 0, len(ids))
	})
	t.Run("invalid entry", func(t *testing.T) {
		_, err := parseInt64List("1,x")
		must.Error(t, err)
	})
}

func testCatalog() *gigahost.DeployCatalog {
	return &gigahost.DeployCatalog{
		Tiers: []gigahost.DeployTier{{Products: []gigahost.DeployProduct{
			{ProductID: 1, PriceID: 10, ProductName: "KVM Value VPS 4GB"},
			{ProductID: 2, PriceID: 20, ProductName: "KVM Value VPS 8GB"},
		}}},
		Regions: []gigahost.DeployRegion{
			{RegionID: "3", RegionName: "Sandefjord", RegionNameShort: "sdj", RegionActive: true},
			{RegionID: "4", RegionName: "Oslo", RegionNameShort: "osl", RegionActive: false},
		},
	}
}

func TestResolveProduct(t *testing.T) {
	t.Parallel()
	cat := testCatalog()

	t.Run("case-insensitive match returns product and price id", func(t *testing.T) {
		pid, prid, err := resolveProduct(cat, "kvm value vps 4gb")
		must.NoError(t, err)
		must.Eq(t, int64(1), pid)
		must.Eq(t, int64(10), prid)
	})
	t.Run("not found", func(t *testing.T) {
		_, _, err := resolveProduct(cat, "nope")
		must.Error(t, err)
	})
}

func TestResolveRegion(t *testing.T) {
	t.Parallel()
	cat := testCatalog()

	t.Run("match by name", func(t *testing.T) {
		id, err := resolveRegion(cat, "Sandefjord")
		must.NoError(t, err)
		must.Eq(t, int64(3), id)
	})
	t.Run("match by short name", func(t *testing.T) {
		id, err := resolveRegion(cat, "sdj")
		must.NoError(t, err)
		must.Eq(t, int64(3), id)
	})
	t.Run("inactive region", func(t *testing.T) {
		_, err := resolveRegion(cat, "Oslo")
		must.Error(t, err)
	})
	t.Run("not found", func(t *testing.T) {
		_, err := resolveRegion(cat, "Bergen")
		must.Error(t, err)
	})
}

func TestResolveOS(t *testing.T) {
	t.Parallel()
	cat := []gigahost.OSCatalogEntry{
		{Distro: gigahost.Distro{DistName: "Ubuntu", DistValue: "ubuntu"}, OS: gigahost.OSImage{OsID: "42", OsName: "Ubuntu 24.04", OsDist: "24.04"}},
		{Distro: gigahost.Distro{DistName: "Ubuntu", DistValue: "ubuntu"}, OS: gigahost.OSImage{OsID: "43", OsName: "Ubuntu 22.04", OsDist: "22.04"}},
		{Distro: gigahost.Distro{DistName: "Debian", DistValue: "debian"}, OS: gigahost.OSImage{OsID: "50", OsName: "Debian 12", OsDist: "12"}},
	}

	t.Run("distro and version", func(t *testing.T) {
		id, err := resolveOS(cat, "Ubuntu", "24.04")
		must.NoError(t, err)
		must.Eq(t, int64(42), id)
	})
	t.Run("not found", func(t *testing.T) {
		_, err := resolveOS(cat, "Ubuntu", "18.04")
		must.Error(t, err)
	})
	t.Run("ambiguous version", func(t *testing.T) {
		_, err := resolveOS(cat, "Ubuntu", "")
		must.Error(t, err)
	})
}
