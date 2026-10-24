package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func lookupAttr(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Optional: true, Computed: true}
}

func exactlyOneOf(a, b string) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot(a), path.MatchRoot(b))}
}

// porch_tenant

type tenantDataSource struct{ baseDataSource }

// NewTenantDataSource returns the porch_tenant data source.
func NewTenantDataSource() datasource.DataSource { return &tenantDataSource{} }

func (d *tenantDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tenant"
}

func (d *tenantDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return exactlyOneOf("id", "slug")
}

func (d *tenantDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a tenant by ID or slug.",
		Attributes: map[string]schema.Attribute{
			"id":                       lookupAttr("Tenant ID. Exactly one of `id` and `slug` must be set."),
			"slug":                     lookupAttr("Tenant slug."),
			"name":                     schema.StringAttribute{MarkdownDescription: "Display name.", Computed: true},
			"status":                   schema.StringAttribute{MarkdownDescription: "`active` or `disabled`.", Computed: true},
			"session_idle_seconds":     schema.Int64Attribute{MarkdownDescription: "Idle timeout override (0 = default).", Computed: true},
			"session_absolute_seconds": schema.Int64Attribute{MarkdownDescription: "Absolute lifetime override (0 = default).", Computed: true},
		},
	}
}

func (d *tenantDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m tenantModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ref := m.ID.ValueString()
	if ref == "" {
		ref = m.Slug.ValueString()
	}
	t, err := d.client.GetTenant(ctx, ref)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read tenant", err)
		return
	}
	if !m.Slug.IsNull() && t.Slug != m.Slug.ValueString() {
		resp.Diagnostics.AddError("Tenant not found", "No tenant has slug "+m.Slug.ValueString()+".")
		return
	}
	m.fill(t)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// porch_role

type roleDataSource struct{ baseDataSource }

// NewRoleDataSource returns the porch_role data source.
func NewRoleDataSource() datasource.DataSource { return &roleDataSource{} }

type roleDataModel struct {
	ID          types.String `tfsdk:"id"`
	Tenant      types.String `tfsdk:"tenant"`
	Slug        types.String `tfsdk:"slug"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	BuiltIn     types.Bool   `tfsdk:"built_in"`
	Permissions types.Set    `tfsdk:"permissions"`
	Includes    types.Set    `tfsdk:"includes"`
}

func (d *roleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (d *roleDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return exactlyOneOf("id", "slug")
}

func (d *roleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a role of a tenant by ID or slug, including the built-in `admin` and `member` roles.",
		Attributes: map[string]schema.Attribute{
			"tenant":      schema.StringAttribute{MarkdownDescription: "Tenant ID or slug.", Required: true},
			"id":          lookupAttr("Role ID. Exactly one of `id` and `slug` must be set."),
			"slug":        lookupAttr("Role slug, e.g. `admin`."),
			"name":        schema.StringAttribute{MarkdownDescription: "Display name.", Computed: true},
			"description": schema.StringAttribute{MarkdownDescription: "Description.", Computed: true},
			"built_in":    schema.BoolAttribute{MarkdownDescription: "Whether the role is built in.", Computed: true},
			"permissions": schema.SetAttribute{MarkdownDescription: "Permissions.", ElementType: types.StringType, Computed: true},
			"includes":    schema.SetAttribute{MarkdownDescription: "Bundled role IDs.", ElementType: types.StringType, Computed: true},
		},
	}
}

func (d *roleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m roleDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ref := m.ID.ValueString()
	if ref == "" {
		ref = m.Slug.ValueString()
	}
	r, err := d.client.GetRole(ctx, m.Tenant.ValueString(), ref)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read role", err)
		return
	}
	m.ID, m.Slug, m.Name = types.StringValue(r.ID), types.StringValue(r.Slug), types.StringValue(r.Name)
	m.Description, m.BuiltIn = types.StringValue(r.Description), types.BoolValue(r.BuiltIn)
	m.Permissions, m.Includes = setOf(r.Permissions), setOf(r.Includes)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// porch_user

type userDataSource struct{ baseDataSource }

// NewUserDataSource returns the porch_user data source.
func NewUserDataSource() datasource.DataSource { return &userDataSource{} }

type userDataModel struct {
	Email       types.String `tfsdk:"email"`
	ID          types.String `tfsdk:"id"`
	PublicID    types.String `tfsdk:"public_id"`
	DisplayName types.String `tfsdk:"display_name"`
	Status      types.String `tfsdk:"status"`
}

func (d *userDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *userDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up an installation user by primary email (exact, case-insensitive). Requires an installation token.",
		Attributes: map[string]schema.Attribute{
			"email":        schema.StringAttribute{MarkdownDescription: "Primary email address.", Required: true},
			"id":           schema.StringAttribute{MarkdownDescription: "User ID.", Computed: true},
			"public_id":    schema.StringAttribute{MarkdownDescription: "Stable OIDC subject.", Computed: true},
			"display_name": schema.StringAttribute{MarkdownDescription: "Display name.", Computed: true},
			"status":       schema.StringAttribute{MarkdownDescription: "`active` or `disabled`.", Computed: true},
		},
	}
}

func (d *userDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m userDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	u, err := d.client.FindUserByEmail(ctx, m.Email.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot find user", err)
		return
	}
	m.ID, m.PublicID = types.StringValue(u.ID), types.StringValue(u.PublicID)
	m.DisplayName, m.Status = types.StringValue(u.DisplayName), types.StringValue(u.Status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// porch_group

type groupDataSource struct{ baseDataSource }

// NewGroupDataSource returns the porch_group data source.
func NewGroupDataSource() datasource.DataSource { return &groupDataSource{} }

type groupDataModel struct {
	ID          types.String `tfsdk:"id"`
	Tenant      types.String `tfsdk:"tenant"`
	Slug        types.String `tfsdk:"slug"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Members     types.Set    `tfsdk:"members"`
	Parents     types.Set    `tfsdk:"parents"`
}

func (d *groupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (d *groupDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return exactlyOneOf("id", "slug")
}

func (d *groupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a group of a tenant by ID or slug.",
		Attributes: map[string]schema.Attribute{
			"tenant":      schema.StringAttribute{MarkdownDescription: "Tenant ID or slug.", Required: true},
			"id":          lookupAttr("Group ID. Exactly one of `id` and `slug` must be set."),
			"slug":        lookupAttr("Group slug."),
			"name":        schema.StringAttribute{MarkdownDescription: "Display name.", Computed: true},
			"description": schema.StringAttribute{MarkdownDescription: "Description.", Computed: true},
			"members":     schema.SetAttribute{MarkdownDescription: "IDs of direct member users.", ElementType: types.StringType, Computed: true},
			"parents":     schema.SetAttribute{MarkdownDescription: "IDs of parent groups.", ElementType: types.StringType, Computed: true},
		},
	}
}

func (d *groupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m groupDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ref := m.ID.ValueString()
	if ref == "" {
		ref = m.Slug.ValueString()
	}
	g, err := d.client.GetGroup(ctx, m.Tenant.ValueString(), ref)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read group", err)
		return
	}
	m.ID, m.Slug, m.Name, m.Description = types.StringValue(g.ID), types.StringValue(g.Slug), types.StringValue(g.Name), types.StringValue(g.Description)
	m.Members, m.Parents = setOf(g.Members), setOf(g.Parents)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
