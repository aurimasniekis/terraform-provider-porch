package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

var _ resource.ResourceWithImportState = (*userResource)(nil)

type userResource struct{ baseResource }

// NewUserResource returns the porch_user resource.
func NewUserResource() resource.Resource { return &userResource{} }

type userModel struct {
	ID           types.String `tfsdk:"id"`
	PublicID     types.String `tfsdk:"public_id"`
	DisplayName  types.String `tfsdk:"display_name"`
	PrimaryEmail types.String `tfsdk:"primary_email"`
	Status       types.String `tfsdk:"status"`
}

func (r *userResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *userResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An installation user (a person). Users sign in with linked upstream identities; the primary email is an " +
			"attribute only and never links login identities. Add users to tenants with `porch_tenant_member`. Requires an installation " +
			"`admin` token.",
		Attributes: map[string]schema.Attribute{
			"id":        idAttribute("User ID (`usr_…`)."),
			"public_id": computedString("Stable OIDC subject (`sub`) of the user."),
			"display_name": schema.StringAttribute{MarkdownDescription: "Display name.", Required: true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 200)}},
			"primary_email": schema.StringAttribute{MarkdownDescription: "Primary email address (lowercase).", Optional: true,
				Validators: []validator.String{stringvalidator.LengthBetween(3, 254), lowercaseValidator()}},
			"status": schema.StringAttribute{
				MarkdownDescription: "`active` (default) or `disabled`. Disabling a user ends all of their sessions immediately.",
				Optional:            true, Computed: true, Default: stringdefault.StaticString("active"),
				Validators: []validator.String{stringvalidator.OneOf("active", "disabled")},
			},
		},
	}
}

func (m *userModel) fill(u *client.User) {
	m.ID = types.StringValue(u.ID)
	m.PublicID = types.StringValue(u.PublicID)
	m.DisplayName = types.StringValue(u.DisplayName)
	m.PrimaryEmail = optString(u.PrimaryEmail)
	m.Status = types.StringValue(u.Status)
}

func (r *userResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	u, err := r.client.CreateUser(ctx, client.UserCreate{DisplayName: plan.DisplayName.ValueString(), PrimaryEmail: plan.PrimaryEmail.ValueString()})
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot create user", err)
		return
	}
	if plan.Status.ValueString() != u.Status {
		u2, err := r.client.UpdateUser(ctx, u.ID, u.ETag, client.UserUpdate{Status: ptr(plan.Status.ValueString())})
		if err != nil {
			plan.fill(u)
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			resp.Diagnostics.Append(saveETag(ctx, resp.Private, u.ETag)...)
			apiError(&resp.Diagnostics, "User created but its status could not be set", err)
			return
		}
		u = u2
	}
	plan.fill(u)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, u.ETag)...)
}

func (r *userResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	u, err := r.client.GetUser(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read user", err)
		return
	}
	state.fill(u)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, u.ETag)...)
}

func (r *userResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// An empty primary_email clears it.
	in := client.UserUpdate{DisplayName: ptr(plan.DisplayName.ValueString()), PrimaryEmail: ptr(plan.PrimaryEmail.ValueString()),
		Status: ptr(plan.Status.ValueString())}
	u, err := r.client.UpdateUser(ctx, state.ID.ValueString(), loadETag(ctx, req.Private), in)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot update user", err)
		return
	}
	plan.fill(u)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, u.ETag)...)
}

func (r *userResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteUser(ctx, state.ID.ValueString(), loadETag(ctx, req.Private)); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot delete user", err)
	}
}

func (r *userResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
