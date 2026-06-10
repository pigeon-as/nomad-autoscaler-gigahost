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
	configKeyBaseURL  = "gigahost_base_url"
)

// Per-policy keys (read in Scale/scaleOut). product/region/OS mirror the TF
// provider's gigahost_server inputs and are resolved to catalog ids at scale-out.
const (
	configKeyProductName = "gigahost_product_name"
	configKeyRegion      = "gigahost_region"
	configKeyOSDistro    = "gigahost_os_distro"
	configKeyOSVersion   = "gigahost_os_version"
	configKeySSHKeys     = "gigahost_ssh_keys"
	configKeyHostname    = "gigahost_hostname"
	configKeyBackups     = "gigahost_backups"
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
