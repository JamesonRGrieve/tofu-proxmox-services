# terraform-provider-proxmox-services

A native OpenTofu/Terraform provider that **orchestrates service installs on Proxmox guests via
the neighbouring Ansible repo** and tracks installed-service state. Companion to
`jamesonrgrieve/proxmox` (which creates the guests); this provider installs services onto them.

It is the Terraform-graph replacement for the per-guest fan-out in the Ansible repo's
`playbooks/proxmox/deploy_parallel.yml` (Play 2): each `proxmoxsvc_service` runs one guest's
install playbook, and dependency ordering (a service that needs another's tokens) becomes a real
resource DAG instead of `strategy: free`.

Resources/data sources (provider local name `proxmoxsvc`):

- **`proxmoxsvc_service`** — install/track a service on a guest. Shells out to `ansible-playbook`
  with the service's `app_playbook` + `app_vars`, captures `provides_tokens`, and records
  installed-service state. Drift is a health-endpoint probe; re-apply (installs are idempotent) is
  the remedy.
- **`proxmoxsvc_service_metadata`** (data source) — reads a service's `about.json`
  (`provides_tokens`, `integrations[].requires_tokens`, `verification.health_endpoint`) so token
  dependencies can be wired in HCL.

House standards: `/home/jameson/source/ai-prompts/go.md`.

## How it talks to Ansible

The `internal/ansible` engine reproduces the harness's proven invocation
(`harness/orchestrator/playbook_section.py`):

```
ansible-playbook <playbook> -i "<ip>," -u root -e target_host=<ip> \
  -e @<app_vars.json (0600)> --ssh-extra-args "<args>" [--vault-password-file <file>]
```

run with `cwd = ansible_repo_path`. `app_vars` (and the SSH password) go in the 0600 vars file,
never on argv. `provides_tokens` are captured by passing `-e <output_var>=<tmpfile>` to the init
playbook and reading the files back after success; the `_ct_url` sentinel synthesizes
`http://<ip>:<port>`.

## Token wiring (dependency DAG)

```hcl
resource "proxmoxsvc_service" "db" {
  service   = "postgresql"
  target_ip = "192.168.4.41"
}

resource "proxmoxsvc_service" "semaphore" {
  service   = "semaphore"
  target_ip = "192.168.4.40"
  app_vars = {
    semaphore_db_address = proxmoxsvc_service.db.provides_tokens["db_host"]
  }
}
```

Terraform sequences `db` before `semaphore` automatically via the interpolation.

## Provider configuration

```hcl
provider "proxmoxsvc" {
  ansible_repo_path    = "/home/jameson/source/ansible"
  vault_password_file  = "/run/secrets/vault_pass"
  default_ssh_user     = "root"
  default_ssh_password = var.ct_root_password  # TF_VAR_* / OpenBao
}
```

## Caveats

- This is an **orchestration bridge**, not a hermetic API client: the apply host needs
  `ansible-playbook` + collections + the vault file. Runs are bounded by `CommandContext`.
- **Drift detection is shallow** — a health probe, not full convergence. Re-apply the idempotent
  install to remediate.
- **No uninstall playbooks exist** today, so `Delete` drops state and warns. Acceptable since the
  core provider destroys the guest; a future `uninstall_<svc>.yml` convention closes the gap.
- Secrets (`app_vars`, `provides_tokens`) are sensitive and live in state — keep state encrypted.

## License

AGPL-3.0-or-later. Engine uses the Go standard library only (`os/exec`, `encoding/json`, `net/http`).
