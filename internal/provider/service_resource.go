// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	_ resource.Resource                = (*serviceResource)(nil)
	_ resource.ResourceWithConfigure   = (*serviceResource)(nil)
	_ resource.ResourceWithImportState = (*serviceResource)(nil)
)

// NewServiceResource constructs the proxmoxsvc_service resource.
func NewServiceResource() resource.Resource { return &serviceResource{} }

type serviceResource struct {
	runner *ansible.Runner
}

type serviceModel struct {
	ID                 types.String `tfsdk:"id"`
	Service            types.String `tfsdk:"service"`
	TargetIP           types.String `tfsdk:"target_ip"`
	CTID               types.Int64  `tfsdk:"ct_id"`
	Node               types.String `tfsdk:"node"`
	AppPlaybook        types.String `tfsdk:"app_playbook"`
	AppVars            types.Map    `tfsdk:"app_vars"`
	SSHUser            types.String `tfsdk:"ssh_user"`
	SSHPassword        types.String `tfsdk:"ssh_password"`
	RunInit            types.Bool   `tfsdk:"run_init"`
	AppVarsHash        types.String `tfsdk:"app_vars_hash"`
	ProvidesTokens     types.Map    `tfsdk:"provides_tokens"`
	ProvidesTokensHash types.String `tfsdk:"provides_tokens_hash"`
	HealthEndpoint     types.String `tfsdk:"health_endpoint"`
	LastApplied        types.String `tfsdk:"last_applied"`
}

func (r *serviceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service"
}

func (r *serviceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Install and track a service on a Proxmox guest by running its Ansible playbook. " +
			"Captures the service's `provides_tokens` for wiring into dependent services. Installs are idempotent; " +
			"re-apply is the drift remedy.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<service>@<target_ip>` identifier.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"service": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Service name — the `playbooks/applications/<service>` directory (and its `about.json`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"target_ip": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "IP of the guest to install onto.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ct_id": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Optional PVE container/VM id (for SoT stamping; informational).",
			},
			"node": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional PVE node (informational).",
			},
			"app_playbook": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Override the install playbook path (relative to the Ansible repo root). Defaults to the service's `about.json` `playbook`.",
			},
			"app_vars": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Variables passed to the playbook via a 0600 `-e @file`. Wire upstream `provides_tokens` here. A change re-runs the (idempotent) install.",
			},
			"ssh_user": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "SSH user override (else the provider default).",
			},
			"ssh_password": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "SSH password override (injected via the 0600 vars file, never argv).",
			},
			"run_init": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Run the service's `init_playbook` to capture `provides_tokens` (default true).",
			},
			"app_vars_hash": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Fingerprint of the applied `app_vars`.",
			},
			"provides_tokens": schema.MapAttribute{
				Computed:            true,
				Sensitive:           true,
				ElementType:         types.StringType,
				MarkdownDescription: "Tokens published by the service (from `about.json` `provides_tokens`); feed into dependents' `app_vars`.",
			},
			"provides_tokens_hash": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Fingerprint of the `about.json` `provides_tokens` KEY SET. When the service's declared " +
					"token set grows (a new token is added to about.json), this changes and the resource re-runs " +
					"init to capture the new tokens — WITHOUT reinstalling the app.",
			},
			"health_endpoint": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Health endpoint from the service's `about.json` (used for drift probing).",
			},
			"last_applied": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC3339 timestamp of the last successful apply. Cleared when a health probe fails (signals needs-reapply).",
			},
		},
	}
}

func (r *serviceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// install runs the service's install playbook (and init, for token capture) and
// populates the computed fields on m. Shared by Create and Update.
func (r *serviceResource) install(ctx context.Context, m *serviceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	service := m.Service.ValueString()
	ip := m.TargetIP.ValueString()

	about, err := ansible.LoadAbout(r.runner.RepoPath(), service)
	if err != nil {
		diags.AddError("about.json not found", err.Error())
		return diags
	}
	playbook := m.AppPlaybook.ValueString()
	if playbook == "" {
		playbook = about.InstallPlaybook(service)
	}
	if playbook == "" {
		diags.AddError("No install playbook", fmt.Sprintf("service %q has no `playbook` in about.json and no app_playbook was set", service))
		return diags
	}

	appVars, d := toStringMap(ctx, m.AppVars)
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}

	in := ansible.RunInput{
		Playbook:    playbook,
		TargetIP:    ip,
		SSHUser:     m.SSHUser.ValueString(),
		SSHPassword: m.SSHPassword.ValueString(),
		AppVars:     appVars,
	}
	if _, err := r.runner.RunPlaybook(ctx, in); err != nil {
		diags.AddError("Service install failed", err.Error())
		return diags
	}

	tokens, d2 := r.captureTokens(ctx, about, m, in)
	diags.Append(d2...)
	if diags.HasError() {
		return diags
	}

	tv, d := stringMapValue(ctx, tokens)
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}
	m.ProvidesTokens = tv
	m.AppVarsHash = types.StringValue(appVarsHash(appVars))
	m.ProvidesTokensHash = types.StringValue(providesTokensSetHash(about))
	m.HealthEndpoint = types.StringValue(about.Verification.HealthEndpoint)
	m.LastApplied = types.StringValue(time.Now().UTC().Format(time.RFC3339))
	m.ID = types.StringValue(service + "@" + ip)
	return diags
}

