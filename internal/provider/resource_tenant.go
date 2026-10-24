package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

var (
	_ resource.ResourceWithImportState = (*tenantResource)(nil)
	_ resource.ResourceWithConfigure   = (*tenantResource)(nil)
)

type tenantResource struct{ baseResource }

// NewTenantResource returns the porch_tenant resource.
func NewTenantResource() resource.Resource { return &tenantResource{} }

type tenantModel struct {
	ID                     types.String `tfsdk:"id"`
	Slug                   types.String `tfsdk:"slug"`
	Name                   types.String `tfsdk:"name"`
	Status                 types.String `tfsdk:"status"`
	SessionIdleSeconds     types.Int64  `tfsdk:"session_idle_seconds"`
	SessionAbsoluteSeconds types.Int64  `tfsdk:"session_absolute_seconds"`
}

func (r *tenantResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tenant"
}

func sessionSecondsValidators() []validator.Int64 {
	return []validator.Int64{int64validator.Any(int64validator.OneOf(0), int64validator.Between(60, 90*24*3600))}
}

func (r *tenantResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Porch tenant (for example a family or a team). Deleting a tenant deletes everything it owns. " +
			"Requires an installation `admin` token.",
		Attributes: map[string]schema.Attribute{
			"id":   idAttribute("Tenant ID (`ten_…`)."),
			"slug": requiredImmutableString("URL-safe identifier, unique in the installation.", slugValidators()...),
			"name": schema.StringAttribute{MarkdownDescription: "Display name.", Required: true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 200)}},
			"status": schema.StringAttribute{
				MarkdownDescription: "`active` (default) or `disabled`. Disabling a tenant blocks sign-ins to it.",
				Optional:            true, Computed: true, Default: stringdefault.StaticString("active"),
				Validators: []validator.String{stringvalidator.OneOf("active", "disabled")},
			},
			"session_idle_seconds": schema.Int64Attribute{
				MarkdownDescription: "Per-tenant session idle timeout in seconds (60..7776000); `0` (default) uses the installation default.",
				Optional:            true, Computed: true, Default: int64default.StaticInt64(0), Validators: sessionSecondsValidators(),
			},
			"session_absolute_seconds": schema.Int64Attribute{
				MarkdownDescription: "Per-tenant absolute session lifetime in seconds (60..7776000); `0` (default) uses the installation default.",
				Optional:            true, Computed: true, Default: int64default.StaticInt64(0), Validators: sessionSecondsValidators(),
			},
		},
	}
}

func (m *tenantModel) fill(t *client.Tenant) {
	m.ID = types.StringValue(t.ID)
	m.Slug = types.StringValue(t.Slug)
	m.Name = types.StringValue(t.Name)
	m.Status = types.StringValue(t.Status)
	m.SessionIdleSeconds = types.Int64Value(t.SessionIdleSeconds)
	m.SessionAbsoluteSeconds = types.Int64Value(t.SessionAbsoluteSeconds)
}

func (r *tenantResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan tenantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, err := r.client.CreateTenant(ctx, client.TenantCreate{Slug: plan.Slug.ValueString(), Name: plan.Name.ValueString()})
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot create tenant", err)
		return
	}
	if plan.Status.ValueString() != t.Status || plan.SessionIdleSeconds.ValueInt64() != 0 || plan.SessionAbsoluteSeconds.ValueInt64() != 0 {
		up := client.TenantUpdate{SessionIdleSeconds: ptr(plan.SessionIdleSeconds.ValueInt64()),
			SessionAbsoluteSeconds: ptr(plan.SessionAbsoluteSeconds.ValueInt64())}
		if plan.Status.ValueString() != t.Status {
			up.Status = ptr(plan.Status.ValueString())
		}
		t2, err := r.client.UpdateTenant(ctx, t.ID, t.ETag, up)
		if err != nil {
			// The tenant exists: keep it in state so the next apply converges instead of leaking it.
			plan.fill(t)
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			resp.Diagnostics.Append(saveETag(ctx, resp.Private, t.ETag)...)
			apiError(&resp.Diagnostics, "Tenant created but its settings could not be applied", err)
			return
		}
		t = t2
	}
	plan.fill(t)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, t.ETag)...)
}

func (r *tenantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state tenantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, err := r.client.GetTenant(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read tenant", err)
		return
	}
	state.fill(t)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, t.ETag)...)
}

func (r *tenantResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state tenantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	up := client.TenantUpdate{}
	if !plan.Name.Equal(state.Name) {
		up.Name = ptr(plan.Name.ValueString())
	}
	if !plan.Status.Equal(state.Status) {
		// Only installation operators may change the status, so it is sent only when it changes.
		up.Status = ptr(plan.Status.ValueString())
	}
	if !plan.SessionIdleSeconds.Equal(state.SessionIdleSeconds) || !plan.SessionAbsoluteSeconds.Equal(state.SessionAbsoluteSeconds) {
		up.SessionIdleSeconds = ptr(plan.SessionIdleSeconds.ValueInt64())
		up.SessionAbsoluteSeconds = ptr(plan.SessionAbsoluteSeconds.ValueInt64())
	}
	t, err := r.client.UpdateTenant(ctx, state.ID.ValueString(), loadETag(ctx, req.Private), up)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot update tenant", err)
		return
	}
	plan.fill(t)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, t.ETag)...)
}

func (r *tenantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state tenantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteTenant(ctx, state.ID.ValueString(), loadETag(ctx, req.Private))
	if err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot delete tenant", err)
	}
}

func (r *tenantResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Accept an ID or a slug; Read resolves it.
	t, err := r.client.GetTenant(ctx, req.ID)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot import tenant", err)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), t.ID)...)
}
