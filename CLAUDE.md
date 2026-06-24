# tofu-proxmox-services — Agent Guide

> **⛔ NO DIRECT APPLIES TO ANY DEVICE — EVER.**
>
> Direct changes to **any** device — router, firewall, switch, access point, hypervisor, mail gateway, or any other appliance — are **NEVER** permitted, by anyone, for any reason. This bans hand-run `tofu apply`, hand-run `ansible-playbook`, SSH/serial/CLI config writes, REST/API mutations, and web-GUI/console edits.
>
> **Every change MUST flow through the sanctioned pipeline:** declare intent in **prod-netbox** (the single source of truth), then realize it **only** through **prod-semaphore** (the sanctioned runner). A change that did not go **prod-netbox → prod-semaphore** must never reach a device.
>
> **Sole exception:** a specific direct action is permitted *only* when the operator authorizes that exact action in advance by answering an explicit, **alarm-flavored `AskUserQuestion`** — one that names the device, the precise action, and the risk — **in the affirmative**. No standing grants, no inferred permission, no carrying one approval to another action or device. Absent that in-the-moment "yes," the answer is no.
>
> **Never offload the work onto the operator.** When you are blocked, ask for the break-glass authorization that lets *you* do the job — never ask the operator to run a command, SSH in, or make the change on your behalf. The operator grants permission; they do not perform your labour.

A native OpenTofu/Terraform provider that orchestrates service installs on Proxmox guests through
the neighbouring `ansible` repo. Companion to `tofu-proxmox` (guest lifecycle). Built to the same
house standards; general Go / provider standards are canonical at
`/home/jameson/source/ai-prompts/go.md`. This file holds only repo-specific facts.

## Planned: rename → `tofu-services` + NetBox/Semaphore re-architecture (2026-06-17)

Per the `netbox-services` design (`../netbox-services/DESIGN.md` §0), this repo is to be
**renamed `tofu-proxmox-services → tofu-services`** before any adoption (zero state to
migrate): the provider is host-agnostic (it shells ansible to an IP), so the `proxmox`
prefix is misleading; guest-lifecycle is the separate `tofu-proxmox`. Concrete changes:

- TypeName `proxmoxsvc` → `services`; resources `proxmoxsvc_service` →
  **`services_instance`** + new **`services_integration`** (surface mirrors the NetBox
  model: deployed instance + integration edge).
- Read intent from **NetBox** (`ServiceInstance` + `Integration`), not hand-wired
  `app_vars`; Tofu's graph orders providers→consumers via token refs.
- Execution moves **through Semaphore**: `internal/ansible.Runner` (local subprocess) →
  a **Semaphore client** that triggers the existing ansible templates; the services-apply
  runs in a **separate Semaphore queue** to avoid task-pool starvation.

The sections below describe the **current** (pre-rename) shape and stay accurate until
the rename lands.

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
