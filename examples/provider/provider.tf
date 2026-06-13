# SPDX-License-Identifier: AGPL-3.0-or-later
# Install postgresql, then semaphore wired to use the DB tokens. Terraform
# sequences db -> semaphore automatically via the interpolation.

terraform {
  required_providers {
    proxmoxsvc = { source = "jamesonrgrieve/proxmox-services" }
  }
}

variable "ct_root_password" {
  type      = string
  sensitive = true
} # from OpenBao -> TF_VAR_ct_root_password

provider "proxmoxsvc" {
  ansible_repo_path    = "/home/jameson/source/ansible"
  vault_password_file  = "/run/secrets/vault_pass"
  default_ssh_user     = "root"
  default_ssh_password = var.ct_root_password
}

# Read service contracts so token wiring is explicit.
data "proxmoxsvc_service_metadata" "postgresql" { service = "postgresql" }
data "proxmoxsvc_service_metadata" "semaphore" { service = "semaphore" }

# Provider service: PostgreSQL — its init playbook publishes db_* tokens.
resource "proxmoxsvc_service" "db" {
  service   = "postgresql"
  target_ip = "192.168.4.41"
  ct_id     = 141
  run_init  = true
}

# Consumer: Semaphore, fed the DB tokens above.
resource "proxmoxsvc_service" "semaphore" {
  service   = "semaphore"
  target_ip = "192.168.4.40"
  ct_id     = 140
  app_vars = {
    semaphore_db_type     = "postgres"
    semaphore_db_address  = proxmoxsvc_service.db.provides_tokens["db_host"]
    semaphore_db_port     = proxmoxsvc_service.db.provides_tokens["db_port"]
    semaphore_db_user     = proxmoxsvc_service.db.provides_tokens["db_admin_user"]
    semaphore_db_password = proxmoxsvc_service.db.provides_tokens["db_admin_password"]
  }
}

output "semaphore_url" {
  value = try(proxmoxsvc_service.semaphore.provides_tokens["semaphore_url"], null)
}
