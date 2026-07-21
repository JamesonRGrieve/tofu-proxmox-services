// SPDX-License-Identifier: AGPL-3.0-or-later

// Package provider implements the proxmox-services OpenTofu/Terraform provider:
// it orchestrates service installs on Proxmox guests through the neighbouring
// Ansible repo (proxmoxsvc_service) and exposes each service's about.json
// contract (proxmoxsvc_service_metadata) so token dependencies between services
// can be wired declaratively. The provider local name is `proxmoxsvc` (distinct
// from the core `proxmox` provider so both can be used in one configuration).
package provider

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/JamesonRGrieve/tofu-proxmox-services/internal/ansible"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = (*servicesProvider)(nil)

// New returns the provider factory for a given version.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &servicesProvider{version: version} }
}

type servicesProvider struct {
	version string
}

type providerModel struct {
	AnsibleRepoPath    types.String `tfsdk:"ansible_repo_path"`
	AnsibleBin         types.String `tfsdk:"ansible_bin"`
	VaultPasswordFile  types.String `tfsdk:"vault_password_file"`
	DefaultSSHUser     types.String `tfsdk:"default_ssh_user"`
	DefaultSSHPassword types.String `tfsdk:"default_ssh_password"`
	SSHExtraArgs       types.String `tfsdk:"ssh_extra_args"`
	DefaultTimeout     types.Int64  `tfsdk:"default_timeout_seconds"`
}

func (p *servicesProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "proxmoxsvc"
	resp.Version = p.version
}

func (p *servicesProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Orchestrates service installs on Proxmox guests via the neighbouring Ansible repo. " +
			"Companion to the `proxmox` provider (which creates the guests). Requires `ansible-playbook` on PATH.",
		Attributes: map[string]schema.Attribute{
			"ansible_repo_path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Absolute path to the Ansible repo (cwd for every `ansible-playbook` invocation). Must contain `playbooks/applications`.",
			},
			"ansible_bin": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "ansible-playbook binary (default `ansible-playbook`, resolved on PATH).",
			},
			"vault_password_file": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path passed to `--vault-password-file` for vault-encrypted vars.",
			},
			"default_ssh_user": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Default SSH user for installs (default `root`).",
			},
			"default_ssh_password": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Default SSH password (injected via the 0600 vars file as `ansible_password`, never on argv). From TF_VAR_* / OpenBao.",
			},
			"ssh_extra_args": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Value passed to `--ssh-extra-args` (default `-o StrictHostKeyChecking=no -o PubkeyAuthentication=no`).",
			},
			"default_timeout_seconds": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Default per-playbook timeout in seconds (default 1800).",
			},
		},
	}
}

func (p *servicesProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.AnsibleRepoPath.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("ansible_repo_path"), "Unknown ansible_repo_path",
			"The provider cannot be configured with an unknown ansible_repo_path.")
		return
	}
	repo := cfg.AnsibleRepoPath.ValueString()
	if info, err := os.Stat(filepath.Join(repo, "playbooks", "applications")); err != nil || !info.IsDir() {
		resp.Diagnostics.AddAttributeError(path.Root("ansible_repo_path"), "Invalid ansible_repo_path",
			"expected an Ansible repo containing playbooks/applications at "+repo)
		return
	}

	var timeout time.Duration
	if !cfg.DefaultTimeout.IsNull() && cfg.DefaultTimeout.ValueInt64() > 0 {
		timeout = time.Duration(cfg.DefaultTimeout.ValueInt64()) * time.Second
	}
	runner := ansible.NewRunner(ansible.Config{
		RepoPath:           repo,
		VaultPasswordFile:  cfg.VaultPasswordFile.ValueString(),
		DefaultSSHUser:     cfg.DefaultSSHUser.ValueString(),
		DefaultSSHPassword: cfg.DefaultSSHPassword.ValueString(),
		SSHExtraArgs:       cfg.SSHExtraArgs.ValueString(),
		AnsibleBin:         cfg.AnsibleBin.ValueString(),
		DefaultTimeout:     timeout,
	})
	resp.ResourceData = runner
	resp.DataSourceData = runner
}

func (p *servicesProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewServiceResource, NewIntegrationResource}
}

func (p *servicesProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{NewServiceMetadataDataSource}
}
