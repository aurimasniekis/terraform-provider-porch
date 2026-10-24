package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

var (
	_ resource.ResourceWithImportState    = (*applicationResource)(nil)
	_ resource.ResourceWithValidateConfig = (*applicationResource)(nil)
)

type applicationResource struct{ baseResource }

// NewApplicationResource returns the porch_application resource.
func NewApplicationResource() resource.Resource { return &applicationResource{} }

type applicationModel struct {
	ID             types.String `tfsdk:"id"`
	Tenant         types.String `tfsdk:"tenant"`
	Slug           types.String `tfsdk:"slug"`
	Name           types.String `tfsdk:"name"`
	Kind           types.String `tfsdk:"kind"`
	Description    types.String `tfsdk:"description"`
	LaunchURL      types.String `tfsdk:"launch_url"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	AccessMode     types.String `tfsdk:"access_mode"`
	Disclosure     types.Object `tfsdk:"disclosure"`
	Hosts          types.Set    `tfsdk:"hosts"`
	ForwardHeaders types.Map    `tfsdk:"forward_headers"`
	Group          types.String `tfsdk:"group"`
	IconSource     types.String `tfsdk:"icon_source"`
	IconRef        types.String `tfsdk:"icon_ref"`
	Client         types.Object `tfsdk:"client"`
	ClientID       types.String `tfsdk:"client_id"`
	ClientSecret   types.String `tfsdk:"client_secret"`
	SecretVersion  types.String `tfsdk:"secret_version"`
	Issuer         types.String `tfsdk:"issuer"`
	DiscoveryURL   types.String `tfsdk:"discovery_url"`
}

type disclosureModel struct {
	Email        types.Bool `tfsdk:"email"`
	Groups       types.Bool `tfsdk:"groups"`
	Roles        types.Bool `tfsdk:"roles"`
	Entitlements types.Bool `tfsdk:"entitlements"`
}

var disclosureAttrTypes = map[string]attr.Type{"email": types.BoolType, "groups": types.BoolType, "roles": types.BoolType,
	"entitlements": types.BoolType}

type oidcClientModel struct {
	ClientType      types.String `tfsdk:"client_type"`
	ApplicationType types.String `tfsdk:"application_type"`
	RedirectURIs    types.Set    `tfsdk:"redirect_uris"`
	PostLogoutURIs  types.Set    `tfsdk:"post_logout_redirect_uris"`
	GrantTypes      types.Set    `tfsdk:"grant_types"`
	AllowedScopes   types.Set    `tfsdk:"allowed_scopes"`
	Audiences       types.Set    `tfsdk:"audiences"`
	AllowedOrigins  types.Set    `tfsdk:"allowed_origins"`
	RequirePKCE     types.Bool   `tfsdk:"require_pkce"`
	SigningAlg      types.String `tfsdk:"signing_alg"`
	AccessTokenTTL  types.Int64  `tfsdk:"access_token_ttl_seconds"`
	IDTokenTTL      types.Int64  `tfsdk:"id_token_ttl_seconds"`
	RefreshTTL      types.Int64  `tfsdk:"refresh_ttl_seconds"`
	RefreshIdle     types.Int64  `tfsdk:"refresh_idle_seconds"`
}

var stringSet = types.SetType{ElemType: types.StringType}

var oidcClientAttrTypes = map[string]attr.Type{
	"client_type": types.StringType, "application_type": types.StringType, "redirect_uris": stringSet,
	"post_logout_redirect_uris": stringSet, "grant_types": stringSet, "allowed_scopes": stringSet, "audiences": stringSet,
	"allowed_origins": stringSet, "require_pkce": types.BoolType, "signing_alg": types.StringType,
	"access_token_ttl_seconds": types.Int64Type, "id_token_ttl_seconds": types.Int64Type, "refresh_ttl_seconds": types.Int64Type,
	"refresh_idle_seconds": types.Int64Type,
}

// Server defaults (internal/apps/apps.go validateClient and Create).
var (
	defaultGrantTypes = []string{"authorization_code"}
	defaultScopes     = []string{"openid", "profile", "email", "offline_access"}
	defaultDisclosure = client.Disclosure{Email: true, Roles: true}
)

func disclosureObject(d client.Disclosure) types.Object {
	return types.ObjectValueMust(disclosureAttrTypes, map[string]attr.Value{"email": types.BoolValue(d.Email),
		"groups": types.BoolValue(d.Groups), "roles": types.BoolValue(d.Roles), "entitlements": types.BoolValue(d.Entitlements)})
}

func (r *applicationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application"
}

func stringSetAttr(desc string, def []string, v ...validator.Set) schema.SetAttribute {
	return schema.SetAttribute{MarkdownDescription: desc, ElementType: types.StringType, Optional: true, Computed: true,
		Default: setdefault.StaticValue(setOf(def)), Validators: v}
}

func ttlAttr(desc string, def, lo, hi int64) schema.Int64Attribute {
	return schema.Int64Attribute{MarkdownDescription: desc, Optional: true, Computed: true, Default: int64default.StaticInt64(def),
		Validators: []validator.Int64{int64validator.Between(lo, hi)}}
}

func (r *applicationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	day := int64(24 * 3600)
	resp.Schema = schema.Schema{
		MarkdownDescription: "An application of a tenant: an OpenID Connect client (`kind = \"oidc\"`) or a forward-auth protected " +
			"web app (`kind = \"forward_auth\"`). Needs `app:manage` in the tenant. The client secret of confidential OIDC clients is " +
			"returned by Porch only once, at creation; it is kept in state (sensitive) and never re-read. Change `secret_version` to " +
			"rotate it. Import with `<tenant>/<application_id>` (the secret is then unknown until rotated).",
		Attributes: map[string]schema.Attribute{
			"id":     idAttribute("Application ID (`app_…`)."),
			"tenant": tenantAttribute(),
			"slug":   requiredImmutableString("Identifier, unique in the tenant.", slugValidators()...),
			"name": schema.StringAttribute{MarkdownDescription: "Display name (shown in the launcher).", Required: true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 200)}},
			"kind": requiredImmutableString("`oidc` or `forward_auth`.", stringvalidator.OneOf("oidc", "forward_auth")),
			"description": schema.StringAttribute{MarkdownDescription: "Description.", Optional: true, Computed: true,
				Default: stringdefault.StaticString(""), Validators: []validator.String{stringvalidator.LengthAtMost(2000)}},
			"launch_url": schema.StringAttribute{MarkdownDescription: "URL the launcher opens (http/https).", Optional: true, Computed: true,
				Default: stringdefault.StaticString(""), Validators: []validator.String{stringvalidator.LengthAtMost(2048)}},
			"enabled": schema.BoolAttribute{MarkdownDescription: "Whether the application accepts sign-ins. Default `true`.", Optional: true,
				Computed: true, Default: booldefault.StaticBool(true)},
			"group": schema.StringAttribute{MarkdownDescription: "Launcher section the application appears in (e.g. `Media`). Default: none.",
				Optional: true, Computed: true, Default: stringdefault.StaticString(""), Validators: []validator.String{stringvalidator.LengthAtMost(64)}},
			"icon_source": schema.StringAttribute{
				MarkdownDescription: "Where Porch takes the launcher icon from: `thesvg` (a [theSVG](https://thesvg.org) brand logo, `slug` or " +
					"`slug/variant`, e.g. `github/mono`), `dashboard_icons` (a name from the [dashboard-icons](https://dashboardicons.com) set, e.g. " +
					"`jellyfin`) or `url` (an https image). Porch downloads it, sanitizes SVGs and re-encodes other formats. " +
					"`\"\"` removes the icon. Unset: whatever the application has (an icon uploaded in the admin UI shows as `upload`).",
				Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators: []validator.String{stringvalidator.OneOf("", "thesvg", "dashboard_icons", "url")}},
			"icon_ref": schema.StringAttribute{MarkdownDescription: "The dashboard-icons name or image URL for `icon_source`.",
				Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators: []validator.String{stringvalidator.LengthAtMost(512)}},
			"access_mode": schema.StringAttribute{
				MarkdownDescription: "`all_members` (default: every active member of the tenant and of tenants it is shared with) or " +
					"`assigned` (explicit `app:access` grants only).",
				Optional: true, Computed: true, Default: stringdefault.StaticString("all_members"),
				Validators: []validator.String{stringvalidator.OneOf("all_members", "assigned")},
			},
			"disclosure": schema.SingleNestedAttribute{
				MarkdownDescription: "Optional claims released to the application.",
				Optional:            true, Computed: true, Default: objectdefault.StaticValue(disclosureObject(defaultDisclosure)),
				Attributes: map[string]schema.Attribute{
					"email": schema.BoolAttribute{MarkdownDescription: "Release the email (default `true`).", Optional: true, Computed: true,
						Default: booldefault.StaticBool(true)},
					"groups": schema.BoolAttribute{MarkdownDescription: "Release groups (default `false`).", Optional: true, Computed: true,
						Default: booldefault.StaticBool(false)},
					"roles": schema.BoolAttribute{MarkdownDescription: "Release roles (default `true`).", Optional: true, Computed: true,
						Default: booldefault.StaticBool(true)},
					"entitlements": schema.BoolAttribute{MarkdownDescription: "Release entitlements (default `false`).", Optional: true,
						Computed: true, Default: booldefault.StaticBool(false)},
				},
			},
			"hosts": schema.SetAttribute{
				MarkdownDescription: "Hosts protected by a `forward_auth` application (at least one; lowercase). Must be empty for `oidc`.",
				ElementType:         types.StringType, Optional: true, Computed: true, Default: setdefault.StaticValue(setOf(nil)),
				Validators: []validator.Set{setvalidator.SizeAtMost(20), setvalidator.ValueStringsAre(lowercaseValidator())},
			},
			"forward_headers": schema.MapAttribute{
				MarkdownDescription: "Identity headers sent to forward-auth backends (header name → `sub`, `email`, `name`, `groups`, " +
					"`tenant` or `roles`); empty uses Porch's defaults.",
				ElementType: types.StringType, Optional: true, Computed: true, Default: mapdefault.StaticValue(mapOf(nil)),
			},
			"client": schema.SingleNestedAttribute{
				MarkdownDescription: "OIDC client configuration; required for `kind = \"oidc\"`, not allowed for `forward_auth`. " +
					"Updates replace the whole client configuration.",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"client_type": schema.StringAttribute{MarkdownDescription: "`confidential` (default) or `public`. Changing it forces a new resource.",
						Optional: true, Computed: true, Default: stringdefault.StaticString("confidential"),
						PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
						Validators:    []validator.String{stringvalidator.OneOf("confidential", "public")}},
					"application_type": schema.StringAttribute{MarkdownDescription: "`web` (default), `native` or `user_agent`. Changing it forces a new resource.",
						Optional: true, Computed: true, Default: stringdefault.StaticString("web"),
						PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
						Validators:    []validator.String{stringvalidator.OneOf("web", "native", "user_agent")}},
					"redirect_uris":             stringSetAttr("Exact redirect URIs (https, or http on loopback).", nil, setvalidator.SizeAtMost(20)),
					"post_logout_redirect_uris": stringSetAttr("Post-logout redirect URIs.", nil, setvalidator.SizeAtMost(20)),
					"grant_types": stringSetAttr("`authorization_code` (default), `refresh_token`, `client_credentials`.", defaultGrantTypes,
						setvalidator.SizeBetween(1, 3), setvalidator.ValueStringsAre(stringvalidator.OneOf("authorization_code", "refresh_token", "client_credentials"))),
					"allowed_scopes": stringSetAttr("Scopes the client may request (default `openid profile email offline_access`).", defaultScopes,
						setvalidator.SizeBetween(1, 10), setvalidator.ValueStringsAre(stringvalidator.OneOf("openid", "profile", "email", "offline_access", "groups"))),
					"audiences":       stringSetAttr("API audiences access tokens may be issued for (RFC 8707).", nil, setvalidator.SizeAtMost(20)),
					"allowed_origins": stringSetAttr("Browser origins allowed to call the token endpoint (SPA clients).", nil, setvalidator.SizeAtMost(20)),
					"require_pkce": schema.BoolAttribute{MarkdownDescription: "Require PKCE (default `true`; always true for public clients).",
						Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
					"signing_alg": schema.StringAttribute{MarkdownDescription: "ID token signing algorithm: `ES256` (default) or `RS256`.",
						Optional: true, Computed: true, Default: stringdefault.StaticString("ES256"),
						Validators: []validator.String{stringvalidator.OneOf("ES256", "RS256")}},
					"access_token_ttl_seconds": ttlAttr("Access token lifetime (60..3600, default 600).", 600, 60, 3600),
					"id_token_ttl_seconds":     ttlAttr("ID token lifetime (60..3600, default 600).", 600, 60, 3600),
					"refresh_ttl_seconds":      ttlAttr("Refresh token lifetime (3600..31536000, default 2592000).", 30*day, 3600, 365*day),
					"refresh_idle_seconds":     ttlAttr("Refresh token idle timeout (3600..refresh_ttl_seconds, default 1209600).", 14*day, 3600, 365*day),
				},
			},
			"client_id": computedString("OAuth client ID (`oidc` only)."),
			"client_secret": schema.StringAttribute{
				MarkdownDescription: "Client secret of a confidential OIDC client. Only known after create (or rotation); never re-read.",
				Computed:            true, Sensitive: true,
				PlanModifiers: []planmodifier.String{secretPlanModifier{}},
			},
			"secret_version": schema.StringAttribute{
				MarkdownDescription: "Arbitrary value; changing it (including setting it for the first time, e.g. after an import) rotates the client secret and stores the new one. The old secret stops working immediately.",
				Optional:            true,
			},
			"issuer":        computedString("OIDC issuer (`oidc` only)."),
			"discovery_url": computedString("OIDC discovery document URL (`oidc` only)."),
		},
	}
}

