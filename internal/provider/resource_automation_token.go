package provider

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

var (
	_ resource.ResourceWithImportState    = (*automationTokenResource)(nil)
	_ resource.ResourceWithValidateConfig = (*automationTokenResource)(nil)
)

type automationTokenResource struct{ baseResource }

// NewAutomationTokenResource returns the porch_automation_token resource.
func NewAutomationTokenResource() resource.Resource { return &automationTokenResource{} }

type automationTokenModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Tenant        types.String `tfsdk:"tenant"`
	Role          types.String `tfsdk:"role"`
	Permissions   types.Set    `tfsdk:"permissions"`
	ExpiresInDays types.Int64  `tfsdk:"expires_in_days"`
	NeverExpires  types.Bool   `tfsdk:"never_expires"`
	Token         types.String `tfsdk:"token"`
	Hint          types.String `tfsdk:"hint"`
	ExpiresAt     types.String `tfsdk:"expires_at"`
	IssuedBy      types.String `tfsdk:"issued_by"`
}

func (r *automationTokenResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_automation_token"
}

func (r *automationTokenResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceString := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An automation token (`porch_at_…`) for another integration, issued by the provider's own token " +
			"(ADR-0048). Without `tenant` it is an installation token with a `role`: `admin`, `auditor` (installation health and " +
			"audit log) or `inspector` (reads the configuration of every tenant, changes nothing). With `tenant` it is a tenant " +
			"token holding `permissions` in that tenant, for example `[\"app:read\", \"service_account:read\"]`.\n\n" +
			"The provider's token must be an installation `admin` token created by an installation owner (to issue installation " +
			"or tenant tokens), or a tenant token holding a write permission (to issue narrower tokens of its tenant). The new " +
			"token is no broader than the provider's token and cannot outlive it; revoking the provider's token revokes it too. " +
			"Every argument forces a new token. The secret is returned only at creation and kept in state (sensitive); an " +
			"expired or revoked token disappears from state, so the next apply issues a new one. Destroying the resource " +
			"revokes the token and every token it issued. Import with `installation/<id>` or `<tenant>/<id>` (the secret " +
			"stays unknown).",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("Token ID (`atk_…`)."),
			"name": schema.StringAttribute{MarkdownDescription: "Display name. Changing it forces a new token.", Required: true,
				PlanModifiers: replaceString, Validators: []validator.String{stringvalidator.LengthBetween(1, 200)}},
			"tenant": schema.StringAttribute{
				MarkdownDescription: "ID or slug of the tenant for a tenant token; omit for an installation token. Changing it forces a new token.",
				Optional:            true, PlanModifiers: replaceString, Validators: []validator.String{stringvalidator.LengthBetween(1, 64)},
			},
			"role": schema.StringAttribute{
				MarkdownDescription: "Installation token role: `admin`, `auditor` or `inspector`. Required without `tenant`. Changing it forces a new token.",
				Optional:            true, PlanModifiers: replaceString,
				Validators: []validator.String{stringvalidator.OneOf("admin", "auditor", "inspector")},
			},
			"permissions": schema.SetAttribute{
				MarkdownDescription: "Tenant permissions (`<resource_type>:<action>`), 1–50. Required with `tenant`. Changing them forces a new token.",
				ElementType:         types.StringType, Optional: true,
				PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace()},
				Validators: []validator.Set{setvalidator.SizeBetween(1, 50),
					setvalidator.ValueStringsAre(stringvalidator.RegexMatches(permissionRE, "must be <resource_type>:<action>"))},
			},
			"expires_in_days": schema.Int64Attribute{
				MarkdownDescription: "Lifetime in days (1–366); it may not outlive the provider's token. Required unless `never_expires` is set. Changing it forces a new token.",
				Optional:            true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				Validators: []validator.Int64{int64validator.Between(1, 366)},
			},
			"never_expires": schema.BoolAttribute{
				MarkdownDescription: "Issue an installation token that never expires (ADR-0046). Needs a provider token that never expires itself. Default `false`. Changing it forces a new token.",
				Optional:            true, Computed: true, Default: booldefault.StaticBool(false),
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "The token secret, sent as `Authorization: Bearer <token>`. Only known after create; unknown after import.",
				Computed:            true, Sensitive: true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"hint":       computedString("Last characters of the token, as shown in the admin UI."),
			"expires_at": computedString("When the token expires (RFC 3339); empty when it never expires."),
			"issued_by":  computedString("ID of the token that issued this one (the provider's token at creation)."),
		},
	}
}

