# SPDX-License-Identifier: AGPL-3.0-or-later
# Install a single service on a guest. Import an existing install with:
#   tofu import 'proxmoxsvc_service.grafana' 'grafana@192.168.4.50'

resource "proxmoxsvc_service" "grafana" {
  service   = "grafana"
  target_ip = "192.168.4.50"
  ct_id     = 150
  app_vars = {
    grafana_admin_password = var.grafana_admin_password
  }
}

variable "grafana_admin_password" {
  type      = string
  sensitive = true
}
