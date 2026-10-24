package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

func TestAccGroups(t *testing.T) {
	testAccPreCheck(t)
	tslug := randSlug("grp")
	var tenantID, kidsID, familyID, userID string
	cfg := func(kidsName string) string {
		return tenantConfig(tslug) + fmt.Sprintf(`
resource "porch_user" "kid" {
  display_name = "Kid"
}

resource "porch_tenant_member" "kid" {
  tenant  = porch_tenant.t.id
  user_id = porch_user.kid.id
}

resource "porch_group" "family" {
  tenant = porch_tenant.t.id
  slug   = "family"
  name   = "Family"
}

resource "porch_group" "kids" {
  tenant      = porch_tenant.t.id
  slug        = "kids"
  name        = %q
  description = "Children of the family"
}

resource "porch_group_parent" "kids_in_family" {
  tenant          = porch_tenant.t.id
  group_id        = porch_group.kids.id
  parent_group_id = porch_group.family.id
}

resource "porch_group_member" "kid" {
  tenant   = porch_tenant.t.id
  group_id = porch_group.kids.id
  user_id  = porch_tenant_member.kid.user_id
}

data "porch_group" "kids" {
  tenant     = porch_tenant.t.id
  slug       = "kids"
  depends_on = [porch_group_member.kid, porch_group_parent.kids_in_family]
}
`, kidsName)
	}
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg("Kids"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("porch_group.kids", "id", idRE("grp")),
					resource.TestCheckResourceAttr("porch_group.family", "description", ""),
					resource.TestCheckResourceAttrPair("data.porch_group.kids", "id", "porch_group.kids", "id"),
					resource.TestCheckTypeSetElemAttrPair("data.porch_group.kids", "members.*", "porch_user.kid", "id"),
					resource.TestCheckTypeSetElemAttrPair("data.porch_group.kids", "parents.*", "porch_group.family", "id"),
					capture("porch_tenant.t", "id", &tenantID),
					capture("porch_group.kids", "id", &kidsID),
					capture("porch_group.family", "id", &familyID),
					capture("porch_user.kid", "id", &userID),
				),
			},
			{
				Config:           cfg("Children"),
				ConfigPlanChecks: expectUpdate("porch_group.kids"),
				Check:            resource.TestCheckResourceAttr("porch_group.kids", "name", "Children"),
			},
			{ResourceName: "porch_group.kids", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: importID("porch_group.kids", "tenant", "id")},
			{ResourceName: "porch_group_member.kid", ImportState: true, ImportStateVerify: true},
			{ResourceName: "porch_group_parent.kids_in_family", ImportState: true, ImportStateVerify: true},
			{
				PreConfig: oob(t, func(ctx context.Context, c *client.Client) error {
					if err := c.RemoveGroupMember(ctx, tenantID, kidsID, userID); err != nil {
						return err
					}
					return c.UnnestGroup(ctx, tenantID, kidsID, familyID)
				}),
				Config: cfg("Children"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: append(expectRecreate("porch_group_member.kid").PreApply,
					expectRecreate("porch_group_parent.kids_in_family").PreApply...)},
			},
			{
				// Deleting the group drops its memberships and nesting edges too.
				PreConfig: oob(t, func(ctx context.Context, c *client.Client) error { return c.DeleteGroup(ctx, tenantID, kidsID) }),
				Config:    cfg("Children"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: append(expectRecreate("porch_group.kids").PreApply,
					expectRecreate("porch_group_member.kid").PreApply...)},
			},
		},
	})
}