func (r *applicationResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg applicationModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.Kind.IsUnknown() || cfg.Kind.IsNull() {
		return
	}
	switch cfg.Kind.ValueString() {
	case "oidc":
		if cfg.Client.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("client"), "Missing client", "`client` is required for oidc applications.")
		}
		if !cfg.Hosts.IsNull() && !cfg.Hosts.IsUnknown() && len(cfg.Hosts.Elements()) > 0 {
			resp.Diagnostics.AddAttributeError(path.Root("hosts"), "Invalid hosts", "Only forward_auth applications have hosts.")
		}
	case "forward_auth":
		if !cfg.Client.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("client"), "Invalid client", "forward_auth applications have no OIDC client.")
		}
		if cfg.Hosts.IsNull() || (!cfg.Hosts.IsUnknown() && len(cfg.Hosts.Elements()) == 0) {
			resp.Diagnostics.AddAttributeError(path.Root("hosts"), "Missing hosts", "forward_auth applications protect at least one host.")
		}
	}
}

func clientConfigFrom(ctx context.Context, obj types.Object, diags *diag.Diagnostics) *client.ClientConfig {
	if obj.IsNull() || obj.IsUnknown() {
		return nil
	}
	var m oidcClientModel
	diags.Append(obj.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return nil
	}
	return &client.ClientConfig{ClientType: m.ClientType.ValueString(), ApplicationType: m.ApplicationType.ValueString(),
		RedirectURIs: stringsOf(ctx, m.RedirectURIs, diags), PostLogoutURIs: stringsOf(ctx, m.PostLogoutURIs, diags),
		GrantTypes: stringsOf(ctx, m.GrantTypes, diags), AllowedScopes: stringsOf(ctx, m.AllowedScopes, diags),
		Audiences: stringsOf(ctx, m.Audiences, diags), AllowedOrigins: stringsOf(ctx, m.AllowedOrigins, diags),
		RequirePKCE: ptr(m.RequirePKCE.ValueBool()), SigningAlg: m.SigningAlg.ValueString(), AccessTokenTTL: m.AccessTokenTTL.ValueInt64(),
		IDTokenTTL: m.IDTokenTTL.ValueInt64(), RefreshTTL: m.RefreshTTL.ValueInt64(), RefreshIdle: m.RefreshIdle.ValueInt64()}
}

