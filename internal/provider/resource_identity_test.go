package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

func TestAccUser(t *testing.T) {
	testAccPreCheck(t)
	email := randSlug("user") + "@example.com"
	var id string
	cfg := func(name, emailLine, status string) string {
		return fmt.Sprintf(`
resource "porch_user" "u" {
  display_name = %q
  %s
  %s
}
`, name, emailLine, status)
	}
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: checkGone(t, "porch_user", func(ctx context.Context, c *client.Client, a map[string]string) error {
			_, err := c.GetUser(ctx, a["id"])
			return err
		}),
		Steps: []resource.TestStep{
			{
				Config: cfg("Alice", fmt.Sprintf("primary_email = %q", email), ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("porch_user.u", "id", idRE("usr")),
					resource.TestCheckResourceAttrSet("porch_user.u", "public_id"),
					resource.TestCheckResourceAttr("porch_user.u", "primary_email", email),
					resource.TestCheckResourceAttr("porch_user.u", "status", "active"),
					capture("porch_user.u", "id", &id),
				),
			},
			{
				Config:           cfg("Alice Smith", "", `status = "disabled"`),
				ConfigPlanChecks: expectUpdate("porch_user.u"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_user.u", "display_name", "Alice Smith"),
					resource.TestCheckNoResourceAttr("porch_user.u", "primary_email"),
					resource.TestCheckResourceAttr("porch_user.u", "status", "disabled"),
				),
			},
			{ResourceName: "porch_user.u", ImportState: true, ImportStateVerify: true},
			{
				PreConfig:        oob(t, func(ctx context.Context, c *client.Client) error { return c.DeleteUser(ctx, id, "") }),
				Config:           cfg("Alice Smith", "", `status = "disabled"`),
				ConfigPlanChecks: expectRecreate("porch_user.u"),
			},
		},
	})
}

func TestAccIdentityProviderAndDomainRule(t *testing.T) {
	testAccPreCheck(t)
	slug := randSlug("idp")
	tslug := randSlug("dr")
	domain := randSlug("corp") + ".example.com"
	var provID, ruleID, tenantID string
	cfg := func(name, secret, extra, mode string) string {
		return tenantConfig(tslug) + fmt.Sprintf(`
resource "porch_identity_provider" "p" {
  slug          = %q
  kind          = "oidc"
  name          = %q
  issuer        = "https://login.example.com"
  client_id     = "porch-client"
  client_secret = %q
  %s
}

resource "porch_domain_rule" "r" {
  tenant        = porch_tenant.t.id
  domain        = %q
  provider_id   = porch_identity_provider.p.id
  evidence_kind = "verified_email"
  mode          = %q
}
`, slug, name, secret, extra, domain, mode)
	}
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			checkGone(t, "porch_identity_provider", func(ctx context.Context, c *client.Client, a map[string]string) error {
				_, err := c.GetIdentityProvider(ctx, a["id"])
				return err
			}),
			checkGone(t, "porch_domain_rule", func(ctx context.Context, c *client.Client, a map[string]string) error {
				_, err := c.GetDomainRule(ctx, a["tenant"], a["id"])
				return err
			}),
		),
		Steps: []resource.TestStep{
			{
				Config: cfg("Corp SSO", "s3cret-1", "", "jit"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("porch_identity_provider.p", "id", idRE("idp")),
					resource.TestCheckResourceAttr("porch_identity_provider.p", "scopes", "openid email profile"),
					resource.TestCheckResourceAttr("porch_identity_provider.p", "enabled", "true"),
					resource.TestCheckResourceAttr("porch_identity_provider.p", "callback_url", acc.Endpoint+"/login/"+slug+"/callback"),
					resource.TestCheckResourceAttr("porch_identity_provider.p", "allowed_hds.#", "0"),
					resource.TestMatchResourceAttr("porch_domain_rule.r", "id", idRE("dom")),
					capture("porch_identity_provider.p", "id", &provID),
					capture("porch_domain_rule.r", "id", &ruleID),
					capture("porch_tenant.t", "id", &tenantID),
				),
			},
			{
				Config:           cfg("Corp SSO v2", "s3cret-2", `scopes = "openid email"`+"\n  enabled = false", "jit"),
				ConfigPlanChecks: expectUpdate("porch_identity_provider.p"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_identity_provider.p", "name", "Corp SSO v2"),
					resource.TestCheckResourceAttr("porch_identity_provider.p", "scopes", "openid email"),
					resource.TestCheckResourceAttr("porch_identity_provider.p", "enabled", "false"),
				),
			},
			{ResourceName: "porch_identity_provider.p", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"client_secret"}},
			{ResourceName: "porch_identity_provider.p", ImportState: true, ImportStateVerify: true, ImportStateId: slug,
				ImportStateVerifyIgnore: []string{"client_secret"}},
			{ResourceName: "porch_domain_rule.r", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: importID("porch_domain_rule.r", "tenant", "id")},
			// Rules are immutable: changing the mode replaces the rule.
			{
				Config:           cfg("Corp SSO v2", "s3cret-2", `scopes = "openid email"`+"\n  enabled = false", "invite_only"),
				ConfigPlanChecks: expectReplace("porch_domain_rule.r"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_domain_rule.r", "mode", "invite_only"),
					capture("porch_domain_rule.r", "id", &ruleID),
				),
			},
			{
				PreConfig: oob(t, func(ctx context.Context, c *client.Client) error {
					return c.DeleteDomainRule(ctx, tenantID, ruleID, "")
				}),
				Config:           cfg("Corp SSO v2", "s3cret-2", `scopes = "openid email"`+"\n  enabled = false", "invite_only"),
				ConfigPlanChecks: expectRecreate("porch_domain_rule.r"),
			},
			{
				// Deleting the provider removes its rules as well (both are recreated).
				PreConfig: oob(t, func(ctx context.Context, c *client.Client) error {
					return c.DeleteIdentityProvider(ctx, provID, "")
				}),
				Config:           cfg("Corp SSO v2", "s3cret-2", `scopes = "openid email"`+"\n  enabled = false", "invite_only"),
				ConfigPlanChecks: expectRecreate("porch_identity_provider.p"),
			},
		},
	})
}

