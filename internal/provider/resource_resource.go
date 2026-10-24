package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

var _ resource.ResourceWithImportState = (*resourceResource)(nil)

type resourceResource struct{ baseResource }

// NewResourceResource returns the porch_resource resource.
func NewResourceResource() resource.Resource { return &resourceResource{} }

type resourceModel struct {
	ID         types.String `tfsdk:"id"`
	Tenant     types.String `tfsdk:"tenant"`
	Type       types.String `tfsdk:"type"`
	Slug       types.String `tfsdk:"slug"`
	Name       types.String `tfsdk:"name"`
	ParentID   types.String `tfsdk:"parent_id"`
	Attributes types.Map    `tfsdk:"attributes"`
}

func (r *resourceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource"
}

func (r *resourceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An authorization resource of a tenant (e.g. a media library or a folder) that role assignments can be scoped " +
			"to; resources form a hierarchy through `parent_id`. Its attributes are visible to policies as `resource.attributes`. " +
			"Needs `resource:manage`. Import with `<tenant>/<resource_id>`.",
		Attributes: map[string]schema.Attribute{
			"id":     idAttribute("Resource ID (`res_…`)."),
			"tenant": tenantAttribute(),
			"type": requiredImmutableString("Lowercase type identifier (`app` and `tenant` are reserved).",
				stringvalidator.RegexMatches(typeRE, "must be a lowercase identifier"), stringvalidator.NoneOf("app", "tenant")),
			"slug": requiredImmutableString("Identifier, unique per type in the tenant.", slugValidators()...),
			"name": schema.StringAttribute{MarkdownDescription: "Display name.", Required: true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 200)}},
			"parent_id": optionalImmutableString("ID of the parent resource.", idValidator("res")),
			"attributes": schema.MapAttribute{
				MarkdownDescription: "String attributes (at most 50; values up to 512 bytes).",
				ElementType:         types.StringType, Optional: true, Computed: true, Default: mapdefault.StaticValue(mapOf(nil)),
				Validators: []validator.Map{mapvalidator.SizeAtMost(50), mapvalidator.ValueStringsAre(stringvalidator.LengthAtMost(512))},
			},
		},
	}
}

func (m *resourceModel) fill(x *client.Resource) {
	m.ID = types.StringValue(x.ID)
	m.Type = types.StringValue(x.Type)
	m.Slug = types.StringValue(x.Slug)
	m.Name = types.StringValue(x.Name)
	m.ParentID = optString(x.ParentID)
	m.Attributes = mapOf(x.Attributes)
}

func (r *resourceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := client.ResourceCreate{Type: plan.Type.ValueString(), Slug: plan.Slug.ValueString(), Name: plan.Name.ValueString(),
		ParentID: plan.ParentID.ValueString(), Attributes: stringMapOf(ctx, plan.Attributes, &resp.Diagnostics)}
	if resp.Diagnostics.HasError() {
		return
	}
	x, err := r.client.CreateResource(ctx, plan.Tenant.ValueString(), in)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot create resource", err)
		return
	}
	plan.fill(x)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, x.ETag)...)
}

func (r *resourceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state resourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	x, err := r.client.GetResource(ctx, state.Tenant.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read resource", err)
		return
	}
	state.fill(x)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, x.ETag)...)
}

func (r *resourceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := client.ResourceUpdate{Name: plan.Name.ValueString(), Attributes: stringMapOf(ctx, plan.Attributes, &resp.Diagnostics)}
	if resp.Diagnostics.HasError() {
		return
	}
	x, err := r.client.UpdateResource(ctx, state.Tenant.ValueString(), state.ID.ValueString(), loadETag(ctx, req.Private), in)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot update resource", err)
		return
	}
	plan.fill(x)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, x.ETag)...)
}

func (r *resourceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state resourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteResource(ctx, state.Tenant.ValueString(), state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot delete resource", err)
	}
}

func (r *resourceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importTenantScoped(ctx, req, resp)
}

// porch_policy

var (
	_ resource.ResourceWithImportState    = (*policyResource)(nil)
	_ resource.ResourceWithValidateConfig = (*policyResource)(nil)
)

type policyResource struct{ baseResource }

// NewPolicyResource returns the porch_policy resource.
func NewPolicyResource() resource.Resource { return &policyResource{} }

