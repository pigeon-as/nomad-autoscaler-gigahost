// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"fmt"
	"strconv"
	"strings"
)

// Agent-level keys (read in SetConfig).
const (
	configKeyAPIToken = "gigahost_api_token"
	// Test-only API address override.
	configKeyBaseURL       = "gigahost_base_url"
	configKeyRetryAttempts = "retry_attempts"

	configValueRetryAttemptsDefault = "60"
)

// Per-policy keys, resolved to catalog ids at scale-out. Names match the
// Gigahost API fields (and the terraform-provider-gigahost gigahost_server
// attributes); os_name/os_dist are mutually exclusive.
const (
	configKeyProductName    = "gigahost_product_name"
	configKeyRegionName     = "gigahost_region_name"
	configKeyOSName         = "gigahost_os_name"
	configKeyOSDist         = "gigahost_os_dist"
	configKeySSHKeys        = "gigahost_ssh_keys"
	configKeyHostnamePrefix = "gigahost_hostname_prefix"
	configKeyBackups        = "gigahost_backups"
)

func getConfigValue(config map[string]string, key, defaultValue string) string {
	if value, ok := config[key]; ok {
		return value
	}
	return defaultValue
}

func requireString(config map[string]string, key string) (string, error) {
	v := strings.TrimSpace(getConfigValue(config, key, ""))
	if v == "" {
		return "", fmt.Errorf("required config param %s not found", key)
	}
	return v, nil
}

// osImageFor returns the OS identifier for scale-out: exactly one of
// gigahost_os_name (e.g. "Ubuntu 24.04 LTS") or gigahost_os_dist (the
// codename, e.g. "noble"), matching the provider's mutually exclusive inputs.
func osImageFor(config map[string]string) (string, error) {
	name := strings.TrimSpace(getConfigValue(config, configKeyOSName, ""))
	dist := strings.TrimSpace(getConfigValue(config, configKeyOSDist, ""))
	switch {
	case name != "" && dist != "":
		return "", fmt.Errorf("set only one of %s or %s", configKeyOSName, configKeyOSDist)
	case name != "":
		return name, nil
	case dist != "":
		return dist, nil
	default:
		return "", fmt.Errorf("required config param %s or %s not found", configKeyOSName, configKeyOSDist)
	}
}

func parseInt64List(raw string) ([]int64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid integer %q: %v", p, err)
		}
		out = append(out, v)
	}
	return out, nil
}
