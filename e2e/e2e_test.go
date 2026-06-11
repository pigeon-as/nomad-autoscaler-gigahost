// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/shoenig/test/must"

	"github.com/pigeon-as/nomad-autoscaler-gigahost/internal/gigahost"
)

const (
	autoscalerAddr = "http://127.0.0.1:8080"
	nodeMetaKey    = "unique.platform.gigahost.server_id"
)

var (
	autoscalerProc *exec.Cmd
	tmpDir         string
	policyDir      string
)

func TestMain(m *testing.M) {
	if os.Getenv("GIGAHOST_API_TOKEN") == "" {
		fmt.Fprintln(os.Stderr, "required env var GIGAHOST_API_TOKEN not set")
		os.Exit(1)
	}
	if _, err := exec.LookPath("nomad-autoscaler"); err != nil {
		fmt.Fprintln(os.Stderr, "nomad-autoscaler not found on PATH")
		os.Exit(1)
	}

	var err error
	tmpDir, err = os.MkdirTemp("", "gigahost-e2e-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating temp dir: %v\n", err)
		os.Exit(1)
	}

	if err := startAutoscaler(); err != nil {
		fmt.Fprintf(os.Stderr, "starting autoscaler: %v\n", err)
		os.RemoveAll(tmpDir)
		os.Exit(1)
	}

	code := m.Run()

	if autoscalerProc != nil && autoscalerProc.Process != nil {
		autoscalerProc.Process.Kill()
		autoscalerProc.Wait() //nolint:errcheck
	}
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

