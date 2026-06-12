# Nomad dev agent for the e2e tests (make dev). nomad-apm cluster queries
# need a node_class.

log_level = "INFO"

client {
  node_class = "gigahost-e2e-node"
}
