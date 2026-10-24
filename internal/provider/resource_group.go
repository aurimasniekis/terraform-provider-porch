package provider

import (
	"context"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

var _ resource.ResourceWithImportState = (*groupResource)(nil)

type groupResource struct{ baseResource }

// NewGroupResource returns the porch_group resource.
func NewGroupResource() resource.Resource { return &groupResource{} }

type groupModel struct {
	ID          types.String `tfsdk:"id"`
	Tenant      types.String `tfsdk:"tenant"`
	Slug        types.String `tfsdk:"slug"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
}

func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A group of tenant members (needs `group:manage`). Members are managed with `porch_group_member`, nesting " +
			"with `porch_group_parent`. Import with `<tenant>/<group_id>`.",
		Attributes: map[string]schema.Attribute{
			"id":     idAttribute("Group ID (`grp_…`)."),
			"tenant": tenantAttribute(),
			"slug":   requiredImmutableString("Identifier, unique in the tenant.", slugValidators()...),
			"name": schema.StringAttribute{MarkdownDescription: "Display name.", Required: true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 200)}},
			"description": schema.StringAttribute{MarkdownDescription: "Description.", Optional: true, Computed: true,
				Default: stringdefault.StaticString(""), Validators: []validator.String{stringvalidator.LengthAtMost(2000)}},
		},
	}
}

func (m *groupModel) fill(g *client.Group) {
	m.ID = types.StringValue(g.ID)
	m.Slug = types.StringValue(g.Slug)
	m.Name = types.StringValue(g.Name)
	m.Description = types.StringValue(g.Description)
}

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.CreateGroup(ctx, plan.Tenant.ValueString(), client.GroupCreate{Slug: plan.Slug.ValueString(), Name: plan.Name.ValueString(),
		Description: plan.Description.ValueString()})
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot create group", err)
		return
	}
	plan.fill(g)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, g.ETag)...)
}

func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.GetGroup(ctx, state.Tenant.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read group", err)
		return
	}
	state.fill(g)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, g.ETag)...)
}

func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.UpdateGroup(ctx, state.Tenant.ValueString(), state.ID.ValueString(), loadETag(ctx, req.Private),
		client.GroupUpdate{Name: plan.Name.ValueString(), Description: plan.Description.ValueString()})
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot update group", err)
		return
	}
	plan.fill(g)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, g.ETag)...)
}

func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteGroup(ctx, state.Tenant.ValueString(), state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot delete group", err)
	}
}

func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importTenantScoped(ctx, req, resp)
}

// porch_group_member

var _ resource.ResourceWithImportState = (*groupMemberResource)(nil)

type groupMemberResource struct{ baseResource }

// NewGroupMemberResource returns the porch_group_member resource.
func NewGroupMemberResource() resource.Resource { return &groupMemberResource{} }

type groupMemberModel struct {
	ID      types.String `tfsdk:"id"`
	Tenant  types.String `tfsdk:"tenant"`
	GroupID types.String `tfsdk:"group_id"`
	UserID  types.String `tfsdk:"user_id"`
}

func (r *groupMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group_member"
}

func (r *groupMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Adds a tenant member (user) to a group. The user must already be a member of the tenant " +
			"(`porch_tenant_member`). Import with `<tenant>/<group_id>/<user_id>`.",
		Attributes: map[string]schema.Attribute{
			"id":       idAttribute("`<tenant>/<group_id>/<user_id>`."),
			"tenant":   tenantAttribute(),
			"group_id": requiredImmutableString("ID (or slug) of the group."),
			"user_id":  requiredImmutableString("ID of the user."),
		},
	}
}

func (r *groupMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, g, u := plan.Tenant.ValueString(), plan.GroupID.ValueString(), plan.UserID.ValueString()
	if err := r.client.AddGroupMember(ctx, t, g, u); err != nil {
		apiError(&resp.Diagnostics, "Cannot add group member", err)
		return
	}
	plan.ID = types.StringValue(t + "/" + g + "/" + u)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.GetGroup(ctx, state.Tenant.ValueString(), state.GroupID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read group", err)
		return
	}
	if !slices.Contains(g.Members, state.UserID.ValueString()) {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *groupMemberResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Group memberships are immutable", "Every attribute forces replacement; this is a provider bug.")
}

func (r *groupMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.RemoveGroupMember(ctx, state.Tenant.ValueString(), state.GroupID.ValueString(), state.UserID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot remove group member", err)
	}
}

func (r *groupMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importTriple(ctx, req, resp, "<tenant>/<group_id>/<user_id>", "group_id", "user_id")
}

// importTriple handles "<tenant>/<a>/<b>" import IDs of edge resources.
func importTriple(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse, format, a, b string) {
	parts, err := splitID(req.ID, 3, format)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("tenant"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(a), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(b), parts[2])...)
}

// porch_group_parent

var _ resource.ResourceWithImportState = (*groupParentResource)(nil)

type groupParentResource struct{ baseResource }

// NewGroupParentResource returns the porch_group_parent resource.
func NewGroupParentResource() resource.Resource { return &groupParentResource{} }

type groupParentModel struct {
	ID            types.String `tfsdk:"id"`
	Tenant        types.String `tfsdk:"tenant"`
	GroupID       types.String `tfsdk:"group_id"`
	ParentGroupID types.String `tfsdk:"parent_group_id"`
}

func (r *groupParentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group_parent"
}

func (r *groupParentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Nests a group inside a parent group (a DAG of depth ≤ 5): members of the child inherit what the parent " +
			"is assigned. Import with `<tenant>/<group_id>/<parent_group_id>`.",
		Attributes: map[string]schema.Attribute{
			"id":              idAttribute("`<tenant>/<group_id>/<parent_group_id>`."),
			"tenant":          tenantAttribute(),
			"group_id":        requiredImmutableString("ID (or slug) of the child group."),
			"parent_group_id": requiredImmutableString("ID (or slug) of the parent group."),
		},
	}
}

func (r *groupParentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupParentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, g, p := plan.Tenant.ValueString(), plan.GroupID.ValueString(), plan.ParentGroupID.ValueString()
	if err := r.client.NestGroup(ctx, t, g, p); err != nil {
		apiError(&resp.Diagnostics, "Cannot nest group", err)
		return
	}
	plan.ID = types.StringValue(t + "/" + g + "/" + p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupParentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupParentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t := state.Tenant.ValueString()
	child, err := r.client.GetGroup(ctx, t, state.GroupID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read group", err)
		return
	}
	parentID := state.ParentGroupID.ValueString()
	if !slices.Contains(child.Parents, parentID) {
		// The parent may be referenced by slug: resolve it.
		parent, err := r.client.GetGroup(ctx, t, parentID)
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		if err != nil {
			apiError(&resp.Diagnostics, "Cannot read parent group", err)
			return
		}
		if !slices.Contains(child.Parents, parent.ID) {
			resp.State.RemoveResource(ctx)
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *groupParentResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Group nesting edges are immutable", "Every attribute forces replacement; this is a provider bug.")
}

func (r *groupParentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupParentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.UnnestGroup(ctx, state.Tenant.ValueString(), state.GroupID.ValueString(), state.ParentGroupID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot remove group nesting", err)
	}
}

func (r *groupParentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importTriple(ctx, req, resp, "<tenant>/<group_id>/<parent_group_id>", "group_id", "parent_group_id")
}
