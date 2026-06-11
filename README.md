# nomad-autoscaler-gigahost

Nomad Autoscaler [target plugin](https://developer.hashicorp.com/nomad/tools/autoscaling/plugins/target) for horizontal cluster scaling via [Gigahost](https://gigahost.no) cloud servers (hourly-billed KVM VPS or dedicated bare metal).

Deploys new servers when cluster resources are exhausted and cancels idle servers on scale-in. Servers are identified by the Nomad node meta key `unique.platform.gigahost.server_id` (the Gigahost `srv_id`), which workers set during bootstrap — either in the client config:

```hcl
client {
  meta {
    "unique.platform.gigahost.server_id" = "17536"
  }
}
```

or at runtime with `nomad node meta apply 'unique.platform.gigahost.server_id=17536'`. (A node *attribute* of the same name is also honoured and takes precedence, but Nomad has no Gigahost fingerprinter, so meta is the practical path.)

## Agent Configuration

```hcl
target "gigahost" {
  driver = "gigahost"
  config {
    gigahost_api_token = "flux_live_..."
  }
}
```

| Key | Default | Description |
|-----|---------|-------------|
| `gigahost_api_token` | required | Gigahost API token (created under **Account → API keys**). Falls back to the `GIGAHOST_API_TOKEN` environment variable |
| `retry_attempts` | `60` | Number of 10s attempts to wait for new nodes to join the Nomad pool after a scale-out |

### Nomad ACL

When using a Nomad cluster with ACLs enabled, the plugin requires a token with:

```hcl
node {
  policy = "write"
}
```

## Policy Configuration

A cluster policy that adds workers when the pool runs low on schedulable
capacity — i.e. Nomad can no longer place new allocations, not when CPU/RAM
utilization is high:

```hcl
scaling "gigahost_workers" {
  enabled = true
  min     = 1
  max     = 10

  policy {
    cooldown            = "15m"
    evaluation_interval = "30s"

    check "cpu_allocated" {
      source = "nomad-apm"
      query  = "node_percentage-allocated_cpu"
      strategy "target-value" {
        target = 80
      }
    }

    check "mem_allocated" {
      source = "nomad-apm"
      query  = "node_percentage-allocated_memory"
      strategy "target-value" {
        target = 80
      }
    }

    target "gigahost" {
      node_pool              = "workers"
      node_drain_deadline    = "15m"
      node_purge             = "true"
      node_selector_strategy = "least_busy"

      gigahost_product_name = "KVM Value VPS 4GB"
      gigahost_region       = "Sandefjord"
      gigahost_os_distro    = "Ubuntu"
      gigahost_os_version   = "24.04"
      gigahost_ssh_keys     = "101,102"
    }
  }
}
```

Both checks track **allocated** (reserved) capacity, not utilization; with two
the autoscaler scales out on whichever resource — CPU or memory — is tightest.

| Key | Default | Description |
|-----|---------|-------------|
| `gigahost_product_name` | `""` | Catalog product name, e.g. `KVM Value VPS 4GB`. Required for scale-out |
| `gigahost_region` | `""` | Region name or short name, e.g. `Sandefjord`. Required for scale-out |
| `gigahost_os_distro` | `""` | OS distribution to install, e.g. `Ubuntu`. Required for scale-out |
| `gigahost_os_version` | `""` | OS version, e.g. `24.04`. Required for scale-out |
| `gigahost_ssh_keys` | `""` | Comma-separated SSH key ids to authorize on new servers |
| `gigahost_hostname` | `""` | Hostname for new servers; applied only when a scale-out creates a single server. **Leave empty** so Gigahost assigns unique hostnames |
| `gigahost_backups` | `false` | Enable daily backups (adds 25% to the price) |
| `datacenter` | `""` | Nomad client datacenter filter |
| `node_class` | `""` | Nomad client node class filter |
| `node_pool` | `""` | Nomad client node pool filter |
| `node_drain_deadline` | `15m` | Drain deadline before cancellation |
| `node_purge` | `false` | Purge the Nomad node after cancellation |
| `node_selector_strategy` | `least_busy` | Node selection strategy for scale-in |

At least one of `datacenter`, `node_class`, or `node_pool` is required — it identifies the pool that this policy scales.

### Product, region, and OS names

These are the same names the [terraform-provider-gigahost](https://github.com/pigeon-as/terraform-provider-gigahost) `gigahost_server` resource uses, resolved to catalog ids at scale-out.

## Delivery Latency

Gigahost servers take several minutes to deploy, install, and join the cluster. Gigahost isn't a scale set — there's no provider-side desired-capacity (resize) API like AWS ASG / Azure VMSS / GCE MIG have — so three mechanisms guard against double-deploying:

- A scale-out is one batch deploy order; `Scale` blocks until every server reports ready (30-minute timeout) **and** the new nodes have joined the Nomad pool (`retry_attempts` × 10s), so the count reflects the new capacity before the next evaluation.
- `Status` reports the target not-ready while a scaling action is executing and while any server on the account is still installing — the latter, unlike cooldown, survives autoscaler restarts. (The install check is account-wide: a manual deploy on the same account briefly pauses autoscaling.)
- The policy **`cooldown`** (above) covers the rest.

A server that deploys but never joins Nomad fails the scale-out with its `server_id` in the log, and is invisible to scale-in — alert on scale-out failures and cancel such servers manually.

## Build

```bash
make build    # → build/gigahost
make test
make vet
```

## License

MPL-2.0
