package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

func tokensConfig(slug string) string {
	return tenantConfig(slug) + `
resource "porch_service_account" "ci" {
  tenant    = porch_tenant.t.id
  slug      = "ci"
  name      = "CI"
  audiences = ["https://api.example.com"]
}

resource "porch_automation_token" "deed" {
  name            = "deed"
  role            = "inspector"
  expires_in_days = 30
}

resource "porch_automation_token" "apps" {
  name            = "apps-reader"
  tenant          = porch_tenant.t.id
  permissions     = ["app:read", "service_account:read"]
  expires_in_days = 7
}
`
}

// tokenWorks checks that the token stored in name's state can read the "ci" service account of tenant slug and
// cannot create a tenant.
func tokenWorks(t *testing.T, name, slug string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("%s not in state", name)
		}
		secret := rs.Primary.Attributes["token"]
		if !strings.HasPrefix(secret, "porch_at_") || !strings.HasSuffix(secret, rs.Primary.Attributes["hint"]) {
			return fmt.Errorf("%s: unexpected token %q", name, secret)
		}
		c, err := client.New(client.Config{Endpoint: acc.Endpoint, Token: secret, UserAgent: "tfacc-token"})
		if err != nil {
			return err
		}
		ctx := context.Background()
		if _, err := c.GetServiceAccount(ctx, slug, "ci"); err != nil {
			return fmt.Errorf("%s cannot read tenant %s: %w", name, slug, err)
		}
		if _, err := c.CreateTenant(ctx, client.TenantCreate{Slug: randSlug("no"), Name: "No"}); err == nil {
			return fmt.Errorf("%s (read-only) created a tenant", name)
		}
		return nil
	}
}

// checkRevoked verifies that destroyed tokens are revoked (Porch keeps revoked tokens listed for a while).
func checkRevoked(t *testing.T) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		c := adminClient(t)
		for _, rs := range s.RootModule().Resources {
			if rs.Type != "porch_automation_token" {
				continue
			}
			tok, err := c.GetAutomationToken(context.Background(), rs.Primary.Attributes["tenant"], rs.Primary.ID)
			if client.IsNotFound(err) {
				continue
			}
			if err != nil {
				return err
			}
			if tok.RevokedAt == nil {
				return fmt.Errorf("token %s still active", rs.Primary.ID)
			}
		}
		return nil
	}
}

// ADR-0048: the provider's token issues read-only tokens for other integrations.
func TestAccAutomationToken(t *testing.T) {
	testAccPreCheck(t)
	slug := randSlug("tok")
	var deedID string
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkRevoked(t),
		Steps: []resource.TestStep{
			{
				Config: tokensConfig(slug),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("porch_automation_token.deed", "id", idRE("atk")),
					resource.TestCheckResourceAttr("porch_automation_token.deed", "role", "inspector"),
					resource.TestCheckResourceAttr("porch_automation_token.deed", "never_expires", "false"),
					resource.TestMatchResourceAttr("porch_automation_token.deed", "issued_by", idRE("atk")),
					resource.TestCheckResourceAttrSet("porch_automation_token.deed", "expires_at"),
					resource.TestCheckResourceAttr("porch_automation_token.apps", "permissions.#", "2"),
					tokenWorks(t, "porch_automation_token.deed", slug),
					tokenWorks(t, "porch_automation_token.apps", slug),
					capture("porch_automation_token.deed", "id", &deedID),
				),
			},
			// A second plan changes nothing (the secret stays in state).
			{Config: tokensConfig(slug), PlanOnly: true},
			{
				ResourceName: "porch_automation_token.deed", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc:       importID("porch_automation_token.deed", "id"),
				ImportStateVerifyIgnore: []string{"token", "expires_in_days"},
				ImportStateIdPrefix:     "installation/",
			},
			// Revoked out of band: Terraform issues a new token.
			{
				PreConfig: oob(t, func(ctx context.Context, c *client.Client) error {
					return c.RevokeAutomationToken(ctx, "", deedID)
				}),
				Config:           tokensConfig(slug),
				ConfigPlanChecks: expectRecreate("porch_automation_token.deed"),
				Check:            tokenWorks(t, "porch_automation_token.deed", slug),
			},
			// Changing the lifetime replaces the token.
			{
				Config:           strings.Replace(tokensConfig(slug), "expires_in_days = 30", "expires_in_days = 20", 1),
				ConfigPlanChecks: expectReplace("porch_automation_token.deed"),
			},
			// Only a token that never expires issues one that never expires (the harness token does).
			{
				Config: strings.Replace(tokensConfig(slug), "expires_in_days = 30", "expires_in_days = 20", 1) + `
resource "porch_automation_token" "forever" {
  name          = "forever"
  role          = "inspector"
  never_expires = true
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_automation_token.forever", "never_expires", "true"),
					resource.TestCheckResourceAttr("porch_automation_token.forever", "expires_at", ""),
					tokenWorks(t, "porch_automation_token.forever", slug),
				),
			},
		},
	})
}

// Configuration mistakes fail at plan time; tokens Porch does not let issue fail with its problem document.
func TestAccAutomationTokenErrors(t *testing.T) {
	testAccPreCheck(t)
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "porch_automation_token" "x" {
  name            = "x"
  permissions     = ["app:read"]
  expires_in_days = 1
}
`,
				ExpectError: errRE("an installation token (without tenant) needs a role"),
			},
			{
				Config: `
resource "porch_automation_token" "x" {
  name = "x"
  role = "inspector"
}
`,
				ExpectError: errRE("set expires_in_days (1-366) or never_expires = true"),
			},
			{
				// A read-only token issues nothing.
				Config: providerWithToken(acc.AuditorToken) + `
resource "porch_automation_token" "x" {
  name            = "x"
  role            = "auditor"
  expires_in_days = 1
}
`,
				ExpectError: errRE("Cannot create automation token", "403 forbidden"),
			},
			{
				// A tenant token issues only within its own grant.
				Config: providerWithToken(acc.LimitedToken) + fmt.Sprintf(`
resource "porch_automation_token" "x" {
  name            = "x"
  tenant          = %q
  permissions     = ["app:manage"]
  expires_in_days = 1
}
`, acc.LimitedTenant),
				ExpectError: errRE("Cannot create automation token", "404 not_found"),
			},
			{
				// ...and never outlives itself (the limited token expires in a day).
				Config: providerWithToken(acc.LimitedToken) + fmt.Sprintf(`
resource "porch_automation_token" "x" {
  name            = "x"
  tenant          = %q
  permissions     = ["group:manage"]
  expires_in_days = 30
}
`, acc.LimitedTenant),
				ExpectError: errRE("Cannot create automation token", "must not outlive the issuing token"),
			},
		},
	})
}