// ValidateConfig checks the installation/tenant shapes, which depend on each other.
func (r *automationTokenResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m automationTokenModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.Tenant.IsUnknown() {
		return
	}
	tenant := !m.Tenant.IsNull()
	switch {
	case tenant && !m.Role.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("role"), "Invalid token", "role applies only to installation tokens (without tenant).")
	case tenant && m.Permissions.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("permissions"), "Invalid token", "a tenant token needs permissions.")
	case !tenant && m.Role.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("role"), "Invalid token", "an installation token (without tenant) needs a role.")
	case !tenant && !m.Permissions.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("permissions"), "Invalid token", "permissions apply only to tenant tokens.")
	}
	never := !m.NeverExpires.IsNull() && !m.NeverExpires.IsUnknown() && m.NeverExpires.ValueBool()
	switch {
	case never && tenant:
		resp.Diagnostics.AddAttributeError(path.Root("never_expires"), "Invalid token", "only installation tokens can be issued without expiry.")
	case never && !m.ExpiresInDays.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("expires_in_days"), "Invalid token", "omit expires_in_days for a token that never expires.")
	case !never && !m.NeverExpires.IsUnknown() && m.ExpiresInDays.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("expires_in_days"), "Invalid token", "set expires_in_days (1-366) or never_expires = true.")
	}
}

func (m *automationTokenModel) fill(t *client.AutomationToken) {
	m.ID = types.StringValue(t.ID)
	m.Name = types.StringValue(t.Name)
	m.Hint = types.StringValue(t.Hint)
	m.NeverExpires = types.BoolValue(t.NeverExpires)
	m.ExpiresAt = types.StringValue("")
	if t.ExpiresAt != nil {
		m.ExpiresAt = types.StringValue(t.ExpiresAt.UTC().Format(time.RFC3339))
	}
	m.IssuedBy = types.StringValue(t.IssuedBy)
	if t.Kind == "installation" {
		m.Role = types.StringValue(t.Role)
	} else {
		m.Permissions = setOf(t.Permissions)
	}
	if m.Token.IsUnknown() {
		m.Token = types.StringNull()
	}
}

// gone reports whether a token no longer works (revoked or expired): Terraform then issues a new one.
func gone(t *client.AutomationToken, now time.Time) bool {
	return t.RevokedAt != nil || (t.ExpiresAt != nil && !now.Before(*t.ExpiresAt))
}

func (r *automationTokenResource) issue(ctx context.Context, plan *automationTokenModel, perms []string) (*client.AutomationToken, client.Created, error) {
	days := plan.ExpiresInDays.ValueInt64()
	if plan.Tenant.IsNull() {
		return r.client.CreateInstallationToken(ctx, client.InstallationTokenCreate{Name: plan.Name.ValueString(), Role: plan.Role.ValueString(),
			ExpiresInDays: days, NeverExpires: plan.NeverExpires.ValueBool()})
	}
	return r.client.CreateTenantToken(ctx, plan.Tenant.ValueString(), client.TenantTokenCreate{Name: plan.Name.ValueString(), Permissions: perms,
		ExpiresInDays: days})
}

func (r *automationTokenResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan automationTokenModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var perms []string
	if !plan.Tenant.IsNull() {
		perms = stringsOf(ctx, plan.Permissions, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	tenant := plan.Tenant.ValueString()
	t, created, err := r.issue(ctx, &plan, perms)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot create automation token", err)
		return
	}
	if created.SecretWithheld {
		// An earlier attempt created the token but its response (with the only copy of the secret) was lost: revoke it
		// and issue a fresh one.
		if err := r.client.RevokeAutomationToken(ctx, tenant, t.ID); err != nil && !client.IsNotFound(err) {
			apiError(&resp.Diagnostics, "Automation token created but its secret was lost, and it could not be revoked", err)
			return
		}
		if t, _, err = r.issue(ctx, &plan, perms); err != nil {
			apiError(&resp.Diagnostics, "Cannot create automation token", err)
			return
		}
		resp.Diagnostics.AddWarning("Automation token reissued", "A token created by an earlier attempt whose response was lost was "+
			"revoked and a new one issued.")
	}
	plan.Token = types.StringValue(t.Token)
	plan.fill(t)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *automationTokenResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state automationTokenModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, err := r.client.GetAutomationToken(ctx, state.Tenant.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) || (err == nil && gone(t, time.Now())) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read automation token", err)
		return
	}
	state.fill(t)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is never called with a change (every argument forces a new token), only to refresh computed values.
func (r *automationTokenResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan automationTokenModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *automationTokenResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state automationTokenModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.RevokeAutomationToken(ctx, state.Tenant.ValueString(), state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) && !client.HasCode(err, "conflict") {
		apiError(&resp.Diagnostics, "Cannot revoke automation token", err)
	}
}

// ImportState accepts installation/<id> or <tenant>/<id>.
func (r *automationTokenResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitID(req.ID, 2, "installation/<id> or <tenant>/<id>")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	if parts[0] != "installation" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("tenant"), parts[0])...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
