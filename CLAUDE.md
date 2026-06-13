# tofu-proxmox-services — Agent Guide

A native OpenTofu/Terraform provider that orchestrates service installs on Proxmox guests through
the neighbouring `ansible` repo. Companion to `tofu-proxmox` (guest lifecycle). Built to the same
house standards; general Go / provider standards are canonical at
`/home/jameson/source/ai-prompts/go.md`. This file holds only repo-specific facts.

## Design

- Provider local name (TypeName) is **`proxmoxsvc`** (distinct from the core `proxmox` provider so
  both can be used in one config). Resources: `proxmoxsvc_service`, `proxmoxsvc_service_metadata`.
- **`internal/ansible`** engine (zero terraform imports): `Runner` shells out to `ansible-playbook`,
  reproducing the contract in `harness/orchestrator/playbook_section.py`:
  `ansible-playbook <pb> -i "<ip>," -u <user> -e target_host=<ip> -e @<0600 vars.json> --ssh-extra-args <args> [--vault-password-file <f>]`, cwd = `ansible_repo_path`.
  Token capture: pass `-e <output_var>=<tmpfile>` for each `provides_tokens` entry, run the init
  playbook, read the files back; `_ct_url` → `http://<ip>:<port>`.
- **`proxmoxsvc_service`**: Create runs the install (and init for tokens); Read does a health-probe
  drift check; Update re-runs on `app_vars` change (installs are idempotent); Delete runs
  `uninstall_<svc>.yml` if present, else drops state + warns.
- **`proxmoxsvc_service_metadata`**: reads `playbooks/applications/<svc>/about.json`.

## about.json contract (fields consumed)

`name`, `display_name`, `default_port`, `playbook` (e.g. `baremetal/install_<svc>.yml`),
`init_playbook`, `provides_tokens` (map token→deploy_var, or `_ct_url` sentinel), `integrations[]`
(`name`, `playbook`, `requires_service`, `requires_tokens[]`), `verification.health_endpoint` +
`health_status_codes[]`. Install playbook path resolves to `applications/<svc>/<playbook>`.

## Repo-specific notes

- **Secrets never on argv**: `app_vars` and the SSH password go in the 0600 `-e @file`. Token output
  files are 0600 and unlinked after read.
- **rc=4 = UNREACHABLE**: surface as a clear error; do NOT auto-retry (the harness does, but a
  provider that hides failures from the plan is worse). Let the operator / `-parallelism` handle it.
- **Drift is shallow** (health probe, not convergence); re-apply is the remedy. Many install
  playbooks aren't `--check`-clean, so `--check` is opt-in and unreliable.
- **No uninstall playbooks exist** (verified) — Delete warns. Reserve `uninstall_<svc>.yml`.

## Relationship to the lab

Replaces `deploy_parallel.yml` Play 2's parallel fan-out with Terraform's resource graph; ordering
via `about.json` `requires_tokens` → `depends_on`/interpolation. NetBox stays the source of truth
for guest definitions. The core `proxmox` provider (or today's `bpg/proxmox` in `tofu/opentofu/hv/pve`)
creates the guest; this provider installs the service onto its IP.