func TestAccRolePolicyResourceAssignment(t *testing.T) {
	testAccPreCheck(t)
	tslug := randSlug("rbac")
	var tenantID, roleID, policyID, resID, asgID string
	cfg := func(perms, expr, attrs string) string {
		return tenantConfig(tslug) + fmt.Sprintf(`
resource "porch_user" "kid" {
  display_name = "Kid"
}

resource "porch_tenant_member" "kid" {
  tenant  = porch_tenant.t.id
  user_id = porch_user.kid.id
}

resource "porch_group" "kids" {
  tenant = porch_tenant.t.id
  slug   = "kids"
  name   = "Kids"
}

resource "porch_role" "viewer" {
  tenant      = porch_tenant.t.id
  slug        = "media-viewer"
  name        = "Media viewer"
  permissions = %s
}

resource "porch_role" "power" {
  tenant      = porch_tenant.t.id
  slug        = "power"
  name        = "Power user"
  description = "Viewer plus downloads"
  permissions = ["media:download"]
  includes    = [porch_role.viewer.id]
}

resource "porch_policy" "daytime" {
  tenant     = porch_tenant.t.id
  slug       = "daytime"
  name       = "Daytime only"
  effect     = "condition"
  expression = %q
}

resource "porch_policy" "no_delete" {
  tenant     = porch_tenant.t.id
  slug       = "no-delete"
  name       = "Nobody deletes media"
  effect     = "deny"
  actions    = ["media:delete"]
  expression = "true"
}

resource "porch_resource" "library" {
  tenant     = porch_tenant.t.id
  type       = "library"
  slug       = "movies"
  name       = "Movies"
  attributes = %s
}

resource "porch_resource" "shelf" {
  tenant    = porch_tenant.t.id
  type      = "library"
  slug      = "cartoons"
  name      = "Cartoons"
  parent_id = porch_resource.library.id
}

resource "porch_role_assignment" "kid_daytime" {
  tenant              = porch_tenant.t.id
  subject_type        = "user"
  subject_id          = porch_tenant_member.kid.user_id
  role_id             = porch_role.viewer.id
  condition_policy_id = porch_policy.daytime.id
  expires_at          = "2099-01-01T00:00:00Z"
}

resource "porch_role_assignment" "kids_library" {
  tenant              = porch_tenant.t.id
  subject_type        = "group"
  subject_id          = porch_group.kids.id
  role_id             = porch_role.power.slug
  scope_type          = "resource"
  scope_resource_type = "library"
  scope_resource_id   = porch_resource.library.id
}

resource "porch_role_assignment" "kids_all_libraries" {
  tenant              = porch_tenant.t.id
  subject_type        = "group"
  subject_id          = porch_group.kids.id
  role_id             = porch_role.viewer.id
  scope_type          = "resource_type"
  scope_resource_type = "library"
}

data "porch_role" "admin" {
  tenant = porch_tenant.t.id
  slug   = "admin"
}
`, perms, expr, attrs)
	}
	const expr1 = "context.hour >= 7 && context.hour < 21"
	const expr2 = "context.hour >= 8 && context.hour < 20"
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			checkGone(t, "porch_role", func(ctx context.Context, c *client.Client, a map[string]string) error {
				_, err := c.GetRole(ctx, a["tenant"], a["id"])
				return err
			}),
			checkGone(t, "porch_role_assignment", func(ctx context.Context, c *client.Client, a map[string]string) error {
				_, err := c.GetAssignment(ctx, a["tenant"], a["id"])
				return err
			}),
		),
		Steps: []resource.TestStep{
			{
				Config: cfg(`["app:access", "media:read"]`, expr1, `{ rating = "pg" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("porch_role.viewer", "id", idRE("rol")),
					resource.TestCheckResourceAttr("porch_role.viewer", "permissions.#", "2"),
					resource.TestCheckTypeSetElemAttrPair("porch_role.power", "includes.*", "porch_role.viewer", "id"),
					resource.TestCheckResourceAttr("porch_policy.daytime", "actions.#", "0"),
					resource.TestCheckResourceAttr("porch_resource.library", "attributes.rating", "pg"),
					resource.TestCheckResourceAttrPair("porch_resource.shelf", "parent_id", "porch_resource.library", "id"),
					resource.TestCheckResourceAttr("porch_role_assignment.kid_daytime", "scope_type", "tenant"),
					resource.TestCheckResourceAttr("porch_role_assignment.kid_daytime", "expires_at", "2099-01-01T00:00:00Z"),
					resource.TestCheckResourceAttr("porch_role_assignment.kids_library", "role_id", "power"),
					resource.TestCheckResourceAttr("data.porch_role.admin", "built_in", "true"),
					resource.TestCheckTypeSetElemAttr("data.porch_role.admin", "permissions.*", "*:*"),
					capture("porch_tenant.t", "id", &tenantID),
					capture("porch_role.viewer", "id", &roleID),
					capture("porch_policy.daytime", "id", &policyID),
					capture("porch_resource.library", "id", &resID),
					capture("porch_role_assignment.kids_all_libraries", "id", &asgID),
				),
			},
			{
				// Roles and policies are replaced in place (PUT); resources are patched.
				Config: cfg(`["app:access", "media:read", "media:list"]`, expr2, `{ rating = "g", owner = "dad" }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: append(append(expectUpdate("porch_role.viewer").PreApply,
					expectUpdate("porch_policy.daytime").PreApply...), expectUpdate("porch_resource.library").PreApply...)},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_role.viewer", "permissions.#", "3"),
					resource.TestCheckResourceAttrPtr("porch_role.viewer", "id", &roleID),
					resource.TestCheckResourceAttr("porch_policy.daytime", "expression", expr2),
					resource.TestCheckResourceAttrPtr("porch_policy.daytime", "id", &policyID),
					resource.TestCheckResourceAttr("porch_resource.library", "attributes.%", "2"),
				),
			},
			{ResourceName: "porch_role.power", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: importID("porch_role.power", "tenant", "id")},
			{ResourceName: "porch_policy.no_delete", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: importID("porch_policy.no_delete", "tenant", "id")},
			{ResourceName: "porch_resource.shelf", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: importID("porch_resource.shelf", "tenant", "id")},
			{ResourceName: "porch_role_assignment.kid_daytime", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: importID("porch_role_assignment.kid_daytime", "tenant", "id")},
			{ResourceName: "porch_role_assignment.kids_all_libraries", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: importID("porch_role_assignment.kids_all_libraries", "tenant", "id")},
			{
				// The slug-referenced role_id imports as the role ID.
				ResourceName: "porch_role_assignment.kids_library", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: importID("porch_role_assignment.kids_library", "tenant", "id"), ImportStateVerifyIgnore: []string{"role_id"},
			},
			{
				// Drift: an assignment removed out of band is recreated.
				PreConfig:        oob(t, func(ctx context.Context, c *client.Client) error { return c.DeleteAssignment(ctx, tenantID, asgID) }),
				Config:           cfg(`["app:access", "media:read", "media:list"]`, expr2, `{ rating = "g", owner = "dad" }`),
				ConfigPlanChecks: expectRecreate("porch_role_assignment.kids_all_libraries"),
			},
			{
				// Deleting the policy removes the conditional assignment; deleting the resource removes the scoped one.
				PreConfig: oob(t, func(ctx context.Context, c *client.Client) error {
					if err := c.DeletePolicy(ctx, tenantID, policyID); err != nil {
						return err
					}
					return c.DeleteResource(ctx, tenantID, resID)
				}),
				Config: cfg(`["app:access", "media:read", "media:list"]`, expr2, `{ rating = "g", owner = "dad" }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: append(append(append(expectRecreate("porch_policy.daytime").PreApply,
					expectRecreate("porch_role_assignment.kid_daytime").PreApply...), expectRecreate("porch_resource.library").PreApply...),
					expectRecreate("porch_role_assignment.kids_library").PreApply...)},
			},
			{
				PreConfig:        oob(t, func(ctx context.Context, c *client.Client) error { return c.DeleteRole(ctx, tenantID, roleID) }),
				Config:           cfg(`["app:access", "media:read", "media:list"]`, expr2, `{ rating = "g", owner = "dad" }`),
				ConfigPlanChecks: expectRecreate("porch_role.viewer"),
			},
		},
	})
}

func TestAccServiceAccount(t *testing.T) {
	testAccPreCheck(t)
	tslug := randSlug("sa")
	var tenantID, saID, secret1 string
	cfg := func(extra string) string {
		return tenantConfig(tslug) + fmt.Sprintf(`
resource "porch_service_account" "backup" {
  tenant = porch_tenant.t.id
  slug   = "backup"
  name   = "Backup job"
  %s
}

resource "porch_role" "reader" {
  tenant      = porch_tenant.t.id
  slug        = "reader"
  name        = "Reader"
  permissions = ["media:read"]
}

resource "porch_role_assignment" "backup" {
  tenant       = porch_tenant.t.id
  subject_type = "service_account"
  subject_id   = porch_service_account.backup.id
  role_id      = porch_role.reader.id
}
`, extra)
	}
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: checkGone(t, "porch_service_account", func(ctx context.Context, c *client.Client, a map[string]string) error {
			_, err := c.GetServiceAccount(ctx, a["tenant"], a["id"])
			return err
		}),
		Steps: []resource.TestStep{
			{
				Config: cfg(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("porch_service_account.backup", "id", idRE("sva")),
					resource.TestMatchResourceAttr("porch_service_account.backup", "client_secret", regexp.MustCompile(`^porch_sa_`)),
					resource.TestCheckResourceAttr("porch_service_account.backup", "access_token_ttl_seconds", "600"),
					resource.TestCheckResourceAttr("porch_service_account.backup", "token_endpoint", acc.Endpoint+"/oauth2/token"),
					capture("porch_tenant.t", "id", &tenantID),
					capture("porch_service_account.backup", "id", &saID),
					capture("porch_service_account.backup", "client_secret", &secret1),
				),
			},
			{
				Config: cfg(`description = "Nightly"
  audiences = ["https://nas.example.com"]
  access_token_ttl_seconds = 300
  enabled = false`),
				ConfigPlanChecks: expectUpdate("porch_service_account.backup"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_service_account.backup", "enabled", "false"),
					resource.TestCheckResourceAttr("porch_service_account.backup", "audiences.#", "1"),
					resource.TestCheckResourceAttr("porch_service_account.backup", "access_token_ttl_seconds", "300"),
					resource.TestCheckResourceAttrPtr("porch_service_account.backup", "client_secret", &secret1),
				),
			},
			{
				Config: cfg(`description = "Nightly"
  audiences = ["https://nas.example.com"]
  access_token_ttl_seconds = 300
  secret_version = "2024-10"`),
				ConfigPlanChecks: expectUpdate("porch_service_account.backup"),
				Check: func(s *terraform.State) error {
					got := s.RootModule().Resources["porch_service_account.backup"].Primary.Attributes["client_secret"]
					if got == secret1 || got == "" {
						return fmt.Errorf("secret not rotated")
					}
					return nil
				},
			},
			{ResourceName: "porch_service_account.backup", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc:       importID("porch_service_account.backup", "tenant", "id"),
				ImportStateVerifyIgnore: []string{"client_secret", "secret_version"}},
			{
				// Clearing the audiences is an in-place update; the secret is kept.
				Config: cfg(`description = "Nightly"
  access_token_ttl_seconds = 300
  secret_version = "2024-10"`),
				ConfigPlanChecks: expectUpdate("porch_service_account.backup"),
				Check:            resource.TestCheckResourceAttr("porch_service_account.backup", "audiences.#", "0"),
			},
			{
				// Deleting the service account also removes its assignment; both are recreated.
				PreConfig: oob(t, func(ctx context.Context, c *client.Client) error {
					return c.DeleteServiceAccount(ctx, tenantID, saID, "")
				}),
				Config: cfg(`description = "Nightly"
  audiences = ["https://nas.example.com"]
  access_token_ttl_seconds = 300
  secret_version = "2024-10"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: append(expectRecreate("porch_service_account.backup").PreApply,
					expectRecreate("porch_role_assignment.backup").PreApply...)},
			},
		},
	})
}

