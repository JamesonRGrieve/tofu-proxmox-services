# SPDX-License-Identifier: AGPL-3.0-or-later
# Read a service's about.json contract (no install).

data "proxmoxsvc_service_metadata" "grafana" {
  service = "grafana"
}

output "grafana_port" {
  value = data.proxmoxsvc_service_metadata.grafana.default_port
}

output "grafana_provides" {
  value = data.proxmoxsvc_service_metadata.grafana.provides_tokens
}
