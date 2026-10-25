# Consul configuration for the TenSunS-go docker compose stack.
#
# The ACL tokens are injected at container start by the `consul-init` service,
# which renders this file from the template in deploy/consul/init.sh. The
# placeholders below are only used when Consul is started without the init
# service (for example when running the image by hand).

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
    initial_management = "REPLACED_BY_CONSUL_INIT"
    agent = "REPLACED_BY_CONSUL_INIT"
  }
}
