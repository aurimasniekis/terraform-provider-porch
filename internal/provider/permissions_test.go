package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// errRE matches the given fragments in order, tolerating the line wrapping Terraform applies to diagnostics.
func errRE(fragments ...string) *regexp.Regexp {
	parts := make([]string, 0, len(fragments))
	for _, f := range fragments {
		parts = append(parts, strings.Join(strings.Fields(regexp.QuoteMeta(f)), `\s+`))
	}
	return regexp.MustCompile(strings.Join(parts, `[\s\S]*`))
}

func providerWithToken(token string) string {
	return fmt.Sprintf(`
provider "porch" {
  token = %q
}
`, token)
}

// Permission errors surface Porch's problem documents (status, code, detail, field errors).
func TestAccPermissionErrors(t *testing.T) {
	testAccPreCheck(t)
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// An auditor token is read-only: creating a tenant needs the owner or admin installation role.
				Config:      providerWithToken(acc.AuditorToken) + tenantConfig(randSlug("aud")),
				ExpectError: errRE("Cannot create tenant", "403 forbidden: installation role required: owner or admin"),
			},
			{
				// A tenant token holding only group:manage cannot see roles: Porch answers 404 (indistinguishable from
				// a missing tenant, ADR-0038).
				Config: providerWithToken(acc.LimitedToken) + fmt.Sprintf(`
resource "porch_role" "r" {
  tenant      = %q
  slug        = "nope"
  name        = "Nope"
  permissions = ["app:access"]
}
`, acc.LimitedTenant),
				ExpectError: errRE("Cannot create role", "404 not_found: tenant not found"),
			},
			{
				// ...but it can manage groups in its tenant.
				Config: providerWithToken(acc.LimitedToken) + fmt.Sprintf(`
resource "porch_group" "g" {
  tenant = %q
  slug   = %q
  name   = "Managed by a limited token"
}
`, acc.LimitedTenant, randSlug("lim")),
				Check: resource.TestMatchResourceAttr("porch_group.g", "id", idRE("grp")),
			},
		},
	})
}

// A delegated administrator (a tenant member holding role:manage, not an installation operator) may only define
// roles with permissions they hold: anything else fails with 403 privilege_escalation listing what is missing.
func TestAccPrivilegeEscalation(t *testing.T) {
	testAccPreCheck(t)
	if acc != nil && acc.DelegatedToken == "" {
		t.Skip("fake IdP not available (build bin/fakegoogle); delegated administrator could not be set up")
	}
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerWithToken(acc.DelegatedToken) + fmt.Sprintf(`
resource "porch_role" "escalate" {
  tenant      = %q
  slug        = %q
  name        = "Escalation attempt"
  permissions = ["app:manage", "role:manage"]
}
`, acc.DelegatedTenant, randSlug("esc")),
				ExpectError: errRE("403 privilege_escalation: you cannot grant permissions you do not hold: app:manage", "permissions: not held: app:manage"),
			},
			{
				// Granting the admin role (which holds *:*) to herself is an escalation too.
				Config: providerWithToken(acc.DelegatedToken) + fmt.Sprintf(`
data "porch_role" "admin" {
  tenant = %q
  slug   = "admin"
}

resource "porch_role_assignment" "self_admin" {
  tenant       = %q
  subject_type = "user"
  subject_id   = %q
  role_id      = data.porch_role.admin.id
}
`, acc.DelegatedTenant, acc.DelegatedTenant, acc.DelegatedUserID),
				ExpectError: errRE("Cannot create role assignment", "403 privilege_escalation"),
			},
			{
				// What she holds she may delegate.
				Config: providerWithToken(acc.DelegatedToken) + fmt.Sprintf(`
resource "porch_role" "ok" {
  tenant      = %q
  slug        = %q
  name        = "Within her grant"
  permissions = ["role:manage"]
}
`, acc.DelegatedTenant, randSlug("ok")),
				Check: resource.TestCheckResourceAttr("porch_role.ok", "permissions.#", "1"),
			},
		},
	})
}
