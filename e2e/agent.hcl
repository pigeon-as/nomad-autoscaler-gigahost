# Nomad dev agent config for e2e testing of the autoscaler plugin.
# Run via: make dev
#
# The node_class makes the dev node targetable by the scale-in policy: the
# autoscaler canonicalizes nomad-apm cluster queries with the target's
# node_class only, and empty-class pool matching is unreliable.

log_level = "INFO"

client {
  node_class = "gigahost-e2e-node"
}
