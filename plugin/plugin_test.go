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

	t.Run("hostname present", func(t *testing.T) {
		n := &nomadapi.Node{Attributes: map[string]string{nodeAttrHostname: "worker-ab12cd"}}
		id, err := gigahostNodeIDMap(n)
		must.NoError(t, err)
		must.Eq(t, "worker-ab12cd", id)
	})

	t.Run("hostname missing", func(t *testing.T) {
		n := &nomadapi.Node{Attributes: map[string]string{}}
		_, err := gigahostNodeIDMap(n)
		must.Error(t, err)
	})

	t.Run("hostname empty", func(t *testing.T) {
		n := &nomadapi.Node{Attributes: map[string]string{nodeAttrHostname: ""}}
		_, err := gigahostNodeIDMap(n)
		must.Error(t, err)
	})
}

func TestLastEventNanos(t *testing.T) {
	t.Parallel()

	must.Eq(t, int64(0), lastEventNanos(nil))
	must.Eq(t, int64(0), lastEventNanos([]gigahost.Server{{}}))

	servers := []gigahost.Server{
		{SrvDateCreated: 1781280000},
		{SrvDateCreated: 1781281000, SrvDeletedDate: 1781283000},
		{SrvDateCreated: 1781282000},
	}
	must.Eq(t, int64(1781283000)*int64(time.Second), lastEventNanos(servers))
}

func TestServerNameIndex(t *testing.T) {
	t.Parallel()
	tp := &TargetPlugin{logger: testLogger()}

	names, srvIDFor := tp.serverNameIndex([]gigahost.Server{
		{SrvID: "42", SrvName: "worker-aaaaaa"},
		{SrvID: "43", SrvName: "worker-bbbbbb"},
		{SrvID: "44", SrvName: ""},
		{SrvID: "45", SrvName: "worker-gone", Order: gigahost.ServerOrder{OrderStatus: "cancelled"}},
		{SrvID: "46", SrvName: "worker-dup"},
		{SrvID: "47", SrvName: "worker-dup"},
	})

	must.Len(t, 2, names)
	must.SliceContains(t, names, "worker-aaaaaa")
	must.SliceContains(t, names, "worker-bbbbbb")
	must.Eq(t, "42", srvIDFor["worker-aaaaaa"])
	must.Eq(t, "43", srvIDFor["worker-bbbbbb"])
	must.MapNotContainsKey(t, srvIDFor, "worker-dup")
	must.MapNotContainsKey(t, srvIDFor, "worker-gone")
	must.MapNotContainsKey(t, srvIDFor, "")
}