func disclosureFrom(ctx context.Context, obj types.Object, diags *diag.Diagnostics) *client.Disclosure {
	if obj.IsNull() || obj.IsUnknown() {
		return nil
	}
	var m disclosureModel
	diags.Append(obj.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	return &client.Disclosure{Email: m.Email.ValueBool(), Groups: m.Groups.ValueBool(), Roles: m.Roles.ValueBool(), Entitlements: m.Entitlements.ValueBool()}
}

// fill copies the API view into the model. The client secret and secret_version are never touched.
func (m *applicationModel) fill(a *client.Application) {
	m.ID = types.StringValue(a.ID)
	m.Slug = types.StringValue(a.Slug)
	m.Name = types.StringValue(a.Name)
	m.Kind = types.StringValue(a.Kind)
	m.Description = types.StringValue(a.Description)
	m.LaunchURL = types.StringValue(a.LaunchURL)
	m.Enabled = types.BoolValue(a.Enabled)
	m.AccessMode = types.StringValue(a.AccessMode)
	m.Disclosure = disclosureObject(a.Disclosure)
	m.Hosts = setOf(a.Hosts)
	m.ForwardHeaders = mapOf(a.ForwardHeaders)
	m.Group = types.StringValue(a.Group)
	m.IconSource = types.StringValue(a.IconSource)
	m.IconRef = types.StringValue(a.IconRef)
	if c := a.Client; c != nil {
		m.Client = types.ObjectValueMust(oidcClientAttrTypes, map[string]attr.Value{
			"client_type": types.StringValue(c.ClientType), "application_type": types.StringValue(c.ApplicationType),
			"redirect_uris": setOf(c.RedirectURIs), "post_logout_redirect_uris": setOf(c.PostLogoutURIs), "grant_types": setOf(c.GrantTypes),
			"allowed_scopes": setOf(c.AllowedScopes), "audiences": setOf(c.Audiences), "allowed_origins": setOf(c.AllowedOrigins),
			"require_pkce": types.BoolValue(c.RequirePKCE), "signing_alg": types.StringValue(c.SigningAlg),
			"access_token_ttl_seconds": types.Int64Value(c.AccessTokenTTL), "id_token_ttl_seconds": types.Int64Value(c.IDTokenTTL),
			"refresh_ttl_seconds": types.Int64Value(c.RefreshTTL), "refresh_idle_seconds": types.Int64Value(c.RefreshIdle),
		})
		m.ClientID = types.StringValue(c.ClientID)
		m.Issuer = types.StringValue(c.Issuer)
		m.DiscoveryURL = types.StringValue(c.DiscoveryURL)
	} else {
		m.Client = types.ObjectNull(oidcClientAttrTypes)
		m.ClientID = types.StringNull()
		m.Issuer = types.StringNull()
		m.DiscoveryURL = types.StringNull()
	}
	if m.ClientSecret.IsUnknown() {
		m.ClientSecret = types.StringNull()
	}
}

func (r *applicationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan applicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tenant := plan.Tenant.ValueString()
	in := client.ApplicationCreate{Slug: plan.Slug.ValueString(), Name: plan.Name.ValueString(), Kind: plan.Kind.ValueString(),
		Description: plan.Description.ValueString(), LaunchURL: plan.LaunchURL.ValueString(), AccessMode: plan.AccessMode.ValueString(),
		Disclosure: disclosureFrom(ctx, plan.Disclosure, &resp.Diagnostics), Client: clientConfigFrom(ctx, plan.Client, &resp.Diagnostics),
		ForwardHeaders: stringMapOf(ctx, plan.ForwardHeaders, &resp.Diagnostics), Group: plan.Group.ValueString()}
	if !plan.IconSource.IsUnknown() && !plan.IconSource.IsNull() && plan.IconSource.ValueString() != "" {
		in.Icon = &client.Icon{Source: plan.IconSource.ValueString(), Ref: plan.IconRef.ValueString()}
	}
	if plan.Kind.ValueString() == "forward_auth" {
		in.Hosts = stringsOf(ctx, plan.Hosts, &resp.Diagnostics)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	a, created, err := r.client.CreateApplication(ctx, tenant, in)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot create application", err)
		return
	}
	plan.ClientSecret = optString(a.ClientSecret)
	saveState := func(app *client.Application) {
		plan.fill(app)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.Append(saveETag(ctx, resp.Private, app.ETag)...)
	}
	if created.SecretWithheld && a.Client != nil && a.Client.ClientType == "confidential" {
		// The create succeeded on an attempt whose response was lost; Porch never returns a secret twice, so issue a new one.
		s, err := r.client.RotateApplicationSecret(ctx, tenant, a.ID)
		if err != nil {
			saveState(a)
			apiError(&resp.Diagnostics, "Application created but its client secret was lost and could not be rotated", err)
			return
		}
		plan.ClientSecret = types.StringValue(s.ClientSecret)
		resp.Diagnostics.AddWarning("Client secret rotated", "The application was created by an earlier attempt whose response was lost; "+
			"a new client secret was issued.")
	}
	// Re-read: the create response does not include forward-auth hosts, and the version may have moved.
	cur, err := r.client.GetApplication(ctx, tenant, a.ID)
	if err != nil {
		saveState(a)
		apiError(&resp.Diagnostics, "Application created but could not be read back", err)
		return
	}
	if !plan.Enabled.ValueBool() && cur.Enabled {
		cur, err = r.client.UpdateApplication(ctx, tenant, a.ID, cur.ETag, client.ApplicationUpdate{Enabled: ptr(false)})
		if err != nil {
			saveState(a)
			apiError(&resp.Diagnostics, "Application created but could not be disabled", err)
			return
		}
	}
	saveState(cur)
}

func (r *applicationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state applicationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	a, err := r.client.GetApplication(ctx, state.Tenant.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read application", err)
		return
	}
	state.fill(a)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, a.ETag)...)
}

