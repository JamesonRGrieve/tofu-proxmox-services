// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"
	"fmt"

	"github.com/JamesonRGrieve/tofu-proxmox-services/internal/ansible"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*serviceMetadataDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*serviceMetadataDataSource)(nil)
)

// NewServiceMetadataDataSource reads a service's about.json so token
// dependencies and ports can be referenced in HCL without installing.
func NewServiceMetadataDataSource() datasource.DataSource { return &serviceMetadataDataSource{} }

type serviceMetadataDataSource struct {
	runner *ansible.Runner
}

type metadataModel struct {
	Service         types.String `tfsdk:"service"`
	DisplayName     types.String `tfsdk:"display_name"`
	DefaultPort     types.Int64  `tfsdk:"default_port"`
	InstallPlaybook types.String `tfsdk:"install_playbook"`
	InitPlaybook    types.String `tfsdk:"init_playbook"`
	ProvidesTokens  types.Map    `tfsdk:"provides_tokens"`
	HealthEndpoint  types.String `tfsdk:"health_endpoint"`
}

func (d *serviceMetadataDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_metadata"
}

func (d *serviceMetadataDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a service's `playbooks/applications/<service>/about.json`, exposing the fields needed " +
			"to wire token dependencies and ports between `proxmoxsvc_service` resources.",
		Attributes: map[string]schema.Attribute{
			"service": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Service name (the `playbooks/applications/<service>` directory).",
			},
			"display_name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Human-readable name.",
			},
			"default_port": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Default service port.",
			},
			"install_playbook": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Install playbook path (relative to the Ansible repo root).",
			},
			"init_playbook": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Init playbook path (relative to the repo root), or empty.",
			},
			"provides_tokens": schema.MapAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Tokens this service publishes (token name → output var or the `_ct_url` sentinel).",
			},
			"health_endpoint": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Health endpoint from `verification.health_endpoint`.",
			},
		},
	}
}

func (d *serviceMetadataDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	runner, ok := req.ProviderData.(*ansible.Runner)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *ansible.Runner, got %T", req.ProviderData))
		return
	}
	d.runner = runner
}

func (d *serviceMetadataDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m metadataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	service := m.Service.ValueString()
	about, err := ansible.LoadAbout(d.runner.RepoPath(), service)
	if err != nil {
		resp.Diagnostics.AddError("about.json not found", err.Error())
		return
	}
	tokens, d2 := stringMapValue(ctx, about.ProvidesTokens)
	resp.Diagnostics.Append(d2...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.DisplayName = types.StringValue(about.DisplayName)
	m.DefaultPort = types.Int64Value(int64(about.DefaultPort))
	m.InstallPlaybook = types.StringValue(about.InstallPlaybook(service))
	m.InitPlaybook = types.StringValue(about.InitPlaybookPath(service))
	m.ProvidesTokens = tokens
	m.HealthEndpoint = types.StringValue(about.Verification.HealthEndpoint)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
