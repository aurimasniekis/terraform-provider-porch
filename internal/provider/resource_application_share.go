package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

var _ resource.ResourceWithImportState = (*applicationShareResource)(nil)

type applicationShareResource struct{ baseResource }

// NewApplicationShareResource returns the porch_application_share resource.
func NewApplicationShareResource() resource.Resource { return &applicationShareResource{} }

type applicationShareModel struct {
	ID             types.String `tfsdk:"id"`
	Tenant         types.String `tfsdk:"tenant"`
	ApplicationID  types.String `tfsdk:"application_id"`
	TargetTenant   types.String `tfsdk:"target_tenant"`
	TargetTenantID types.String `tfsdk:"target_tenant_id"`
}

func (r *applicationShareResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application_share"
}

func (r *applicationShareResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Shares an application owned by one tenant with another tenant (ADR-0034): members of the target tenant pass " +
			"the tenant gate of the application, subject to its access mode and the owner's deny policies. Cross-tenant shares are an " +
			"installation matter and need an installation `admin` token. Import with `<tenant>/<application_id>/<target_tenant>`.",
		Attributes: map[string]schema.Attribute{
			"id":               idAttribute("`<tenant>/<application_id>/<target_tenant>`."),
			"tenant":           tenantAttribute(),
			"application_id":   requiredImmutableString("ID of the shared application."),
			"target_tenant":    requiredImmutableString("ID or slug of the tenant the application is shared with."),
			"target_tenant_id": computedString("ID of the target tenant."),
		},
	}
}

func (r *applicationShareResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan applicationShareModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, app, target := plan.Tenant.ValueString(), plan.ApplicationID.ValueString(), plan.TargetTenant.ValueString()
	if err := r.client.ShareApplication(ctx, t, app, target); err != nil {
		apiError(&resp.Diagnostics, "Cannot share application", err)
		return
	}
	s, err := r.client.GetApplicationShare(ctx, t, app, target)
	if err != nil {
		apiError(&resp.Diagnostics, "Application shared but the share could not be read back", err)
		return
	}
	plan.ID = types.StringValue(t + "/" + app + "/" + target)
	plan.TargetTenantID = types.StringValue(s.TenantID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *applicationShareResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state applicationShareModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.client.GetApplicationShare(ctx, state.Tenant.ValueString(), state.ApplicationID.ValueString(), state.TargetTenant.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read application share", err)
		return
	}
	state.TargetTenantID = types.StringValue(s.TenantID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *applicationShareResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Application shares are immutable", "Every attribute forces replacement; this is a provider bug.")
}

func (r *applicationShareResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state applicationShareModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.UnshareApplication(ctx, state.Tenant.ValueString(), state.ApplicationID.ValueString(), state.TargetTenant.ValueString())
	if err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot stop sharing application", err)
	}
}

func (r *applicationShareResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importTriple(ctx, req, resp, "<tenant>/<application_id>/<target_tenant>", "application_id", "target_tenant")
}
