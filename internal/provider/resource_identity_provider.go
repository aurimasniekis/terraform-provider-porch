package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

var _ resource.ResourceWithImportState = (*identityProviderResource)(nil)

type identityProviderResource struct{ baseResource }

// NewIdentityProviderResource returns the porch_identity_provider resource.
func NewIdentityProviderResource() resource.Resource { return &identityProviderResource{} }

type identityProviderModel struct {
	ID           types.String `tfsdk:"id"`
	Slug         types.String `tfsdk:"slug"`
	Kind         types.String `tfsdk:"kind"`
	Name         types.String `tfsdk:"name"`
	Issuer       types.String `tfsdk:"issuer"`
	ClientID     types.String `tfsdk:"client_id"`
	ClientSecret types.String `tfsdk:"client_secret"`
	AllowedHDs   types.Set    `tfsdk:"allowed_hds"`
	Scopes       types.String `tfsdk:"scopes"`
	Enabled      types.Bool   `tfsdk:"enabled"`
	CallbackURL  types.String `tfsdk:"callback_url"`
}

func (r *identityProviderResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_identity_provider"
}

func (r *identityProviderResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An upstream identity provider (Google or generic OpenID Connect) users sign in with. Installation-wide; " +
			"requires an installation `admin` token. The client secret is write-only in Porch: it is kept in state from configuration " +
			"and never read back.",
		Attributes: map[string]schema.Attribute{
			"id":   idAttribute("Provider ID (`idp_…`)."),
			"slug": requiredImmutableString("URL-safe identifier used in the callback URL (`/login/<slug>/callback`).", slugValidators()...),
			"kind": requiredImmutableString("`google` or `oidc`.", stringvalidator.OneOf("google", "oidc")),
			"name": schema.StringAttribute{MarkdownDescription: "Display name shown on the sign-in page.", Required: true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 200)}},
			"issuer": schema.StringAttribute{
				MarkdownDescription: "OpenID issuer URL. Defaults to `https://accounts.google.com` for `google`; required for `oidc`.",
				Optional:            true, Computed: true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"client_id": schema.StringAttribute{MarkdownDescription: "OAuth client ID registered at the provider.", Required: true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 512)}},
			"client_secret": schema.StringAttribute{
				MarkdownDescription: "OAuth client secret (write-only in Porch; changing it rotates the stored secret).",
				Required:            true, Sensitive: true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 1024)},
			},
			"allowed_hds": schema.SetAttribute{
				MarkdownDescription: "Google Workspace domains accepted (`google` only; empty = any account).",
				ElementType:         types.StringType, Optional: true, Computed: true,
				Default:    setdefault.StaticValue(setOf(nil)),
				Validators: []validator.Set{setvalidator.ValueStringsAre(lowercaseValidator()), setvalidator.SizeAtMost(50)},
			},
			"scopes": schema.StringAttribute{
				MarkdownDescription: "Space-separated scopes requested upstream (must include `openid`). Default `openid email profile`.",
				Optional:            true, Computed: true, Default: stringdefault.StaticString("openid email profile"),
			},
			"enabled": schema.BoolAttribute{MarkdownDescription: "Whether the provider is offered at sign-in. Default `true`.",
				Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
			"callback_url": computedString("Redirect URI to register at the upstream provider."),
		},
	}
}

func (m *identityProviderModel) fill(p *client.IdentityProvider) {
	m.ID = types.StringValue(p.ID)
	m.Slug = types.StringValue(p.Slug)
	m.Kind = types.StringValue(p.Kind)
	m.Name = types.StringValue(p.Name)
	m.Issuer = types.StringValue(p.Issuer)
	m.ClientID = types.StringValue(p.ClientID)
	m.AllowedHDs = setOf(p.AllowedHDs)
	m.Scopes = types.StringValue(p.Scopes)
	m.Enabled = types.BoolValue(p.Enabled)
	m.CallbackURL = types.StringValue(p.CallbackURL)
}

func (r *identityProviderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan identityProviderModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := client.IdentityProviderCreate{Slug: plan.Slug.ValueString(), Kind: plan.Kind.ValueString(), Name: plan.Name.ValueString(),
		Issuer: plan.Issuer.ValueString(), ClientID: plan.ClientID.ValueString(), ClientSecret: plan.ClientSecret.ValueString(),
		AllowedHDs: stringsOf(ctx, plan.AllowedHDs, &resp.Diagnostics), Scopes: plan.Scopes.ValueString(), Enabled: ptr(plan.Enabled.ValueBool())}
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.CreateIdentityProvider(ctx, in)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot create identity provider", err)
		return
	}
	plan.fill(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, p.ETag)...)
}

func (r *identityProviderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state identityProviderModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetIdentityProvider(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read identity provider", err)
		return
	}
	state.fill(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, p.ETag)...)
}

func (r *identityProviderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state identityProviderModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := client.IdentityProviderUpdate{Name: plan.Name.ValueString(), ClientID: plan.ClientID.ValueString(),
		AllowedHDs: ptr(stringsOf(ctx, plan.AllowedHDs, &resp.Diagnostics)), Scopes: plan.Scopes.ValueString(), Enabled: ptr(plan.Enabled.ValueBool())}
	if !plan.Issuer.IsUnknown() {
		in.Issuer = plan.Issuer.ValueString()
	}
	if !plan.ClientSecret.Equal(state.ClientSecret) {
		in.ClientSecret = plan.ClientSecret.ValueString()
	}
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.UpdateIdentityProvider(ctx, state.ID.ValueString(), loadETag(ctx, req.Private), in)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot update identity provider", err)
		return
	}
	plan.fill(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, p.ETag)...)
}

func (r *identityProviderResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state identityProviderModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteIdentityProvider(ctx, state.ID.ValueString(), loadETag(ctx, req.Private)); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot delete identity provider", err)
	}
}

func (r *identityProviderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p, err := r.client.GetIdentityProvider(ctx, req.ID)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot import identity provider", err)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), p.ID)...)
}
