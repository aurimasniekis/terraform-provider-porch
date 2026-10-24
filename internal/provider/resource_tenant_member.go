package provider

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

var _ resource.ResourceWithImportState = (*tenantMemberResource)(nil)

type tenantMemberResource struct{ baseResource }

// NewTenantMemberResource returns the porch_tenant_member resource.
func NewTenantMemberResource() resource.Resource { return &tenantMemberResource{} }

type tenantMemberModel struct {
	ID     types.String `tfsdk:"id"`
	Tenant types.String `tfsdk:"tenant"`
	UserID types.String `tfsdk:"user_id"`
	Status types.String `tfsdk:"status"`
	Source types.String `tfsdk:"source"`
}

func (r *tenantMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tenant_member"
}

func (r *tenantMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Makes a user a member of a tenant (needs `member:manage` in the tenant). Import with `<tenant>/<membership_id>`.",
		Attributes: map[string]schema.Attribute{
			"id":      idAttribute("Membership ID (`mem_…`)."),
			"tenant":  tenantAttribute(),
			"user_id": requiredImmutableString("ID of the user."),
			"status": schema.StringAttribute{
				MarkdownDescription: "`active` (default) or `suspended`.",
				Optional:            true, Computed: true, Default: stringdefault.StaticString("active"),
				Validators: []validator.String{stringvalidator.OneOf("active", "suspended")},
			},
			"source": computedString("How the membership was created (`terraform`, `manual`, `domain_rule:<id>`, …)."),
		},
	}
}

func (m *tenantMemberModel) fill(x *client.Member) {
	m.ID = types.StringValue(x.ID)
	m.UserID = types.StringValue(x.UserID)
	m.Status = types.StringValue(x.Status)
	m.Source = types.StringValue(x.Source)
}

func (r *tenantMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan tenantMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.client.AddMember(ctx, plan.Tenant.ValueString(), plan.UserID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot add tenant member", err)
		return
	}
	if plan.Status.ValueString() != m.Status {
		m2, err := r.client.SetMemberStatus(ctx, plan.Tenant.ValueString(), m.ID, m.ETag, plan.Status.ValueString())
		if err != nil {
			plan.fill(m)
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			resp.Diagnostics.Append(saveETag(ctx, resp.Private, m.ETag)...)
			apiError(&resp.Diagnostics, "Member added but the status could not be set", err)
			return
		}
		m = m2
	}
	plan.fill(m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, m.ETag)...)
}

func (r *tenantMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state tenantMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.client.GetMember(ctx, state.Tenant.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read tenant member", err)
		return
	}
	state.fill(m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, m.ETag)...)
}

func (r *tenantMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state tenantMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.client.SetMemberStatus(ctx, state.Tenant.ValueString(), state.ID.ValueString(), loadETag(ctx, req.Private), plan.Status.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot update tenant member", err)
		return
	}
	plan.fill(m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(saveETag(ctx, resp.Private, m.ETag)...)
}

func (r *tenantMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state tenantMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.RemoveMember(ctx, state.Tenant.ValueString(), state.ID.ValueString(), loadETag(ctx, req.Private))
	if err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot remove tenant member", err)
	}
}

func (r *tenantMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importTenantScoped(ctx, req, resp)
}

// porch_invite

var _ resource.ResourceWithImportState = (*inviteResource)(nil)

type inviteResource struct{ baseResource }

// NewInviteResource returns the porch_invite resource.
func NewInviteResource() resource.Resource { return &inviteResource{} }

type inviteModel struct {
	ID           types.String `tfsdk:"id"`
	Tenant       types.String `tfsdk:"tenant"`
	Email        types.String `tfsdk:"email"`
	ValidSeconds types.Int64  `tfsdk:"valid_seconds"`
	ExpiresAt    types.String `tfsdk:"expires_at"`
	Consumed     types.Bool   `tfsdk:"consumed"`
}

func (r *inviteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_invite"
}

func (r *inviteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Invites an email address to a tenant (needs `invite:manage`). The invitation is accepted when someone signs in " +
			"with a matching verified upstream email. Invitations cannot be edited; changes replace them. Import with `<tenant>/<invite_id>`.",
		Attributes: map[string]schema.Attribute{
			"id":     idAttribute("Invitation ID."),
			"tenant": tenantAttribute(),
			"email":  requiredImmutableString("Invited email address (lowercase).", lowercaseValidator(), stringvalidator.LengthBetween(3, 254)),
			"valid_seconds": schema.Int64Attribute{
				MarkdownDescription: "Validity in seconds (up to 30 days). Defaults to 7 days. Only used at creation and not read back " +
					"from Porch; changing it forces a new invitation (setting it after an import does not).",
				Optional: true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplaceIf(func(_ context.Context, req planmodifier.Int64Request,
					resp *int64planmodifier.RequiresReplaceIfFuncResponse) {
					resp.RequiresReplace = !req.StateValue.IsNull()
				}, "Changing valid_seconds forces a new invitation.", "Changing `valid_seconds` forces a new invitation.")},
				Validators: []validator.Int64{int64validator.Between(1, 30*24*3600)},
			},
			"expires_at": computedString("Expiry time (RFC 3339)."),
			"consumed":   schema.BoolAttribute{MarkdownDescription: "Whether the invitation has been accepted.", Computed: true},
		},
	}
}

func (m *inviteModel) fill(i *client.Invite) {
	m.ID = types.StringValue(i.ID)
	m.Email = types.StringValue(i.Email)
	m.ExpiresAt = types.StringValue(i.ExpiresAt.UTC().Format(time.RFC3339))
	m.Consumed = types.BoolValue(i.ConsumedAt != nil)
}

func (r *inviteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan inviteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	i, err := r.client.CreateInvite(ctx, plan.Tenant.ValueString(), client.InviteCreate{Email: plan.Email.ValueString(),
		ValidSeconds: plan.ValidSeconds.ValueInt64()})
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot create invitation", err)
		return
	}
	plan.fill(i)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *inviteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state inviteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	i, err := r.client.GetInvite(ctx, state.Tenant.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read invitation", err)
		return
	}
	state.fill(i)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only records valid_seconds after an import (every other change replaces the invitation).
func (r *inviteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state inviteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.ValidSeconds = plan.ValidSeconds
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *inviteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state inviteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteInvite(ctx, state.Tenant.ValueString(), state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot delete invitation", err)
	}
}

func (r *inviteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importTenantScoped(ctx, req, resp)
}