// captureTokens runs the init playbook (when the service declares one and RunInit is on)
// to capture provides_tokens, or synthesizes the _ct_url sentinels otherwise. Shared by
// install() (post-install) and initOnly() (init without reinstall).
func (r *serviceResource) captureTokens(ctx context.Context, about *ansible.About, m *serviceModel, in ansible.RunInput) (map[string]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	tokens := map[string]string{}
	runInit := m.RunInit.IsNull() || m.RunInit.IsUnknown() || m.RunInit.ValueBool()
	ip := m.TargetIP.ValueString()
	port := about.DefaultPort
	switch {
	case runInit && about.InitPlaybook != "" && len(about.ProvidesTokens) > 0:
		initIn := in
		initIn.Playbook = about.InitPlaybookPath(m.Service.ValueString())
		caps, err := r.runner.CaptureTokens(ctx, initIn, about.ProvidesTokens, ip, port)
		if err != nil {
			diags.AddError("Service init / token capture failed", err.Error())
			return tokens, diags
		}
		tokens = caps
	default:
		for tok, outputVar := range about.ProvidesTokens {
			if outputVar == "_ct_url" {
				tokens[tok] = fmt.Sprintf("http://%s:%d", ip, port)
			}
		}
	}
	return tokens, diags
}

// initOnly re-runs the init playbook to (re)capture provides_tokens WITHOUT re-running the
// install. Used when only the declared token set changed (about.json grew a token): the app
// is already installed and running, so reinstalling it would be wasteful and — for services
// with live external state (e.g. a printer's serial connection) — risky.
func (r *serviceResource) initOnly(ctx context.Context, m *serviceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	service := m.Service.ValueString()
	ip := m.TargetIP.ValueString()

	about, err := ansible.LoadAbout(r.runner.RepoPath(), service)
	if err != nil {
		diags.AddError("about.json not found", err.Error())
		return diags
	}
	appVars, d := toStringMap(ctx, m.AppVars)
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}
	in := ansible.RunInput{
		TargetIP:    ip,
		SSHUser:     m.SSHUser.ValueString(),
		SSHPassword: m.SSHPassword.ValueString(),
		AppVars:     appVars,
	}
	tokens, d2 := r.captureTokens(ctx, about, m, in)
	diags.Append(d2...)
	if diags.HasError() {
		return diags
	}
	tv, d3 := stringMapValue(ctx, tokens)
	diags.Append(d3...)
	if diags.HasError() {
		return diags
	}
	m.ProvidesTokens = tv
	m.AppVarsHash = types.StringValue(appVarsHash(appVars))
	m.ProvidesTokensHash = types.StringValue(providesTokensSetHash(about))
	m.HealthEndpoint = types.StringValue(about.Verification.HealthEndpoint)
	m.LastApplied = types.StringValue(time.Now().UTC().Format(time.RFC3339))
	m.ID = types.StringValue(service + "@" + ip)
	return diags
}

