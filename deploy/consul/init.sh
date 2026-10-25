#!/usr/bin/env sh
# Render the Consul configuration with the management token from the
# environment and start the Consul agent in server mode.
#
# The management token is provided via CONSUL_HTTP_TOKEN / CONSUL_TOKEN by the
# compose file so that a fresh checkout can be started with a single command
# and no manual UUID generation.
set -eu

TOKEN="${CONSUL_MANAGEMENT_TOKEN:-}"
if [ -z "$TOKEN" ]; then
    echo "consul-init: CONSUL_MANAGEMENT_TOKEN is not set" >&2
    exit 1
fi

mkdir -p /consul/data /consul/config

cat > /consul/config/consul.hcl <<EOF
log_level = "error"
data_dir = "/consul/data"
client_addr = "0.0.0.0"
ui_config {
  enabled = true
}
ports {
  grpc = -1
  https = -1
  dns = -1
  grpc_tls = -1
  serf_wan = -1
}
peering {
  enabled = false
}
connect {
  enabled = false
}
server = true
bootstrap_expect = 1
acl {
  enabled = true
  default_policy = "deny"
  enable_token_persistence = true
  tokens {
    initial_management = "$TOKEN"
    agent = "$TOKEN"
  }
}
EOF

echo "consul-init: starting consul agent (management token configured)"
exec consul agent -config-file=/consul/config/consul.hcl -data-dir=/consul/data
