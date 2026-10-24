package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

var (
	_ resource.ResourceWithImportState    = (*roleAssignmentResource)(nil)
	_ resource.ResourceWithValidateConfig = (*roleAssignmentResource)(nil)
)

type roleAssignmentResource struct{ baseResource }

// NewRoleAssignmentResource returns the porch_role_assignment resource.
func NewRoleAssignmentResource() resource.Resource { return &roleAssignmentResource{} }

type roleAssignmentModel struct {
	ID                types.String `tfsdk:"id"`
	Tenant            types.String `tfsdk:"tenant"`
	SubjectType       types.String `tfsdk:"subject_type"`
	SubjectID         types.String `tfsdk:"subject_id"`
	RoleID            types.String `tfsdk:"role_id"`
	ScopeType         types.String `tfsdk:"scope_type"`
	ScopeResourceType types.String `tfsdk:"scope_resource_type"`
	ScopeResourceID   types.String `tfsdk:"scope_resource_id"`
	ConditionPolicyID types.String `tfsdk:"condition_policy_id"`
	ExpiresAt         types.String `tfsdk:"expires_at"`
	Source            types.String `tfsdk:"source"`
}

func (r *roleAssignmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role_assignment"
}

type rfc3339Validator struct{}

func (rfc3339Validator) Description(context.Context) string { return "must be an RFC 3339 timestamp" }
func (v rfc3339Validator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (rfc3339Validator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := time.Parse(time.RFC3339, req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid timestamp", fmt.Sprintf("%q is not an RFC 3339 timestamp: %v", req.ConfigValue.ValueString(), err))
	}
}

func optionalImmutableString(desc string, v ...validator.String) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc + " Changing it forces a new resource.", Optional: true,
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Validators: v}
}

func (r *roleAssignmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Grants a role to a user, group or service account at tenant, resource-type or resource scope, optionally " +
			"under a condition policy and with an expiry. Needs `role:manage`; delegated administrators may only grant roles whose " +
			"permissions they hold. Assignments are immutable: every change replaces them. Import with `<tenant>/<assignment_id>`.",
		Attributes: map[string]schema.Attribute{
			"id":     idAttribute("Assignment ID (`asg_…`)."),
			"tenant": tenantAttribute(),
			"subject_type": requiredImmutableString("`user`, `group` or `service_account`.",
				stringvalidator.OneOf("user", "group", "service_account")),
			"subject_id": requiredImmutableString("ID of the user (a member of the tenant), group or service account."),
			"role_id":    requiredImmutableString("ID (or slug) of the role."),
			"scope_type": schema.StringAttribute{
				MarkdownDescription: "`tenant` (default), `resource_type` or `resource`. Changing it forces a new resource.",
				Optional:            true, Computed: true, Default: stringdefault.StaticString("tenant"),
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.OneOf("tenant", "resource_type", "resource")},
			},
			"scope_resource_type": optionalImmutableString("Resource type for `resource_type` and `resource` scopes (`app` for applications).",
				stringvalidator.RegexMatches(typeRE, "must be a lowercase identifier")),
			"scope_resource_id": optionalImmutableString("Resource (or application) ID for `resource` scope."),
			"condition_policy_id": optionalImmutableString("ID of a `condition` policy that must evaluate to true for the grant to apply.",
				idValidator("pol")),
			"expires_at": optionalImmutableString("Expiry time (RFC 3339, in the future).", rfc3339Validator{}),
			"source":     computedString("Origin of the assignment (`manual`, …)."),
		},
	}
}

func (r *roleAssignmentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg roleAssignmentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.ScopeType.IsUnknown() {
		return
	}
	scope := cfg.ScopeType.ValueString()
	if cfg.ScopeType.IsNull() {
		scope = "tenant"
	}
	switch scope {
	case "tenant":
		if !cfg.ScopeResourceType.IsNull() || !cfg.ScopeResourceID.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("scope_type"), "Inconsistent scope",
				"scope_resource_type and scope_resource_id must not be set for tenant scope.")
		}
	case "resource_type":
		if cfg.ScopeResourceType.IsNull() || !cfg.ScopeResourceID.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("scope_type"), "Inconsistent scope",
				"resource_type scope needs scope_resource_type and no scope_resource_id.")
		}
	case "resource":
		if cfg.ScopeResourceType.IsNull() || cfg.ScopeResourceID.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("scope_type"), "Inconsistent scope",
				"resource scope needs scope_resource_type and scope_resource_id.")
		}
	}
}

func (r *roleAssignmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan roleAssignmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := client.AssignmentCreate{SubjectType: plan.SubjectType.ValueString(), SubjectID: plan.SubjectID.ValueString(),
		RoleID: plan.RoleID.ValueString(), ScopeType: plan.ScopeType.ValueString(), ScopeResourceType: plan.ScopeResourceType.ValueString(),
		ScopeResourceID: plan.ScopeResourceID.ValueString(), ConditionPolicyID: plan.ConditionPolicyID.ValueString()}
	if !plan.ExpiresAt.IsNull() {
		t, err := time.Parse(time.RFC3339, plan.ExpiresAt.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("expires_at"), "Invalid timestamp", err.Error())
			return
		}
		in.ExpiresAt = &t
	}
	a, err := r.client.CreateAssignment(ctx, plan.Tenant.ValueString(), in)
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot create role assignment", err)
		return
	}
	plan.ID = types.StringValue(a.ID)
	plan.Source = types.StringValue(a.Source)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *roleAssignmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state roleAssignmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tenant := state.Tenant.ValueString()
	a, err := r.client.GetAssignment(ctx, tenant, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "Cannot read role assignment", err)
		return
	}
	state.ID = types.StringValue(a.ID)
	state.SubjectType = types.StringValue(a.SubjectType)
	state.SubjectID = types.StringValue(a.SubjectID)
	state.ScopeType = types.StringValue(a.ScopeType)
	state.ScopeResourceType = optString(a.ScopeResourceType)
	state.ScopeResourceID = optString(a.ScopeResourceID)
	state.ConditionPolicyID = optString(a.ConditionPolicyID)
	state.Source = types.StringValue(a.Source)
	// role_id may have been configured as a slug: keep it when it still names the same role.
	if cur := state.RoleID.ValueString(); cur != a.RoleID {
		keep := false
		if cur != "" && !strings.HasPrefix(cur, "rol_") {
			if ro, err := r.client.GetRole(ctx, tenant, cur); err == nil && ro.ID == a.RoleID {
				keep = true
			}
		}
		if !keep {
			state.RoleID = types.StringValue(a.RoleID)
		}
	}
	// expires_at: keep the configured spelling when it denotes the same instant.
	if a.ExpiresAt == nil {
		state.ExpiresAt = types.StringNull()
	} else {
		cur, err := time.Parse(time.RFC3339, state.ExpiresAt.ValueString())
		if state.ExpiresAt.IsNull() || err != nil || !cur.Equal(*a.ExpiresAt) {
			state.ExpiresAt = types.StringValue(a.ExpiresAt.UTC().Format(time.RFC3339Nano))
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *roleAssignmentResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Role assignments are immutable", "Every attribute forces replacement; this is a provider bug.")
}

func (r *roleAssignmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state roleAssignmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteAssignment(ctx, state.Tenant.ValueString(), state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "Cannot delete role assignment", err)
	}
}

func (r *roleAssignmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importTenantScoped(ctx, req, resp)
}
