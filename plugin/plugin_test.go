// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad-autoscaler/sdk/helper/scaleutils"
	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/shoenig/test/must"

	"github.com/pigeon-as/nomad-autoscaler-gigahost/internal/gigahost"
)

func testLogger() hclog.Logger { return hclog.NewNullLogger() }

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

	t.Run("meta fallback", func(t *testing.T) {
		n := &nomadapi.Node{
			Attributes: map[string]string{},
			Meta:       map[string]string{nodeAttrGigahostServerID: "43"},
		}
		id, err := gigahostNodeIDMap(n)
		must.NoError(t, err)
		must.Eq(t, "43", id)
	})

	t.Run("attribute wins over meta", func(t *testing.T) {
		n := &nomadapi.Node{
			Attributes: map[string]string{nodeAttrGigahostServerID: "42"},
			Meta:       map[string]string{nodeAttrGigahostServerID: "43"},
		}
		id, err := gigahostNodeIDMap(n)
		must.NoError(t, err)
		must.Eq(t, "42", id)
	})

	t.Run("missing everywhere", func(t *testing.T) {
		n := &nomadapi.Node{Attributes: map[string]string{}, Meta: map[string]string{}}
		_, err := gigahostNodeIDMap(n)
		must.Error(t, err)
	})

	t.Run("empty values", func(t *testing.T) {
		n := &nomadapi.Node{
			Attributes: map[string]string{nodeAttrGigahostServerID: ""},
			Meta:       map[string]string{nodeAttrGigahostServerID: ""},
		}
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
			{ProductID: 1, PriceID: 10, ProductName: "KVM Value VPS 4GB", RegionIDs: []int64{3}},
			{ProductID: 2, PriceID: 20, ProductName: "KVM Value VPS 8GB", RegionIDs: []int64{4}},
		}}},
		Regions: []gigahost.DeployRegion{
			{RegionID: "3", RegionName: "Sandefjord", RegionNameShort: "sdj", RegionActive: true},
			{RegionID: "4", RegionName: "Oslo", RegionNameShort: "osl", RegionActive: false},
		},
	}
}

// Status view omits the order; the server list (matched by order id)
// completes the wait.
func TestWaitForServersListFallback(t *testing.T) {
	orig := serverDeployPollInterval
	serverDeployPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { serverDeployPollInterval = orig })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/deploy/status":
			_, _ = w.Write([]byte(`{"meta":{},"data":{"servers":[]}}`))
		case "/servers":
			_, _ = w.Write([]byte(`{"meta":{},"data":[
				{"srv_id":"77","srv_status":"1","srv_status_install":"0","order":{"order_id":"500","order_status":"active"}}
			]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client, err := gigahost.NewClient(&gigahost.Config{Address: srv.URL, Token: "t"})
	must.NoError(t, err)

	tp := &TargetPlugin{client: client, logger: testLogger(), retryAttempts: 1}
	ids, err := tp.waitForServers(context.Background(), []int64{500}, 1)
	must.NoError(t, err)
	must.Eq(t, 1, len(ids))
	must.Eq(t, "77", ids[0])
}

// A previously observed server that disappears from both views is gone.
func TestWaitForServersGone(t *testing.T) {
	orig := serverDeployPollInterval
	serverDeployPollInterval = time.Millisecond
	t.Cleanup(func() { serverDeployPollInterval = orig })

	first := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/deploy/status":
			if first {
				first = false
				_, _ = w.Write([]byte(`{"meta":{},"data":{"servers":[{"order_id":"500","srv_id":"77","status":"installing"}]}}`))
				return
			}
			_, _ = w.Write([]byte(`{"meta":{},"data":{"servers":[]}}`))
		case "/servers":
			_, _ = w.Write([]byte(`{"meta":{},"data":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client, err := gigahost.NewClient(&gigahost.Config{Address: srv.URL, Token: "t"})
	must.NoError(t, err)

	tp := &TargetPlugin{client: client, logger: testLogger(), retryAttempts: 1}
	_, err = tp.waitForServers(context.Background(), []int64{500}, 1)
	must.Error(t, err)
	must.StrContains(t, err.Error(), "disappeared")
	must.StrContains(t, err.Error(), "77")
}

func TestEnsureServersGone(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"servers absent":    `{"meta":{},"data":[]}`,
		"servers cancelled": `{"meta":{},"data":[{"srv_id":"42","order":{"order_status":"cancelled"}}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			client, err := gigahost.NewClient(&gigahost.Config{Address: srv.URL, Token: "t"})
			must.NoError(t, err)

			tp := &TargetPlugin{client: client, retryAttempts: 1}
			err = tp.ensureServersGone(context.Background(), []scaleutils.NodeResourceID{
				{NomadNodeID: "n1", RemoteResourceID: "42"},
			})
			must.NoError(t, err)
		})
	}
}

func TestProductOffersRegion(t *testing.T) {
	t.Parallel()
	cat := testCatalog()

	t.Run("offered", func(t *testing.T) {
		must.True(t, productOffersRegion(cat, 1, 3))
	})
	t.Run("not offered", func(t *testing.T) {
		must.False(t, productOffersRegion(cat, 1, 4))
	})
	t.Run("unknown product", func(t *testing.T) {
		must.False(t, productOffersRegion(cat, 99, 3))
	})
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
