package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

// idValidator checks that a value is a Porch ID with the given prefix (references must be IDs, not slugs, or the value
// read back would differ from the configuration).
func idValidator(prefix string) validator.String {
	return stringvalidator.RegexMatches(regexp.MustCompile(`^`+prefix+`_[0-9a-z]{10,40}$`), "must be a Porch ID starting with "+prefix+"_")
}

var _ resource.ResourceWithImportState = (*roleResource)(nil)

type roleResource struct{ baseResource }

// NewRoleResource returns the porch_role resource.
func NewRoleResource() resource.Resource { return &roleResource{} }

type roleModel struct {
	ID          types.String `tfsdk:"id"`
	Tenant      types.String `tfsdk:"tenant"`
	Slug        types.String `tfsdk:"slug"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Permissions types.Set    `tfsdk:"permissions"`
	Includes    types.Set    `tfsdk:"includes"`
}

func (r *roleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (r *roleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A custom role of a tenant: a set of permissions (`<resource_type>:<action>`, either side may be `*`) plus " +
			"the roles it bundles. Needs `role:manage`; delegated administrators may only define roles with permissions they hold " +
			"(`403 privilege_escalation` otherwise). Updates replace the definition (PUT). Import with `<tenant>/<role_id>`.",
		Attributes: map[string]schema.Attribute{
			"id":     idAttribute("Role ID (`rol_…`)."),
			"tenant": tenantAttribute(),
			"slug":   requiredImmutableString("Identifier, unique in the tenant (`admin` and `member` are built in).", slugValidators()...),
			"name": schema.StringAttribute{MarkdownDescription: "Display name.", Required: true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 200)}},
			"description": schema.StringAttribute{MarkdownDescription: "Description.", Optional: true, Computed: true,
				Default: stringdefault.StaticString(""), Validators: []validator.String{stringvalidator.LengthAtMost(2000)}},
			"permissions": schema.SetAttribute{
				MarkdownDescription: "Permissions granted, e.g. `app:access`, `media:read`, `app:*`.",
				ElementType:         types.StringType, Optional: true, Computed: true, Default: setdefault.StaticValue(setOf(nil)),
				Validators: []validator.Set{setvalidator.SizeAtMost(100), setvalidator.ValueStringsAre(
					stringvalidator.RegexMatches(permissionRE, "must be <resource_type>:<action>"))},
			},
			"includes": schema.SetAttribute{
				MarkdownDescription: "IDs of roles bundled into this one (a DAG of depth ≤ 5).",
				ElementType:         types.StringType, Optional: true, Computed: true, Default: setdefault.StaticValue(setOf(nil)),
				Validators: []validator.Set{setvalidator.SizeAtMost(20), setvalidator.ValueStringsAre(idValidator("rol"))},
			},
		},
	}
}

func (m *roleModel) fill(r *client.Role) {
	m.ID = types.StringValue(r.ID)
	m.Slug = types.StringValue(r.Slug)
	m.Name = types.StringValue(r.Name)
	m.Description = types.StringValue(r.Description)
	m.Permissions = setOf(r.Permissions)
	m.Includes = setOf(r.Includes)
}

func (r *roleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan roleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := client.RoleRequest{Slug: plan.Slug.ValueString(), Name: plan.Name.ValueString(), Description: plan.Description.ValueString(),
		Permissions: stringsOf(ctx, plan.Permissions, &resp.Diagnostics), Includes: stringsOf(ctx, plan.Includes, &resp.Diagnostics)}
	if resp.Diagnostics.HasError() {
		return
	}
	ro, err := r.client.CreateRole(ctx, plan.Tenant.ValueString(), in)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot create role", err)
		return
	}
	plan.fill(ro)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, ro.ETag)...)
}

func (r *roleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state roleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ro, err := r.client.GetRole(ctx, state.Tenant.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read role", err)
		return
	}
	state.fill(ro)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, ro.ETag)...)
}

func (r *roleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state roleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := client.RoleRequest{Name: plan.Name.ValueString(), Description: plan.Description.ValueString(),
		Permissions: stringsOf(ctx, plan.Permissions, &resp.Diagnostics), Includes: stringsOf(ctx, plan.Includes, &resp.Diagnostics)}
	if resp.Diagnostics.HasError() {
		return
	}
	ro, err := r.client.ReplaceRole(ctx, state.Tenant.ValueString(), state.ID.ValueString(), loadETag(ctx, req.Private), in)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot update role", err)
		return
	}
	plan.fill(ro)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, ro.ETag)...)
}

func (r *roleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state roleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteRole(ctx, state.Tenant.ValueString(), state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot delete role", err)
	}
}

func (r *roleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importTenantScoped(ctx, req, resp)
}
