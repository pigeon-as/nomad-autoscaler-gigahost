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
	"testing"
	"time"

	"github.com/shoenig/test/must"

	"github.com/pigeon-as/nomad-autoscaler-gigahost/internal/gigahost"
)

const autoscalerAddr = "http://127.0.0.1:8080"

var (
	autoscalerProc *exec.Cmd
	tmpDir         string
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

	policyDir := filepath.Join(tmpDir, "policies")
	os.MkdirAll(policyDir, 0o755)

	if lifecycleEnvSet() {
		if err := writeLifecyclePolicy(policyDir); err != nil {
			return fmt.Errorf("writing policy: %v", err)
		}
	}

	// driver is the plugin binary filename in plugin_dir; the label is the policy target name.
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

func writeLifecyclePolicy(dir string) error {
	policy := fmt.Sprintf(`scaling "e2e" {
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

	return os.WriteFile(filepath.Join(dir, "e2e.hcl"), []byte(policy), 0o644)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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

func TestPluginHealthy(t *testing.T) {
	resp, err := http.Get(autoscalerAddr + "/v1/health")
	must.NoError(t, err)
	defer resp.Body.Close()
	must.Eq(t, 200, resp.StatusCode)
}

func TestScaleLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: -short")
	}
	if !lifecycleEnvSet() {
		t.Skip("skipping: E2E_PRODUCT_NAME, E2E_REGION, E2E_OS_DISTRO, E2E_OS_VERSION required")
	}

	client := newGigahostClient(t)

	// No server-side tag filter: detect the autoscaler's server by diffing the account list.
	before, err := listServerIDs(client)
	must.NoError(t, err)

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