func TestAccDataSources(t *testing.T) {
	testAccPreCheck(t)
	tslug := randSlug("ds")
	email := randSlug("ds") + "@example.com"
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: tenantConfig(tslug) + fmt.Sprintf(`
resource "porch_user" "u" {
  display_name  = "Lookup"
  primary_email = %q
}

data "porch_tenant" "by_slug" {
  slug = porch_tenant.t.slug
}

data "porch_tenant" "by_id" {
  id = porch_tenant.t.id
}

data "porch_user" "u" {
  email = porch_user.u.primary_email
}

data "porch_role" "member" {
  tenant = porch_tenant.t.slug
  slug   = "member"
}
`, email),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("data.porch_tenant.by_slug", "id", "porch_tenant.t", "id"),
					resource.TestCheckResourceAttrPair("data.porch_tenant.by_id", "slug", "porch_tenant.t", "slug"),
					resource.TestCheckResourceAttr("data.porch_tenant.by_id", "status", "active"),
					resource.TestCheckResourceAttrPair("data.porch_user.u", "id", "porch_user.u", "id"),
					resource.TestCheckResourceAttrPair("data.porch_user.u", "public_id", "porch_user.u", "public_id"),
					resource.TestCheckResourceAttr("data.porch_role.member", "built_in", "true"),
					resource.TestMatchResourceAttr("data.porch_role.member", "id", idRE("rol")),
				),
			},
			{
				Config:      `data "porch_user" "missing" { email = "nobody-here@example.com" }`,
				ExpectError: regexp.MustCompile(`404 not_found`),
			},
		},
	})
}
