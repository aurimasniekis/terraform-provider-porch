package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

// privateState is the subset of the framework's private state API used to keep the ETag out of the visible schema.
type privateState interface {
	GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics)
	SetKey(ctx context.Context, key string, value []byte) diag.Diagnostics
}

const etagKey = "etag"

// saveETag stores the resource version (sent as If-Match on the next change) in private state.
func saveETag(ctx context.Context, p privateState, etag string) diag.Diagnostics {
	if p == nil || etag == "" {
		return nil
	}
	b, _ := json.Marshal(etag)
	return p.SetKey(ctx, etagKey, b)
}

// loadETag returns the stored ETag ("" when unknown, which makes the change unconditional).
func loadETag(ctx context.Context, p privateState) string {
	if p == nil {
		return ""
	}
	b, diags := p.GetKey(ctx, etagKey)
	if diags.HasError() || len(b) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(b, &s) != nil {
		return ""
	}
	return s
}

// configureClient extracts the API client from provider data.
func configureClient(data any, diags *diag.Diagnostics) *client.Client {
	if data == nil {
		return nil
	}
	c, ok := data.(*client.Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", data))
		return nil
	}
	return c
}

// baseResource carries the client for every resource.
type baseResource struct {
	client *client.Client
}

func (b *baseResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c := configureClient(req.ProviderData, &resp.Diagnostics); c != nil {
		b.client = c
	}
}

// baseDataSource carries the client for every data source.
type baseDataSource struct {
	client *client.Client
}

func (b *baseDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if c := configureClient(req.ProviderData, &resp.Diagnostics); c != nil {
		b.client = c
	}
}

// apiError adds an API error as a diagnostic. The message carries Porch's problem code, detail and field errors.
func apiError(diags *diag.Diagnostics, summary string, err error) {
	diags.AddError(summary, err.Error())
}

// splitID splits a composite import ID into exactly n non-empty parts separated by "/".
func splitID(id string, n int, format string) ([]string, error) {
	parts := strings.Split(id, "/")
	if len(parts) != n || slices.Contains(parts, "") {
		return nil, fmt.Errorf("expected an import ID of the form %q, got %q", format, id)
	}
	return parts, nil
}

// setOf converts strings to a types.Set (never null).
func setOf(ss []string) types.Set {
	vals := make([]attr.Value, 0, len(ss))
	for _, s := range ss {
		vals = append(vals, types.StringValue(s))
	}
	return types.SetValueMust(types.StringType, vals)
}

// stringsOf converts a set or list of strings ([] for null/unknown).
func stringsOf(ctx context.Context, v interface {
	IsNull() bool
	IsUnknown() bool
	ElementsAs(context.Context, any, bool) diag.Diagnostics
}, diags *diag.Diagnostics) []string {
	out := []string{}
	if v.IsNull() || v.IsUnknown() {
		return out
	}
	diags.Append(v.ElementsAs(ctx, &out, false)...)
	slices.Sort(out)
	return out
}

// mapOf converts a string map to types.Map (never null).
func mapOf(m map[string]string) types.Map {
	vals := make(map[string]attr.Value, len(m))
	for k, v := range m {
		vals[k] = types.StringValue(v)
	}
	return types.MapValueMust(types.StringType, vals)
}

// stringMapOf converts a types.Map ({} for null/unknown).
func stringMapOf(ctx context.Context, m types.Map, diags *diag.Diagnostics) map[string]string {
	out := map[string]string{}
	if m.IsNull() || m.IsUnknown() {
		return out
	}
	diags.Append(m.ElementsAs(ctx, &out, false)...)
	return out
}

// optString maps "" to null.
func optString(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

var (
	slugRE       = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	permissionRE = regexp.MustCompile(`^(\*|[a-z][a-z0-9_]{0,31}):(\*|[a-z][a-z0-9_]{0,31})$`)
	typeRE       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	lowerRE      = regexp.MustCompile(`^[^A-Z]*$`)
)

func slugValidators() []validator.String {
	return []validator.String{stringvalidator.RegexMatches(slugRE, "must be lowercase letters, digits or inner hyphens (1-63 characters)")}
}

func lowercaseValidator() validator.String {
	return stringvalidator.RegexMatches(lowerRE, "must be lowercase (Porch normalises it to lowercase)")
}

// Common schema attributes.

func idAttribute(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

func tenantAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "ID or slug of the tenant that owns this object. Changing it forces a new resource.",
		Required:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		Validators:          []validator.String{stringvalidator.LengthBetween(1, 64)},
	}
}

func requiredImmutableString(desc string, v ...validator.String) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc + " Changing it forces a new resource.",
		Required:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		Validators:          v,
	}
}

func computedString(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// secretPlanModifier keeps a write-once secret from state, except when secret_version changes (rotation), in which case
// the new secret is unknown until apply.
type secretPlanModifier struct{}

func (secretPlanModifier) Description(context.Context) string {
	return "Keeps the secret from state unless secret_version changes."
}

func (m secretPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (secretPlanModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return // create (unknown until apply) or destroy
	}
	var planV, stateV types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("secret_version"), &planV)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("secret_version"), &stateV)...)
	if planV.IsUnknown() || !planV.Equal(stateV) {
		return // rotation
	}
	resp.PlanValue = req.StateValue
}
