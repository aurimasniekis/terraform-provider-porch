package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

var _ resource.ResourceWithImportState = (*domainRuleResource)(nil)

type domainRuleResource struct{ baseResource }

// NewDomainRuleResource returns the porch_domain_rule resource.
func NewDomainRuleResource() resource.Resource { return &domainRuleResource{} }

type domainRuleModel struct {
	ID           types.String `tfsdk:"id"`
	Tenant       types.String `tfsdk:"tenant"`
	Domain       types.String `tfsdk:"domain"`
	ProviderID   types.String `tfsdk:"provider_id"`
	EvidenceKind types.String `tfsdk:"evidence_kind"`
	Mode         types.String `tfsdk:"mode"`
}

func (r *domainRuleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain_rule"
}

func (r *domainRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Enrolls an email domain into a tenant: users signing in through the given provider with an address in the " +
			"domain join the tenant (`jit`) or may accept invitations (`invite_only`). A domain belongs to at most one tenant. Rules " +
			"cannot be edited in place; every change replaces the rule (deleting a rule removes the memberships derived from it). " +
			"Requires an installation `admin` token.",
		Attributes: map[string]schema.Attribute{
			"id":          idAttribute("Rule ID."),
			"tenant":      tenantAttribute(),
			"domain":      requiredImmutableString("Domain name (lowercase).", lowercaseValidator(), stringvalidator.LengthBetween(3, 253)),
			"provider_id": requiredImmutableString("ID of the `porch_identity_provider` the evidence must come from."),
			"evidence_kind": requiredImmutableString("`workspace_hd` (validated Google Workspace `hd` claim, `google` providers only) or "+
				"`verified_email` (weaker).", stringvalidator.OneOf("workspace_hd", "verified_email")),
			"mode": requiredImmutableString("`jit` (join on first sign-in) or `invite_only`.", stringvalidator.OneOf("invite_only", "jit")),
		},
	}
}

func (m *domainRuleModel) fill(r *client.DomainRule) {
	m.ID = types.StringValue(r.ID)
	m.Domain = types.StringValue(r.Domain)
	m.ProviderID = types.StringValue(r.ProviderID)
	m.EvidenceKind = types.StringValue(r.EvidenceKind)
	m.Mode = types.StringValue(r.Mode)
}

func (r *domainRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan domainRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	dr, err := r.client.CreateDomainRule(ctx, plan.Tenant.ValueString(), client.DomainRuleCreate{Domain: plan.Domain.ValueString(),
		ProviderID: plan.ProviderID.ValueString(), EvidenceKind: plan.EvidenceKind.ValueString(), Mode: plan.Mode.ValueString()})
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot create domain rule", err)
		return
	}
	plan.fill(dr)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, dr.ETag)...)
}

func (r *domainRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state domainRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	dr, err := r.client.GetDomainRule(ctx, state.Tenant.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read domain rule", err)
		return
	}
	state.fill(dr)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, dr.ETag)...)
}

func (r *domainRuleResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Domain rules are immutable", "Every attribute forces replacement; this is a provider bug.")
}

func (r *domainRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state domainRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteDomainRule(ctx, state.Tenant.ValueString(), state.ID.ValueString(), loadETag(ctx, req.Private))
	if err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot delete domain rule", err)
	}
}

func (r *domainRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importTenantScoped(ctx, req, resp)
}

// importTenantScoped handles "<tenant>/<id>" import IDs.
func importTenantScoped(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitID(req.ID, 2, "<tenant>/<id>")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("tenant"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
