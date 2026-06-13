# E2E Tests

Tests the plugin through a real nomad-autoscaler agent, connected to a Nomad
dev cluster and the Gigahost API.

**Warning**: The lifecycle tests deploy real Gigahost servers and incur costs.
They diff the whole account server list for detection and cleanup, so a
**dedicated test account is required** — any server created on the account
during the test window may be cancelled.

## Requirements

- Linux. `nomad` and `nomad-autoscaler` on PATH
- A Gigahost API token (dedicated test account)

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `GIGAHOST_API_TOKEN` | Yes | | Gigahost API token |
| `GIGAHOST_BASE_URL` | No | `https://api.gigahost.no/api/v0` | Override the API address |
| `E2E_PRODUCT_NAME` | Lifecycle only | | Catalog product name (e.g. `KVM Performance VPS 4GB`) |
| `E2E_REGION` | Lifecycle only | | Region name (e.g. `Sandefjord`) |
| `E2E_OS` | Lifecycle only | | OS image name or codename (e.g. `Ubuntu 24.04 LTS` or `noble`) |
| `E2E_SSH_KEYS` | No | | Comma-separated SSH key ids |
| `E2E_HOSTNAME` | No | `e2e-test` | Hostname prefix for new servers |

## Usage

Start the Nomad dev agent in one terminal and run the tests in another:

```sh
make dev
```

```sh
export GIGAHOST_API_TOKEN="flux_live_..."
export E2E_PRODUCT_NAME="KVM Performance VPS 4GB"
export E2E_REGION="Sandefjord"
export E2E_OS="Ubuntu 24.04 LTS"
make e2e
```

Without the `E2E_*` variables only **TestPluginHealthy** runs, which verifies
the autoscaler loads the plugin binary via go-plugin RPC and reports healthy.

## Tests

The harness starts a `nomad-autoscaler` agent as a subprocess with the built
plugin binary, writes a policy file per lifecycle test, and SIGHUPs the agent
on every policy change (the file policy source only re-scans on reload).

**TestScaleInLifecycle** deploys two real servers and renames them to two
class nodes' hostnames (in production the deploy itself sets the hostname); a
min=1 policy then makes the autoscaler drain one node and cancel exactly its
server. The second Nomad client is started by the test in its own UTS
namespace so it fingerprints a distinct hostname.

**TestScaleLifecycle** waits for the autoscaler to evaluate a min=1 policy and
deploy a server, then cancels it. The deployed server never joins the dev
Nomad, so the autoscaler logs a scale-out failure after the test has already
detected the server — expected.
