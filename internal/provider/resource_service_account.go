package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

var _ resource.ResourceWithImportState = (*serviceAccountResource)(nil)

type serviceAccountResource struct{ baseResource }

// NewServiceAccountResource returns the porch_service_account resource.
func NewServiceAccountResource() resource.Resource { return &serviceAccountResource{} }

type serviceAccountModel struct {
	ID             types.String `tfsdk:"id"`
	Tenant         types.String `tfsdk:"tenant"`
	Slug           types.String `tfsdk:"slug"`
	Name           types.String `tfsdk:"name"`
	Description    types.String `tfsdk:"description"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	Audiences      types.Set    `tfsdk:"audiences"`
	AccessTokenTTL types.Int64  `tfsdk:"access_token_ttl_seconds"`
	ClientSecret   types.String `tfsdk:"client_secret"`
	SecretVersion  types.String `tfsdk:"secret_version"`
	TokenEndpoint  types.String `tfsdk:"token_endpoint"`
}

func (r *serviceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

func (r *serviceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A tenant service account (machine identity, ADR-0037). It obtains access tokens from `token_endpoint` with " +
			"the client credentials grant, using its `id` as `client_id` and `client_secret`. Grant it roles with " +
			"`porch_role_assignment` (`subject_type = \"service_account\"`). Needs `service_account:manage`. The secret is returned " +
			"only at creation; it is kept in state (sensitive) and never re-read. Change `secret_version` to rotate it. Import with " +
			"`<tenant>/<service_account_id>`.",
		Attributes: map[string]schema.Attribute{
			"id":     idAttribute("Service account ID (`sva_…`), also its OAuth `client_id`."),
			"tenant": tenantAttribute(),
			"slug":   requiredImmutableString("Identifier, unique in the tenant.", slugValidators()...),
			"name": schema.StringAttribute{MarkdownDescription: "Display name.", Required: true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 200)}},
			"description": schema.StringAttribute{MarkdownDescription: "Description.", Optional: true, Computed: true,
				Default: stringdefault.StaticString(""), Validators: []validator.String{stringvalidator.LengthAtMost(2000)}},
			"enabled": schema.BoolAttribute{MarkdownDescription: "Disabled service accounts fail every check and cannot obtain tokens. Default `true`.",
				Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
			"audiences": schema.SetAttribute{
				MarkdownDescription: "API audiences its access tokens may target (RFC 8707 `resource` values).",
				ElementType:         types.StringType, Optional: true, Computed: true, Default: setdefault.StaticValue(setOf(nil)),
				Validators: []validator.Set{setvalidator.SizeAtMost(20)},
			},
			"access_token_ttl_seconds": schema.Int64Attribute{MarkdownDescription: "Access token lifetime (60..3600, default 600).",
				Optional: true, Computed: true, Default: int64default.StaticInt64(600),
				Validators: []validator.Int64{int64validator.Between(60, 3600)}},
			"client_secret": schema.StringAttribute{
				MarkdownDescription: "Client secret (`porch_sa_…`). Only known after create (or rotation); never re-read.",
				Computed:            true, Sensitive: true,
				PlanModifiers: []planmodifier.String{secretPlanModifier{}},
			},
			"secret_version": schema.StringAttribute{
				MarkdownDescription: "Arbitrary value; changing it (including setting it for the first time, e.g. after an import) rotates the secret and stores the new one. The old secret stops working immediately.",
				Optional:            true,
			},
			"token_endpoint": computedString("OAuth token endpoint to use with the client credentials grant."),
		},
	}
}

func (m *serviceAccountModel) fill(a *client.ServiceAccount) {
	m.ID = types.StringValue(a.ID)
	m.Slug = types.StringValue(a.Slug)
	m.Name = types.StringValue(a.Name)
	m.Description = types.StringValue(a.Description)
	m.Enabled = types.BoolValue(a.Enabled)
	m.Audiences = setOf(a.Audiences)
	m.AccessTokenTTL = types.Int64Value(a.AccessTokenTTL)
	m.TokenEndpoint = types.StringValue(a.TokenEndpoint)
	if m.ClientSecret.IsUnknown() {
		m.ClientSecret = types.StringNull()
	}
}