func (r *applicationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state applicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tenant, id := state.Tenant.ValueString(), state.ID.ValueString()
	in := client.ApplicationUpdate{Name: ptr(plan.Name.ValueString()), Description: ptr(plan.Description.ValueString()),
		LaunchURL: ptr(plan.LaunchURL.ValueString()), Enabled: ptr(plan.Enabled.ValueBool()), AccessMode: ptr(plan.AccessMode.ValueString()),
		Disclosure: disclosureFrom(ctx, plan.Disclosure, &resp.Diagnostics)}
	if !plan.Client.Equal(state.Client) {
		in.Client = clientConfigFrom(ctx, plan.Client, &resp.Diagnostics)
		if in.Client != nil {
			in.Client.ClientType, in.Client.ApplicationType = "", "" // create-only
		}
	}
	if !plan.Hosts.Equal(state.Hosts) && plan.Kind.ValueString() == "forward_auth" {
		in.Hosts = ptr(stringsOf(ctx, plan.Hosts, &resp.Diagnostics))
	}
	if !plan.ForwardHeaders.Equal(state.ForwardHeaders) {
		in.ForwardHeaders = ptr(stringMapOf(ctx, plan.ForwardHeaders, &resp.Diagnostics))
	}
	if !plan.Group.Equal(state.Group) {
		in.Group = ptr(plan.Group.ValueString())
	}
	// Icons are sent only when they change, so an apply never makes Porch download them again.
	if !plan.IconSource.IsUnknown() && (!plan.IconSource.Equal(state.IconSource) || !plan.IconRef.Equal(state.IconRef)) {
		in.Icon = &client.Icon{Source: plan.IconSource.ValueString(), Ref: plan.IconRef.ValueString()}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	a, err := r.client.UpdateApplication(ctx, tenant, id, loadETag(ctx, req.Private), in)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot update application", err)
		return
	}
	plan.ClientSecret = state.ClientSecret
	if !plan.SecretVersion.Equal(state.SecretVersion) && a.Client != nil && a.Client.ClientType == "confidential" {
		s, err := r.client.RotateApplicationSecret(ctx, tenant, id)
		if err != nil {
			plan.SecretVersion = state.SecretVersion
			plan.fill(a)
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			resp.Diagnostics.Append(saveETag(ctx, resp.Private, a.ETag)...)
			apiError(&resp.Diagnostics, "Cannot rotate the client secret", err)
			return
		}
		plan.ClientSecret = types.StringValue(s.ClientSecret)
		if a, err = r.client.GetApplication(ctx, tenant, id); err != nil {
			apiError(&resp.Diagnostics, "Cannot read application after rotating its secret", err)
			return
		}
	}
	plan.fill(a)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, a.ETag)...)
}

func (r *applicationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state applicationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteApplication(ctx, state.Tenant.ValueString(), state.ID.ValueString(), loadETag(ctx, req.Private))
	if err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot delete application", err)
	}
}

func (r *applicationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importTenantScoped(ctx, req, resp)
}