type policyModel struct {
	ID         types.String `tfsdk:"id"`
	Tenant     types.String `tfsdk:"tenant"`
	Slug       types.String `tfsdk:"slug"`
	Name       types.String `tfsdk:"name"`
	Effect     types.String `tfsdk:"effect"`
	Actions    types.Set    `tfsdk:"actions"`
	Expression types.String `tfsdk:"expression"`
}

func (r *policyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_policy"
}

func (r *policyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An ABAC policy of a tenant written in expr-lang over `principal`, `resource`, `action`, `context` and `now`. " +
			"A `condition` policy is referenced by role assignments and must be true for them to apply (e.g. " +
			"`context.hour >= 7 && context.hour < 21`); a `deny` policy overrides grants for its `actions`. Needs `policy:manage`; " +
			"updates replace the policy (PUT). Import with `<tenant>/<policy_id>`.",
		Attributes: map[string]schema.Attribute{
			"id":     idAttribute("Policy ID (`pol_…`)."),
			"tenant": tenantAttribute(),
			"slug":   requiredImmutableString("Identifier, unique in the tenant.", slugValidators()...),
			"name": schema.StringAttribute{MarkdownDescription: "Display name.", Required: true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 200)}},
			"effect": requiredImmutableString("`condition` or `deny`.", stringvalidator.OneOf("condition", "deny")),
			"actions": schema.SetAttribute{
				MarkdownDescription: "Deny policies only: permissions the policy applies to (`*` for all). Must be empty for conditions.",
				ElementType:         types.StringType, Optional: true, Computed: true, Default: setdefault.StaticValue(setOf(nil)),
				Validators: []validator.Set{setvalidator.SizeAtMost(50), setvalidator.ValueStringsAre(stringvalidator.Any(
					stringvalidator.OneOf("*"), stringvalidator.RegexMatches(permissionRE, "must be <resource_type>:<action>")))},
			},
			"expression": schema.StringAttribute{MarkdownDescription: "Boolean expr-lang expression (compiled by Porch on save).",
				Required: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 4096)}},
		},
	}
}

func (r *policyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg policyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.Effect.IsUnknown() || cfg.Actions.IsUnknown() {
		return
	}
	n := 0
	if !cfg.Actions.IsNull() {
		n = len(cfg.Actions.Elements())
	}
	switch cfg.Effect.ValueString() {
	case "deny":
		if n == 0 {
			resp.Diagnostics.AddError("Missing actions", "deny policies list the actions they apply to (\"*\" for all).")
		}
	case "condition":
		if n > 0 {
			resp.Diagnostics.AddError("Invalid actions", "condition policies apply to the assignments that reference them; leave actions empty.")
		}
	}
}

func (m *policyModel) fill(p *client.Policy) {
	m.ID = types.StringValue(p.ID)
	m.Slug = types.StringValue(p.Slug)
	m.Name = types.StringValue(p.Name)
	m.Effect = types.StringValue(p.Effect)
	m.Actions = setOf(p.Actions)
	m.Expression = types.StringValue(p.Expression)
}

func (r *policyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan policyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := client.PolicyRequest{Slug: plan.Slug.ValueString(), Name: plan.Name.ValueString(), Effect: plan.Effect.ValueString(),
		Actions: stringsOf(ctx, plan.Actions, &resp.Diagnostics), Expression: plan.Expression.ValueString()}
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.CreatePolicy(ctx, plan.Tenant.ValueString(), in)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot create policy", err)
		return
	}
	plan.fill(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, p.ETag)...)
}

func (r *policyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state policyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetPolicy(ctx, state.Tenant.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read policy", err)
		return
	}
	state.fill(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, p.ETag)...)
}

func (r *policyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state policyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := client.PolicyRequest{Name: plan.Name.ValueString(), Effect: plan.Effect.ValueString(),
		Actions: stringsOf(ctx, plan.Actions, &resp.Diagnostics), Expression: plan.Expression.ValueString()}
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.ReplacePolicy(ctx, state.Tenant.ValueString(), state.ID.ValueString(), loadETag(ctx, req.Private), in)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot update policy", err)
		return
	}
	plan.fill(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, p.ETag)...)
}

func (r *policyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state policyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeletePolicy(ctx, state.Tenant.ValueString(), state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot delete policy", err)
	}
}

func (r *policyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importTenantScoped(ctx, req, resp)
}
