# nomad-autoscaler-gigahost

Nomad Autoscaler [target plugin](https://developer.hashicorp.com/nomad/tools/autoscaling/plugins/target) for horizontal cluster scaling via [Gigahost](https://gigahost.no) cloud servers (hourly-billed KVM VPS or dedicated bare metal).

Servers are named `<gigahost_hostname_prefix>-<random>` at deploy. Gigahost sets the OS hostname from the name, Nomad fingerprints it, and scale-in uses it to match nodes back to servers. Don't rename autoscaler-managed servers or override their OS hostnames.

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
      query  = "percentage-allocated_cpu"
      strategy "target-value" {
        target = 80
      }
    }

    check "mem_allocated" {
      source = "nomad-apm"
      query  = "percentage-allocated_memory"
      strategy "target-value" {
        target = 80
      }
    }

    target "gigahost" {
      node_class             = "workers"
      node_drain_deadline    = "15m"
      node_purge             = "true"
      node_selector_strategy = "least_busy"

      gigahost_product_name    = "KVM Performance VPS 4GB"
      gigahost_region          = "Sandefjord"
      gigahost_os_distro       = "Ubuntu"
      gigahost_os_version      = "24.04"
      gigahost_hostname_prefix = "worker"
      gigahost_ssh_keys        = "101,102"
    }
  }
}
```

The checks track **allocated** capacity, so the pool grows when Nomad runs out of room to place allocations, on whichever resource is tightest.

| Key | Default | Description |
|-----|---------|-------------|
| `gigahost_product_name` | `""` | Catalog product name, e.g. `KVM Performance VPS 4GB`. Required for scale-out |
| `gigahost_region` | `""` | Region name or short name, e.g. `Sandefjord`. Required for scale-out |
| `gigahost_os_distro` | `""` | OS distribution to install, e.g. `Ubuntu`. Required for scale-out |
| `gigahost_os_version` | `""` | OS version, e.g. `24.04`. Required for scale-out |
| `gigahost_hostname_prefix` | `""` | Server names/hostnames are `<prefix>-<random>`; this is how nodes are identified. Required for scale-out |
| `gigahost_ssh_keys` | `""` | Comma-separated SSH key ids to authorize on new servers |
| `gigahost_backups` | `false` | Enable daily backups (adds 25% to the price) |
| `datacenter` | `""` | Nomad client datacenter filter |
| `node_class` | `""` | Nomad client node class filter |
| `node_pool` | `""` | Nomad client node pool filter |
| `node_drain_deadline` | `15m` | Drain deadline before cancellation |
| `node_purge` | `false` | Purge the Nomad node after cancellation |
| `node_selector_strategy` | `least_busy` | Node selection strategy for scale-in |

Notes:

- At least one of `datacenter`, `node_class`, or `node_pool` is required to identify the pool. nomad-apm cluster queries only work with `node_class`, and the autoscaler forces `min = 1` (a pool cannot scale to zero through the Nomad APM).
- Product, region, and OS names are the same the [terraform-provider-gigahost](https://github.com/pigeon-as/terraform-provider-gigahost) `gigahost_server` resource uses.

## Scaling Behaviour

- Servers take several minutes to deploy; set a generous policy `cooldown`. The cooldown survives autoscaler restarts: the plugin reports the last server creation/deletion as the last scaling event.
- A scale-out is one batch deploy; `Scale` returns once the servers are ready (30-minute timeout) and the new nodes have joined the Nomad pool (`retry_attempts` × 10s).
- The target reports not-ready while a scaling action runs or any server on the account is installing — a manual deploy on the same account briefly pauses autoscaling.
- A server that deploys but never joins Nomad fails the scale-out with its `server_id` in the log and is invisible to scale-in; alert on scale-out failures and cancel such servers manually.

## Build

```bash
make build    # → build/gigahost
make test
make vet
```

## License

MPL-2.0