func (r *serviceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.install(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read does a shallow drift check: it refreshes health_endpoint from about.json
// and, if the service declares one, probes it — clearing last_applied on failure
// to surface "needs reapply" rather than removing the resource.
func (r *serviceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	about, err := ansible.LoadAbout(r.runner.RepoPath(), state.Service.ValueString())
	if err == nil {
		state.HealthEndpoint = types.StringValue(about.Verification.HealthEndpoint)
		// Refresh the declared-token-set fingerprint so a plan shows a diff when about.json
		// grew a token since the last apply (drives the init-only re-run in Update).
		state.ProvidesTokensHash = types.StringValue(providesTokensSetHash(about))
		if ep := about.Verification.HealthEndpoint; ep != "" {
			if !healthOK(state.TargetIP.ValueString(), about.DefaultPort, ep, about.Verification.HealthStatusCodes) {
				state.LastApplied = types.StringValue("")
			}
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serviceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serviceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Decide reinstall vs init-only. A reinstall is needed only when an install-affecting
	// input changed: the service, its target, the explicit playbook override, or app_vars.
	// If the ONLY change is the declared token set (about.json grew a token — reflected in
	// provides_tokens_hash), re-run init alone: the app is already installed and, for
	// services with live external state, reinstalling it is wasteful and risky.
	planAppVars, d := toStringMap(ctx, plan.AppVars)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	reinstall := plan.Service.ValueString() != state.Service.ValueString() ||
		plan.TargetIP.ValueString() != state.TargetIP.ValueString() ||
		plan.AppPlaybook.ValueString() != state.AppPlaybook.ValueString() ||
		appVarsHash(planAppVars) != state.AppVarsHash.ValueString()

	if reinstall {
		resp.Diagnostics.Append(r.install(ctx, &plan)...)
	} else {
		resp.Diagnostics.Append(r.initOnly(ctx, &plan)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete runs an uninstall playbook by convention if one exists, else drops
// state with a warning (no uninstall playbooks exist today; the guest is
// destroyed by the core proxmox provider).
func (r *serviceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	service := state.Service.ValueString()
	uninstall := fmt.Sprintf("playbooks/applications/%s/baremetal/uninstall_%s.yml", service, service)
	if !fileExists(r.runner.RepoPath(), uninstall) {
		resp.Diagnostics.AddWarning(
			"No uninstall playbook",
			fmt.Sprintf("%s not found — dropping %s@%s from state without uninstalling. The service remains on the guest "+
				"(usually fine: the guest is destroyed by the proxmox provider). Add an uninstall_%s.yml to enable clean teardown.",
				uninstall, service, state.TargetIP.ValueString(), service),
		)
		return
	}
	in := ansible.RunInput{
		Playbook:    uninstall,
		TargetIP:    state.TargetIP.ValueString(),
		SSHUser:     state.SSHUser.ValueString(),
		SSHPassword: state.SSHPassword.ValueString(),
	}
	if _, err := r.runner.RunPlaybook(ctx, in); err != nil {
		resp.Diagnostics.AddError("Service uninstall failed", err.Error())
	}
}

func (r *serviceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	svc, ip, ok := splitServiceID(req.ID)
	if !ok {
		resp.Diagnostics.AddError("Invalid import id", "expected `<service>@<target_ip>` (e.g. `grafana@192.168.4.40`)")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service"), svc)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("target_ip"), ip)...)
	// app_vars / provides_tokens are write-time inputs/outputs and cannot be
	// observed on import; leave them null. Read populates health_endpoint.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("app_vars"), types.MapNull(types.StringType))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("provides_tokens"), types.MapNull(types.StringType))...)
}

// --- helpers ---------------------------------------------------------------

func toStringMap(ctx context.Context, m types.Map) (map[string]string, diag.Diagnostics) {
	out := map[string]string{}
	if m.IsNull() || m.IsUnknown() {
		return out, nil
	}
	d := m.ElementsAs(ctx, &out, false)
	return out, d
}

func stringMapValue(ctx context.Context, m map[string]string) (types.Map, diag.Diagnostics) {
	return types.MapValueFrom(ctx, types.StringType, m)
}

func appVarsHash(av map[string]string) string {
	keys := make([]string, 0, len(av))
	for k := range av {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\n", k, av[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// providesTokensSetHash fingerprints the KEYS of about.json's provides_tokens (sorted).
// Only the key set matters — a growing set means new tokens to capture; the values are
// produced by the init run, not declared here.
func providesTokensSetHash(about *ansible.About) string {
	keys := make([]string, 0, len(about.ProvidesTokens))
	for k := range about.ProvidesTokens {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s\n", k)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func healthOK(ip string, port int, endpoint string, codes []int) bool {
	if endpoint == "" {
		return true
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s:%d%s", ip, port, endpoint))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if len(codes) == 0 {
		return resp.StatusCode/100 == 2
	}
	for _, c := range codes {
		if resp.StatusCode == c {
			return true
		}
	}
	return false
}

func splitServiceID(id string) (service, ip string, ok bool) {
	parts := strings.SplitN(id, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func fileExists(repo, rel string) bool {
	info, err := os.Stat(filepath.Join(repo, rel))
	return err == nil && !info.IsDir()
}
