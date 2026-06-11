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

Each lifecycle test writes its own policy file and removes it afterwards, so
the two never run concurrently. The autoscaler's file policy source only
re-scans the directory on reload, so every policy change is followed by a
SIGHUP to the agent.

**TestPluginHealthy** checks the autoscaler health endpoint (validating the full
go-plugin RPC path: binary discovery → launch → SetConfig).

**TestScaleLifecycle** waits for the autoscaler to evaluate the min=1 policy and
deploy a Gigahost server, then cleans up via the Gigahost API. Gigahost has no
server-side tag filter and does not surface the requested hostname in
`srv_hostname` (verified live), so detection and cleanup diff the whole account
server list — **a dedicated test account is required**: any server created on
the account during the test window will be detected and cancelled.

Note: with the plugin's post-scale-out wait for Nomad nodes, the deployed
server never joins the dev Nomad here, so the autoscaler eventually logs a
scale-out failure after the test has already detected the server — expected.
