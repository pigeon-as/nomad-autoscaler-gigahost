# Second Nomad client for the scale-in e2e. The autoscaler forces min=1 on
# nomad-apm cluster policies (scaling to 0 is unsupported), so scale-in is
# exercised from 2 nodes down to 1. Run via: make dev2 (next to make dev).

log_level  = "WARN"
datacenter = "dc1"
data_dir   = "/tmp/nomad-client2"

client {
  enabled    = true
  node_class = "gigahost-e2e-node"
  servers    = ["127.0.0.1:4647"]
}

ports {
  http = 4656
  rpc  = 4657
  serf = 4658
}
