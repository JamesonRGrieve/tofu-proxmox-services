// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/JamesonRGrieve/tofu-proxmox-services/internal/ansible"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*integrationResource)(nil)
	_ resource.ResourceWithConfigure   = (*integrationResource)(nil)
	_ resource.ResourceWithImportState = (*integrationResource)(nil)
)

// NewIntegrationResource constructs the proxmoxsvc_integration resource.
//
// This is the "separate apply step" that `proxmoxsvc_service` deliberately does not do:
// a service install cannot thread cross-service tokens, because a resource may not
// reference its own instances ("Self-referential block"). Integrations are therefore a
// distinct resource whose `tokens` argument is wired from OTHER services'
// `provides_tokens` outputs — which makes each provider→consumer dependency a real edge
// in the graph instead of an ordering convention.
func NewIntegrationResource() resource.Resource { return &integrationResource{} }

type integrationResource struct {
	runner *ansible.Runner
}

type integrationModel struct {
	ID              types.String `tfsdk:"id"`
	ConsumerService types.String `tfsdk:"consumer_service"`
	Name            types.String `tfsdk:"name"`
	Playbook        types.String `tfsdk:"playbook"`
	TargetIP        types.String `tfsdk:"target_ip"`
	CTID            types.Int64  `tfsdk:"ct_id"`
	Node            types.String `tfsdk:"node"`
	SSHUser         types.String `tfsdk:"ssh_user"`
	SSHPassword     types.String `tfsdk:"ssh_password"`
	Tokens          types.Map    `tfsdk:"tokens"`
	AppVars         types.Map    `tfsdk:"app_vars"`
	TimeoutSeconds  types.Int64  `tfsdk:"timeout_seconds"`
	VarsHash        types.String `tfsdk:"vars_hash"`
	LastApplied     types.String `tfsdk:"last_applied"`
}

func (r *integrationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_integration"
}

func (r *integrationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Runs a consumer service's `integrate_<provider>.yml` playbook, passing the provider " +
			"service's captured tokens as extra vars. Wiring `tokens` from another `proxmoxsvc_service`'s " +
			"`provides_tokens` output makes the provider→consumer dependency a real graph edge.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<consumer_service>/<name>@<target_ip>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"consumer_service": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Ansible application directory of the CONSUMER (the service being configured).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Integration name — the provider service, matching `integrations[].name` in the consumer's about.json.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"playbook": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Playbook path relative to the ansible repo root. Defaults to the consumer about.json `integrations[]` entry for `name`.",
			},
			"target_ip": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "IP of the CONSUMER guest (the integration runs against it).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ct_id": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Consumer CT/VM id (informational).",
			},
			"node": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Proxmox node hosting the consumer (informational).",
			},
			"ssh_user": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "SSH user; falls back to the provider default.",
			},
			"ssh_password": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "SSH password; injected via a 0600 vars file, never argv.",
			},
			"tokens": schema.MapAttribute{
				Optional:            true,
				Sensitive:           true,
				ElementType:         types.StringType,
				MarkdownDescription: "Provider tokens passed to the playbook as extra vars (e.g. `octoprint_url`, `octoprint_api_key`). Wire from the provider service's `provides_tokens`.",
			},
			"app_vars": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Additional non-secret vars for the integration playbook.",
			},
			"timeout_seconds": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Overrides the provider default timeout for this integration.",
			},
			"vars_hash": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Hash of tokens+app_vars; changes re-run the integration.",
			},
			"last_applied": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC3339 timestamp of the last successful run.",
			},
		},
	}
}

func (r *integrationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	runner, ok := req.ProviderData.(*ansible.Runner)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *ansible.Runner, got %T", req.ProviderData))
		return
	}
	r.runner = runner
}

// integrate resolves the playbook, merges tokens into the extra vars and runs it.
// Shared by Create and Update; integration playbooks are required to be idempotent
// (repo standard §8.7), so re-running on a token change is the intended behaviour.
func (r *integrationResource) integrate(ctx context.Context, m *integrationModel) diag.Diagnostics {
	var diags diag.Diagnostics
	consumer := m.ConsumerService.ValueString()
	name := m.Name.ValueString()
	ip := m.TargetIP.ValueString()

	playbook := m.Playbook.ValueString()
	if playbook == "" {
		about, err := ansible.LoadAbout(r.runner.RepoPath(), consumer)
		if err != nil {
			diags.AddError("about.json not found", err.Error())
			return diags
		}
		playbook = about.IntegrationPlaybook(name)
		if playbook == "" {
			diags.AddError("No integration playbook",
				fmt.Sprintf("service %q declares no integrations[] entry named %q, and no playbook was set", consumer, name))
			return diags
		}
	}

	tokens, d := toStringMap(ctx, m.Tokens)
	diags.Append(d...)
	appVars, d2 := toStringMap(ctx, m.AppVars)
	diags.Append(d2...)
	if diags.HasError() {
		return diags
	}

	// Tokens ride the same 0600 vars file as app_vars rather than argv: several are
	// credentials (API keys, admin passwords) and must not reach the process list.
	merged := make(map[string]string, len(appVars)+len(tokens))
	for k, v := range appVars {
		merged[k] = v
	}
	for k, v := range tokens {
		merged[k] = v
	}

	in := ansible.RunInput{
		Playbook:    playbook,
		TargetIP:    ip,
		SSHUser:     m.SSHUser.ValueString(),
		SSHPassword: m.SSHPassword.ValueString(),
		AppVars:     merged,
	}
	if !m.TimeoutSeconds.IsNull() && m.TimeoutSeconds.ValueInt64() > 0 {
		in.Timeout = time.Duration(m.TimeoutSeconds.ValueInt64()) * time.Second
	}
	if _, err := r.runner.RunPlaybook(ctx, in); err != nil {
		diags.AddError("Service integration failed", err.Error())
		return diags
	}

	m.ID = types.StringValue(fmt.Sprintf("%s/%s@%s", consumer, name, ip))
	m.Playbook = types.StringValue(playbook)
	m.VarsHash = types.StringValue(appVarsHash(merged))
	m.LastApplied = types.StringValue(time.Now().UTC().Format(time.RFC3339))
	return diags
}

func (r *integrationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan integrationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.integrate(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read is a no-op: an integration's real state lives inside the consumer application
// (a registered printer, a configured auth backend), which has no uniform read surface.
// Drift is handled by re-running on a vars_hash change, exactly as the service resource
// re-installs on app_vars_hash.
func (r *integrationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state integrationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *integrationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan integrationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.integrate(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete drops state. There is no un-integrate convention in the ansible repo; removing
// the wiring from a live consumer is a manual, app-specific operation.
func (r *integrationResource) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddWarning(
		"Integration state dropped",
		"proxmoxsvc_integration has no un-integrate playbook; the consumer keeps its configuration. Remove it in the application if that is not wanted.",
	)
}

func (r *integrationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