func TestAccGoogleProviderAllowedHDs(t *testing.T) {
	testAccPreCheck(t)
	slug := randSlug("goog")
	cfg := func(hds string) string {
		return fmt.Sprintf(`
resource "porch_identity_provider" "g" {
  slug          = %q
  kind          = "google"
  name          = "Google"
  client_id     = "x.apps.googleusercontent.com"
  client_secret = "google-secret"
  allowed_hds   = %s
}
`, slug, hds)
	}
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg(`["smith.example"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_identity_provider.g", "issuer", "https://accounts.google.com"),
					resource.TestCheckTypeSetElemAttr("porch_identity_provider.g", "allowed_hds.*", "smith.example"),
				),
			},
			{
				Config:           cfg(`["smith.example", "jones.example"]`),
				ConfigPlanChecks: expectUpdate("porch_identity_provider.g"),
				Check:            resource.TestCheckResourceAttr("porch_identity_provider.g", "allowed_hds.#", "2"),
			},
			// Clearing allowed_hds is an in-place update.
			{
				Config:           cfg(`[]`),
				ConfigPlanChecks: expectUpdate("porch_identity_provider.g"),
				Check:            resource.TestCheckResourceAttr("porch_identity_provider.g", "allowed_hds.#", "0"),
			},
		},
	})
}

func TestAccTenantMemberAndInvite(t *testing.T) {
	testAccPreCheck(t)
	tslug := randSlug("mem")
	email := randSlug("inv") + "@example.com"
	var memberID, inviteID, tenantID string
	cfg := func(status string) string {
		return tenantConfig(tslug) + fmt.Sprintf(`
resource "porch_user" "u" {
  display_name = "Member"
}

resource "porch_tenant_member" "m" {
  tenant  = porch_tenant.t.id
  user_id = porch_user.u.id
  status  = %q
}

resource "porch_invite" "i" {
  tenant        = porch_tenant.t.id
  email         = %q
  valid_seconds = 86400
}
`, status, email)
	}
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg("active"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("porch_tenant_member.m", "id", idRE("mem")),
					resource.TestCheckResourceAttr("porch_tenant_member.m", "source", "terraform"),
					resource.TestMatchResourceAttr("porch_invite.i", "id", idRE("inv")),
					resource.TestCheckResourceAttr("porch_invite.i", "consumed", "false"),
					resource.TestCheckResourceAttrSet("porch_invite.i", "expires_at"),
					capture("porch_tenant_member.m", "id", &memberID),
					capture("porch_invite.i", "id", &inviteID),
					capture("porch_tenant.t", "id", &tenantID),
				),
			},
			{
				Config:           cfg("suspended"),
				ConfigPlanChecks: expectUpdate("porch_tenant_member.m"),
				Check:            resource.TestCheckResourceAttr("porch_tenant_member.m", "status", "suspended"),
			},
			{ResourceName: "porch_tenant_member.m", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: importID("porch_tenant_member.m", "tenant", "id")},
			{ResourceName: "porch_invite.i", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: importID("porch_invite.i", "tenant", "id"), ImportStateVerifyIgnore: []string{"valid_seconds"}},
			{
				PreConfig: oob(t, func(ctx context.Context, c *client.Client) error {
					if err := c.RemoveMember(ctx, tenantID, memberID, ""); err != nil {
						return err
					}
					return c.DeleteInvite(ctx, tenantID, inviteID)
				}),
				Config: cfg("suspended"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: append(expectRecreate("porch_tenant_member.m").PreApply,
					expectRecreate("porch_invite.i").PreApply...)},
				Check: resource.TestCheckResourceAttr("porch_tenant_member.m", "status", "suspended"),
			},
		},
	})
}

// After an import valid_seconds is unknown; configuring it is recorded in place instead of replacing the invitation.
func TestAccInviteImportKeepsInvitation(t *testing.T) {
	testAccPreCheck(t)
	tslug := randSlug("invimp")
	email := randSlug("imp") + "@example.com"
	var tenantID, inviteID string
	withInvite := tenantConfig(tslug) + fmt.Sprintf(`
resource "porch_invite" "i" {
  tenant        = porch_tenant.t.id
  email         = %q
  valid_seconds = 3600
}
`, email)
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: tenantConfig(tslug),
				Check: resource.ComposeAggregateTestCheckFunc(
					capture("porch_tenant.t", "id", &tenantID),
					func(*terraform.State) error {
						i, err := adminClient(t).CreateInvite(context.Background(), tenantID, client.InviteCreate{Email: email})
						if err == nil {
							inviteID = i.ID
						}
						return err
					},
				),
			},
			{
				Config: withInvite, ResourceName: "porch_invite.i", ImportState: true, ImportStatePersist: true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return tenantID + "/" + inviteID, nil },
			},
			{
				Config:           withInvite,
				ConfigPlanChecks: expectUpdate("porch_invite.i"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPtr("porch_invite.i", "id", &inviteID),
					resource.TestCheckResourceAttr("porch_invite.i", "valid_seconds", "3600"),
				),
			},
		},
	})
}
