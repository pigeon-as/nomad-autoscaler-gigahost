# Second Nomad client for the scale-in e2e; the test starts it itself in its
# own UTS namespace.

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