func (r *serviceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tenant := plan.Tenant.ValueString()
	in := client.ServiceAccountCreate{Slug: plan.Slug.ValueString(), Name: plan.Name.ValueString(), Description: plan.Description.ValueString(),
		Audiences: stringsOf(ctx, plan.Audiences, &resp.Diagnostics), AccessTokenTTL: plan.AccessTokenTTL.ValueInt64()}
	if resp.Diagnostics.HasError() {
		return
	}
	a, created, err := r.client.CreateServiceAccount(ctx, tenant, in)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot create service account", err)
		return
	}
	plan.ClientSecret = optString(a.ClientSecret)
	save := func(x *client.ServiceAccount) {
		plan.fill(x)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.Append(saveETag(ctx, resp.Private, x.ETag)...)
	}
	if created.SecretWithheld {
		s, err := r.client.RotateServiceAccountSecret(ctx, tenant, a.ID)
		if err != nil {
			save(a)
			apiError(&resp.Diagnostics, "Service account created but its secret was lost and could not be rotated", err)
			return
		}
		plan.ClientSecret = types.StringValue(s.ClientSecret)
		resp.Diagnostics.AddWarning("Service account secret rotated", "The service account was created by an earlier attempt whose "+
			"response was lost; a new secret was issued.")
		if a, err = r.client.GetServiceAccount(ctx, tenant, a.ID); err != nil {
			apiError(&resp.Diagnostics, "Cannot read service account", err)
			return
		}
	}
	if !plan.Enabled.ValueBool() && a.Enabled {
		a2, err := r.client.UpdateServiceAccount(ctx, tenant, a.ID, a.ETag, client.ServiceAccountUpdate{Enabled: ptr(false)})
		if err != nil {
			save(a)
			apiError(&resp.Diagnostics, "Service account created but could not be disabled", err)
			return
		}
		a = a2
	}
	save(a)
}

func (r *serviceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	a, err := r.client.GetServiceAccount(ctx, state.Tenant.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read service account", err)
		return
	}
	state.fill(a)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, a.ETag)...)
}

func (r *serviceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serviceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tenant, id := state.Tenant.ValueString(), state.ID.ValueString()
	in := client.ServiceAccountUpdate{Name: ptr(plan.Name.ValueString()), Description: ptr(plan.Description.ValueString()),
		Enabled: ptr(plan.Enabled.ValueBool()), AccessTokenTTL: ptr(plan.AccessTokenTTL.ValueInt64())}
	if !plan.Audiences.Equal(state.Audiences) {
		in.Audiences = ptr(stringsOf(ctx, plan.Audiences, &resp.Diagnostics))
	}
	if resp.Diagnostics.HasError() {
		return
	}
	a, err := r.client.UpdateServiceAccount(ctx, tenant, id, loadETag(ctx, req.Private), in)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot update service account", err)
		return
	}
	plan.ClientSecret = state.ClientSecret
	if !plan.SecretVersion.Equal(state.SecretVersion) {
		s, err := r.client.RotateServiceAccountSecret(ctx, tenant, id)
		if err != nil {
			plan.SecretVersion = state.SecretVersion
			plan.fill(a)
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			resp.Diagnostics.Append(saveETag(ctx, resp.Private, a.ETag)...)
			apiError(&resp.Diagnostics, "Cannot rotate the service account secret", err)
			return
		}
		plan.ClientSecret = types.StringValue(s.ClientSecret)
		if a, err = r.client.GetServiceAccount(ctx, tenant, id); err != nil {
			apiError(&resp.Diagnostics, "Cannot read service account after rotating its secret", err)
			return
		}
	}
	plan.fill(a)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, a.ETag)...)
}

func (r *serviceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteServiceAccount(ctx, state.Tenant.ValueString(), state.ID.ValueString(), loadETag(ctx, req.Private))
	if err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot delete service account", err)
	}
}

func (r *serviceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importTenantScoped(ctx, req, resp)
}