func TestHostnamesFor(t *testing.T) {
	t.Parallel()

	names, err := hostnamesFor("worker", 3)
	must.NoError(t, err)
	must.Len(t, 3, names)
	seen := map[string]bool{}
	for _, n := range names {
		must.StrHasPrefix(t, "worker-", n)
		must.Eq(t, len("worker-")+hostnameSuffixLen, len(n))
		must.False(t, seen[n])
		seen[n] = true
	}
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

// The wait polls deploy status only: a terminal status ends it, a failure
// status errors, and a server stuck installing times out naming its srv_id.
func TestWaitForServers(t *testing.T) {
	origPoll, origTimeout := serverDeployPollInterval, serverDeployTimeout
	serverDeployPollInterval = time.Millisecond
	t.Cleanup(func() { serverDeployPollInterval, serverDeployTimeout = origPoll, origTimeout })

	newClient := func(t *testing.T, handler http.HandlerFunc) *gigahost.Client {
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		c, err := gigahost.NewClient(&gigahost.Config{Address: srv.URL, Token: "t"})
		must.NoError(t, err)
		return c
	}

	t.Run("ready across two orders", func(t *testing.T) {
		serverDeployTimeout = origTimeout
		c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"meta":{},"data":{"servers":[
				{"order_id":"500","srv_id":"77","status":"ready"},
				{"order_id":"501","srv_id":"78","status":"ready"}
			]}}`))
		})
		tp := &TargetPlugin{client: c, logger: testLogger()}
		ids, err := tp.waitForServers(context.Background(), []int64{500, 501}, 2)
		must.NoError(t, err)
		must.SliceContainsAll(t, []string{"77", "78"}, ids)
	})

	t.Run("failure status errors", func(t *testing.T) {
		serverDeployTimeout = origTimeout
		c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"meta":{},"data":{"servers":[{"order_id":"500","srv_id":"77","status":"failed"}]}}`))
		})
		tp := &TargetPlugin{client: c, logger: testLogger()}
		_, err := tp.waitForServers(context.Background(), []int64{500}, 1)
		must.Error(t, err)
		must.StrContains(t, err.Error(), "failed")
	})

	t.Run("stuck installing times out naming the server", func(t *testing.T) {
		serverDeployTimeout = 20 * time.Millisecond
		c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"meta":{},"data":{"servers":[{"order_id":"500","srv_id":"77","status":"installing"}]}}`))
		})
		tp := &TargetPlugin{client: c, logger: testLogger()}
		_, err := tp.waitForServers(context.Background(), []int64{500}, 1)
		must.Error(t, err)
		must.StrContains(t, err.Error(), "timed out")
		must.StrContains(t, err.Error(), "77")
	})
}

func TestEnsureServersGone(t *testing.T) {
	origInterval := defaultRetryInterval
	defaultRetryInterval = time.Millisecond
	t.Cleanup(func() { defaultRetryInterval = origInterval })

	t.Run("cancelled status counts as gone", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			must.Eq(t, "/servers/42", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"meta":{},"data":[{"srv_id":"42","order":{"order_status":"cancelled"}}]}`))
		}))
		defer srv.Close()

		client, err := gigahost.NewClient(&gigahost.Config{Address: srv.URL, Token: "t"})
		must.NoError(t, err)

		tp := &TargetPlugin{client: client, retryAttempts: 2}
		must.NoError(t, tp.ensureServersGone(context.Background(), []string{"42"}))
	})

	t.Run("404 is definitive", func(t *testing.T) {
		polls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			polls++
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"meta":{"message":"404 Not Found"},"data":[]}`))
		}))
		defer srv.Close()

		client, err := gigahost.NewClient(&gigahost.Config{Address: srv.URL, Token: "t"})
		must.NoError(t, err)

		tp := &TargetPlugin{client: client, retryAttempts: 2}
		must.NoError(t, tp.ensureServersGone(context.Background(), []string{"42"}))
		must.Eq(t, 1, polls)
	})
}

func TestDeleteServerRefusedCancellation(t *testing.T) {
	t.Parallel()

	t.Run("refused but confirmed gone", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/servers/42/cancel":
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"meta":{"message":"No order was found."},"data":[]}`))
			case "/servers/42":
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"meta":{"message":"404 Not Found"},"data":[]}`))
			}
		}))
		defer srv.Close()

		client, err := gigahost.NewClient(&gigahost.Config{Address: srv.URL, Token: "t"})
		must.NoError(t, err)

		tp := &TargetPlugin{client: client, logger: testLogger()}
		must.NoError(t, tp.deleteServer(context.Background(), "42"))
	})

	t.Run("refused and still live", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/servers/42/cancel":
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"meta":{"message":"refused"},"data":[]}`))
			case "/servers/42":
				_, _ = w.Write([]byte(`{"meta":{},"data":[{"srv_id":"42","srv_status":"1","order":{"order_status":"active"}}]}`))
			}
		}))
		defer srv.Close()

		client, err := gigahost.NewClient(&gigahost.Config{Address: srv.URL, Token: "t"})
		must.NoError(t, err)

		tp := &TargetPlugin{client: client, logger: testLogger()}
		err = tp.deleteServer(context.Background(), "42")
		must.Error(t, err)
		must.StrContains(t, err.Error(), "refused")
	})
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
	t.Run("short name no longer matches", func(t *testing.T) {
		_, err := resolveRegion(cat, "sdj")
		must.Error(t, err)
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
		{Distro: gigahost.Distro{DistName: "Ubuntu", DistValue: "ubuntu"}, OS: gigahost.OSImage{OsID: "102", OsName: "Ubuntu 24.04 LTS", OsDist: "noble"}},
		{Distro: gigahost.Distro{DistName: "Ubuntu", DistValue: "ubuntu"}, OS: gigahost.OSImage{OsID: "93", OsName: "Ubuntu 22.04 LTS", OsDist: "jammy"}},
		{Distro: gigahost.Distro{DistName: "Debian", DistValue: "debian"}, OS: gigahost.OSImage{OsID: "50", OsName: "Debian 12", OsDist: "bookworm"}},
	}

	t.Run("match by os_name", func(t *testing.T) {
		id, err := resolveOS(cat, "Ubuntu 24.04 LTS")
		must.NoError(t, err)
		must.Eq(t, int64(102), id)
	})
	t.Run("match by codename", func(t *testing.T) {
		id, err := resolveOS(cat, "noble")
		must.NoError(t, err)
		must.Eq(t, int64(102), id)
	})
	t.Run("exact match only, not substring", func(t *testing.T) {
		_, err := resolveOS(cat, "24.04")
		must.Error(t, err)
	})
	t.Run("not found", func(t *testing.T) {
		_, err := resolveOS(cat, "Ubuntu 18.04 LTS")
		must.Error(t, err)
	})
}
