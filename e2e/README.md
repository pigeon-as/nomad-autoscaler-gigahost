# E2E Tests

Tests the Gigahost autoscaler plugin through a real nomad-autoscaler agent,
connected to a Nomad dev cluster and the Gigahost API.

**Warning**: The lifecycle test deploys real Gigahost servers and incurs costs.

## Requirements

- `nomad` on PATH
- `nomad-autoscaler` on PATH
- A Gigahost API token

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `GIGAHOST_API_TOKEN` | Yes | | Gigahost API token |
| `GIGAHOST_BASE_URL` | No | `https://api.gigahost.no/api/v0` | Override the API address |
| `E2E_PRODUCT_NAME` | Lifecycle only | | Catalog product name (e.g. `KVM Value VPS 4GB`) |
| `E2E_REGION` | Lifecycle only | | Region name or short name (e.g. `Sandefjord`) |
| `E2E_OS_DISTRO` | Lifecycle only | | OS distribution (e.g. `Ubuntu`) |
| `E2E_OS_VERSION` | Lifecycle only | | OS version (e.g. `24.04`) |
| `E2E_SSH_KEYS` | No | | Comma-separated SSH key ids |
| `E2E_HOSTNAME` | No | `e2e-test` | Hostname for new servers |

## Usage

In one terminal, start the Nomad dev agent:

```sh
make dev
```

In another terminal, run the tests:

```sh
export GIGAHOST_API_TOKEN="flux_live_..."
make e2e
```

This runs **TestPluginHealthy**, which verifies the autoscaler loads our plugin
binary via go-plugin RPC and reports healthy.

### Lifecycle test (optional)

To also run the full server-deploy lifecycle through the autoscaler:

```sh
export E2E_PRODUCT_NAME="KVM Value VPS 4GB"
export E2E_REGION="Sandefjord"
export E2E_OS_DISTRO="Ubuntu"
export E2E_OS_VERSION="24.04"
make e2e
```

(These are the same product/region/OS names the terraform-provider-gigahost
`gigahost_server` resource uses; the plugin resolves them to catalog ids.)

## How it works

The test starts a `nomad-autoscaler` agent as a subprocess with:

- Our built plugin binary in a temp plugin dir (the binary, `driver`, and the
  plugin's reported name must all match — here `gigahost`)
- A generated agent config with the Gigahost token from env
- A scaling policy (min=1) if the lifecycle env vars are set

**TestPluginHealthy** checks the autoscaler health endpoint (validating the full
go-plugin RPC path: binary discovery → launch → SetConfig).

**TestScaleLifecycle** waits for the autoscaler to evaluate the min=1 policy and
deploy a Gigahost server, then cleans up via the Gigahost API. Because Gigahost
has no server-side tag filter, the test detects the new server by diffing the
account's server list.