func startAutoscaler() error {
	// A zombie autoscaler from a previous run would win the port and fake health.
	if resp, err := http.Get(autoscalerAddr + "/v1/health"); err == nil {
		resp.Body.Close()
		return fmt.Errorf("something is already listening on %s — kill leftover autoscaler processes first", autoscalerAddr)
	}

	pluginDir := filepath.Join(tmpDir, "plugins")
	os.MkdirAll(pluginDir, 0o755)

	bin := findPluginBinary()
	if bin == "" {
		return fmt.Errorf("plugin binary not found (run make build)")
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		return fmt.Errorf("reading plugin binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, filepath.Base(bin)), data, 0o755); err != nil {
		return fmt.Errorf("copying plugin binary: %v", err)
	}

	// Policy files are written/removed per lifecycle test so they never run
	// concurrently.
	policyDir = filepath.Join(tmpDir, "policies")
	os.MkdirAll(policyDir, 0o755)

	// driver must equal the plugin binary filename. retry_attempts is lowered
	// so a scale-out whose node never joins the dev Nomad releases the
	// in-flight guard quickly.
	cfg := fmt.Sprintf(`log_level  = "DEBUG"
plugin_dir = %q

nomad {
  address = "http://127.0.0.1:4646"
}

http {
  bind_address = "127.0.0.1"
  bind_port    = 8080
}

policy {
  dir = %q
}

target "gigahost" {
  driver = "gigahost"
  config = {
    gigahost_api_token = %q
    retry_attempts     = "6"
  }
}
`, pluginDir, policyDir, os.Getenv("GIGAHOST_API_TOKEN"))

	cfgPath := filepath.Join(tmpDir, "autoscaler.hcl")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		return fmt.Errorf("writing config: %v", err)
	}

	autoscalerProc = exec.Command("nomad-autoscaler", "agent", "-config", cfgPath)
	autoscalerProc.Stdout = os.Stdout
	autoscalerProc.Stderr = os.Stderr
	if err := autoscalerProc.Start(); err != nil {
		return fmt.Errorf("starting autoscaler: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(autoscalerAddr + "/v1/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("autoscaler not healthy after 30s")
}

func findPluginBinary() string {
	for _, p := range []string{
		"../build/gigahost",
		"build/gigahost",
	} {
		if _, err := os.Stat(p); err == nil {
			abs, _ := filepath.Abs(p)
			return abs
		}
	}
	return ""
}

func lifecycleEnvSet() bool {
	for _, key := range []string{"E2E_PRODUCT_NAME", "E2E_REGION", "E2E_OS_DISTRO", "E2E_OS_VERSION"} {
		if os.Getenv(key) == "" {
			return false
		}
	}
	return true
}

func writeScaleOutPolicy(dir string) error {
	policy := fmt.Sprintf(`scaling "e2e-out" {
  enabled = true
  min     = 1
  max     = 1

  policy {
    evaluation_interval = "10s"
    cooldown            = "5m"
    on_check_error      = "ignore"

    check "placeholder" {
      source = "nomad-apm"
      query  = "percentage-allocated_cpu"

      strategy "target-value" {
        target = 70
      }
    }

    target "gigahost" {
      datacenter            = "dc1"
      node_class            = "gigahost-e2e"
      gigahost_product_name = %q
      gigahost_region       = %q
      gigahost_os_distro    = %q
      gigahost_os_version   = %q
      gigahost_ssh_keys     = %q
      gigahost_hostname     = %q
    }
  }
}
`, os.Getenv("E2E_PRODUCT_NAME"),
		os.Getenv("E2E_REGION"),
		os.Getenv("E2E_OS_DISTRO"),
		os.Getenv("E2E_OS_VERSION"),
		os.Getenv("E2E_SSH_KEYS"),
		envOrDefault("E2E_HOSTNAME", "e2e-test"))

	return os.WriteFile(filepath.Join(dir, "e2e-out.hcl"), []byte(policy), 0o644)
}

// min=0 over the dev agent's node (dc1): the autoscaler drains it and cancels
// the mapped server.
func writeScaleInPolicy(dir string) error {
	policy := `scaling "e2e-in" {
  enabled = true
  min     = 0
  max     = 1

  policy {
    evaluation_interval = "10s"
    cooldown            = "5m"
    on_check_error      = "ignore"

    check "placeholder" {
      source = "nomad-apm"
      query  = "percentage-allocated_cpu"

      strategy "target-value" {
        target = 70
      }
    }

    target "gigahost" {
      datacenter          = "dc1"
      node_drain_deadline = "2m"
    }
  }
}
`
	return os.WriteFile(filepath.Join(dir, "e2e-in.hcl"), []byte(policy), 0o644)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// The file policy source has no file watching — it re-scans only on SIGHUP,
// so every policy file change must be followed by a reload.
func reloadPolicies(t *testing.T) {
	t.Helper()
	if autoscalerProc == nil || autoscalerProc.Process == nil {
		t.Log("reload: no autoscaler process")
		return
	}
	if err := autoscalerProc.Process.Signal(syscall.SIGHUP); err != nil {
		t.Logf("reload: SIGHUP failed: %v", err)
	}
	time.Sleep(3 * time.Second)
}

func newGigahostClient(t *testing.T) *gigahost.Client {
	t.Helper()
	c, err := gigahost.NewClient(&gigahost.Config{
		Address: os.Getenv("GIGAHOST_BASE_URL"),
		Token:   os.Getenv("GIGAHOST_API_TOKEN"),
	})
	must.NoError(t, err)
	return c
}

func listServerIDs(c *gigahost.Client) ([]string, error) {
	servers, err := c.ListServers(context.Background())
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(servers))
	for _, s := range servers {
		ids = append(ids, s.SrvID)
	}
	return ids, nil
}

func cancelServer(c *gigahost.Client, id string) {
	c.CancelServer(context.Background(), id) //nolint:errcheck
}

func resolveDeployInput(t *testing.T, c *gigahost.Client) gigahost.DeployInput {
	t.Helper()
	ctx := context.Background()

	catalog, err := c.GetDeployCatalog(ctx)
	must.NoError(t, err)

	var productID, priceID, regionID int64
	for _, tier := range catalog.Tiers {
		for _, p := range tier.Products {
			if strings.EqualFold(p.ProductName, os.Getenv("E2E_PRODUCT_NAME")) {
				productID, priceID = p.ProductID, p.PriceID
			}
		}
	}
	must.True(t, productID != 0)

	for _, r := range catalog.Regions {
		if strings.EqualFold(r.RegionName, os.Getenv("E2E_REGION")) || strings.EqualFold(r.RegionNameShort, os.Getenv("E2E_REGION")) {
			id, err := strconv.ParseInt(r.RegionID, 10, 64)
			must.NoError(t, err)
			regionID = id
		}
	}
	must.True(t, regionID != 0)

	osCatalog, err := c.GetOSCatalog(ctx)
	must.NoError(t, err)
	var osID int64
	for _, e := range osCatalog {
		if strings.EqualFold(e.Distro.DistName, os.Getenv("E2E_OS_DISTRO")) &&
			strings.Contains(strings.ToLower(e.OS.OsName), strings.ToLower(os.Getenv("E2E_OS_VERSION"))) {
			id, err := strconv.ParseInt(e.OS.OsID, 10, 64)
			must.NoError(t, err)
			osID = id
		}
	}
	must.True(t, osID != 0)

	return gigahost.DeployInput{
		ProductID: productID,
		PriceID:   priceID,
		RegionID:  regionID,
		Quantity:  1,
		OSID:      &osID,
	}
}

// Installing servers would hold the plugin's install-guard. Ones we did not
// create are ghosts (orders have materialized 25-40 min late) and are
// cancelled on sight — dedicated test account.
func waitNoInstalling(t *testing.T, c *gigahost.Client, ours ...string) {
	t.Helper()
	protected := make(map[string]bool, len(ours))
	for _, id := range ours {
		protected[id] = true
	}

	deadline := time.After(5 * time.Minute)
	for poll := 0; ; poll++ {
		var blocking []string
		servers, err := c.ListServers(context.Background())
		if err == nil {
			for _, s := range servers {
				if s.Installing() && !s.Cancelled() {
					if !protected[s.SrvID] {
						t.Logf("cancelling ghost server %s (installing, not created by this test)", s.SrvID)
						cancelServer(c, s.SrvID)
					}
					blocking = append(blocking, s.SrvID)
				}
			}
			if len(blocking) == 0 {
				return
			}
			if poll%6 == 5 {
				t.Logf("still waiting: servers installing (would hold the plugin's install-guard): %v", blocking)
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for account to have no installing servers; blocking: %v", blocking)
		case <-time.After(10 * time.Second):
		}
	}
}

// A node the APM/plugin cannot see turns into a blind eval no-op — fail with
// the node state instead.
func waitNodeReady(t *testing.T, nc *nomadapi.Client, nodeID string) {
	t.Helper()
	deadline := time.After(2 * time.Minute)
	for {
		node, _, err := nc.Nodes().Info(nodeID, nil)
		if err == nil && node.Status == "ready" && node.SchedulingEligibility == "eligible" {
			t.Logf("node %s ready/eligible in datacenter %s", nodeID, node.Datacenter)
			return
		}
		select {
		case <-deadline:
			if err != nil {
				t.Fatalf("node %s not queryable: %v", nodeID, err)
			}
			t.Fatalf("node %s not ready: status=%s eligibility=%s", nodeID, node.Status, node.SchedulingEligibility)
		case <-time.After(5 * time.Second):
		}
	}
}

// The status view can omit in-flight orders; the server list, matched by
// order id, is the durable completion source (mirrors the plugin).
func waitServerReady(t *testing.T, c *gigahost.Client, orderIDs []int64) string {
	t.Helper()
	deadline := time.After(10 * time.Minute)
	for poll := 0; ; poll++ {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for deploy of orders %v to become ready", orderIDs)
		default:
		}

		status, err := c.GetDeployStatus(context.Background(), orderIDs)
		if err == nil {
			seen := false
			for _, s := range status.Servers {
				seen = true
				switch s.Status {
				case "ready":
					return strconv.FormatInt(s.Server(), 10)
				case "error", "failed", "cancelled", "suspended":
					t.Fatalf("deploy failed with status %q", s.Status)
				}
			}
			if !seen && poll%3 == 2 {
				if servers, lerr := c.ListServers(context.Background()); lerr == nil {
					for _, s := range servers {
						id, perr := strconv.ParseInt(s.Order.OrderID, 10, 64)
						if perr != nil || s.Cancelled() {
							continue
						}
						for _, want := range orderIDs {
							if id == want && !s.Installing() && s.Running() {
								return s.SrvID
							}
						}
					}
				}
			}
			if poll%12 == 11 {
				t.Logf("still waiting for deploy of orders %v", orderIDs)
			}
		}
		time.Sleep(5 * time.Second)
	}
}

func TestPluginHealthy(t *testing.T) {
	resp, err := http.Get(autoscalerAddr + "/v1/health")
	must.NoError(t, err)
	defer resp.Body.Close()
	must.Eq(t, 200, resp.StatusCode)
}

// A real server is deployed and mapped onto the dev node via dynamic node
// meta; a min=0 policy then drives drain -> meta lookup -> CancelServer ->
// ensureServersGone, all live.
func TestScaleInLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: -short")
	}
	if !lifecycleEnvSet() {
		t.Skip("skipping: E2E_PRODUCT_NAME, E2E_REGION, E2E_OS_DISTRO, E2E_OS_VERSION required")
	}

	client := newGigahostClient(t)
	ctx := context.Background()

	result, err := client.Deploy(ctx, resolveDeployInput(t, client))
	must.NoError(t, err)
	must.True(t, len(result.OrderIDs) > 0)
	srvID := waitServerReady(t, client, result.OrderIDs)
	t.Logf("deployed server %s for scale-in", srvID)
	t.Cleanup(func() { cancelServer(client, srvID) })

	// Dynamic node meta — the same key workers set during bootstrap.
	nomadClient, err := nomadapi.NewClient(nomadapi.DefaultConfig())
	must.NoError(t, err)
	nodes, _, err := nomadClient.Nodes().List(nil)
	must.NoError(t, err)
	must.True(t, len(nodes) > 0)
	nodeID := nodes[0].ID

	_, err = nomadClient.Nodes().Meta().Apply(&nomadapi.NodeMetaApplyRequest{
		NodeID: nodeID,
		Meta:   map[string]*string{nodeMetaKey: &srvID},
	}, nil)
	must.NoError(t, err)
	t.Cleanup(func() {
		_, _ = nomadClient.Nodes().Meta().Apply(&nomadapi.NodeMetaApplyRequest{
			NodeID: nodeID,
			Meta:   map[string]*string{nodeMetaKey: nil},
		}, nil)
		_, _ = nomadClient.Nodes().ToggleEligibility(nodeID, true, nil)
	})
	t.Logf("mapped Nomad node %s to server %s", nodeID, srvID)

	waitNoInstalling(t, client, srvID)
	waitNodeReady(t, nomadClient, nodeID)

	must.NoError(t, writeScaleInPolicy(policyDir))
	reloadPolicies(t)
	t.Cleanup(func() {
		os.Remove(filepath.Join(policyDir, "e2e-in.hcl")) //nolint:errcheck
		reloadPolicies(t)
	})

	t.Log("waiting for autoscaler to drain node and cancel server (up to 10 min)...")
	deadline := time.After(10 * time.Minute)
	for poll := 0; ; poll++ {
		select {
		case <-deadline:
			if node, _, err := nomadClient.Nodes().Info(nodeID, nil); err == nil {
				t.Logf("node state at timeout: status=%s eligibility=%s drain=%v", node.Status, node.SchedulingEligibility, node.Drain)
			}
			t.Fatal("timed out waiting for scale-in")
		default:
		}

		servers, err := client.ListServers(ctx)
		if err == nil {
			gone := true
			for _, s := range servers {
				if s.SrvID == srvID && !s.Cancelled() {
					gone = false
					break
				}
			}
			if gone {
				t.Logf("server %s cancelled by autoscaler", srvID)
				return
			}
		}

		if poll%4 == 3 {
			if node, _, err := nomadClient.Nodes().Info(nodeID, nil); err == nil {
				t.Logf("still waiting: node status=%s eligibility=%s drain=%v", node.Status, node.SchedulingEligibility, node.Drain)
			}
		}
		time.Sleep(15 * time.Second)
	}
}

func TestScaleLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: -short")
	}
	if !lifecycleEnvSet() {
		t.Skip("skipping: E2E_PRODUCT_NAME, E2E_REGION, E2E_OS_DISTRO, E2E_OS_VERSION required")
	}

	client := newGigahostClient(t)

	// No server-side tag filter and hostnames don't round-trip, so detection
	// and cleanup diff the whole account list — DEDICATED test account required.
	before, err := listServerIDs(client)
	must.NoError(t, err)

	// LIFO: policy removal (+ straggler settle) must run BEFORE this diff
	// cancel, or an in-flight eval deploys after the snapshot and escapes it.
	t.Cleanup(func() {
		after, _ := listServerIDs(client)
		known := make(map[string]bool, len(before))
		for _, id := range before {
			known[id] = true
		}
		for _, id := range after {
			if !known[id] {
				t.Logf("cleanup: cancelling %s", id)
				cancelServer(client, id)
			}
		}
	})

	must.NoError(t, writeScaleOutPolicy(policyDir))
	reloadPolicies(t)
	t.Cleanup(func() {
		os.Remove(filepath.Join(policyDir, "e2e-out.hcl")) //nolint:errcheck
		reloadPolicies(t)
		// The eval broker can redeliver a failed eval after its policy is
		// removed (observed at +76s); outlast it so the cancel sweep catches
		// anything it deploys.
		t.Log("cleanup: policy removed, settling before cancel sweep...")
		time.Sleep(120 * time.Second)
	})

	t.Log("waiting for autoscaler to deploy server (up to 20 min)...")
	deadline := time.After(20 * time.Minute)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for server creation")
		default:
		}

		after, _ := listServerIDs(client)
		known := make(map[string]bool, len(before))
		for _, id := range before {
			known[id] = true
		}
		for _, id := range after {
			if !known[id] {
				t.Logf("server created by autoscaler: %s", id)
				return
			}
		}
		time.Sleep(30 * time.Second)
	}
}
