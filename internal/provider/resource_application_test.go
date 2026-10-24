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

func TestAccApplicationOIDC(t *testing.T) {
	testAccPreCheck(t)
	tslug := randSlug("app")
	var appID, tenantID, secret1 string
	cfg := func(name, client, extra string) string {
		return tenantConfig(tslug) + fmt.Sprintf(`
resource "porch_application" "a" {
  tenant = porch_tenant.t.id
  slug   = "jellyfin"
  name   = %q
  kind   = "oidc"
  %s
  client = {
    %s
  }
}
`, name, extra, client)
	}
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: checkGone(t, "porch_application", func(ctx context.Context, c *client.Client, a map[string]string) error {
			_, err := c.GetApplication(ctx, a["tenant"], a["id"])
			return err
		}),
		Steps: []resource.TestStep{
			{
				Config: cfg("Jellyfin", `redirect_uris = ["https://jellyfin.example.com/sso/callback"]`, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("porch_application.a", "id", idRE("app")),
					resource.TestMatchResourceAttr("porch_application.a", "client_id", idRE("cli")),
					resource.TestMatchResourceAttr("porch_application.a", "client_secret", regexp.MustCompile(`^porch_cs_`)),
					resource.TestCheckResourceAttr("porch_application.a", "issuer", acc.Endpoint),
					resource.TestCheckResourceAttr("porch_application.a", "discovery_url", acc.Endpoint+"/.well-known/openid-configuration"),
					resource.TestCheckResourceAttr("porch_application.a", "enabled", "true"),
					resource.TestCheckResourceAttr("porch_application.a", "access_mode", "all_members"),
					resource.TestCheckResourceAttr("porch_application.a", "disclosure.email", "true"),
					resource.TestCheckResourceAttr("porch_application.a", "disclosure.groups", "false"),
					resource.TestCheckResourceAttr("porch_application.a", "client.client_type", "confidential"),
					resource.TestCheckResourceAttr("porch_application.a", "client.signing_alg", "ES256"),
					resource.TestCheckResourceAttr("porch_application.a", "client.access_token_ttl_seconds", "600"),
					resource.TestCheckTypeSetElemAttr("porch_application.a", "client.grant_types.*", "authorization_code"),
					resource.TestCheckResourceAttr("porch_application.a", "client.allowed_scopes.#", "4"),
					capture("porch_application.a", "id", &appID),
					capture("porch_application.a", "client_secret", &secret1),
					capture("porch_tenant.t", "id", &tenantID),
				),
			},
			{
				// In-place update: name, disclosure, access mode, client configuration (secret unchanged).
				Config: cfg("Jellyfin Media", `redirect_uris = ["https://jellyfin.example.com/sso/callback", "https://media.example.com/cb"]
    grant_types              = ["authorization_code", "refresh_token"]
    allowed_scopes           = ["openid", "profile", "groups"]
    access_token_ttl_seconds = 900
    signing_alg              = "RS256"`, `access_mode = "assigned"
  launch_url  = "https://jellyfin.example.com"
  disclosure  = { groups = true }`),
				ConfigPlanChecks: expectUpdate("porch_application.a"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_application.a", "name", "Jellyfin Media"),
					resource.TestCheckResourceAttr("porch_application.a", "access_mode", "assigned"),
					resource.TestCheckResourceAttr("porch_application.a", "disclosure.groups", "true"),
					resource.TestCheckResourceAttr("porch_application.a", "disclosure.email", "true"),
					resource.TestCheckResourceAttr("porch_application.a", "client.redirect_uris.#", "2"),
					resource.TestCheckResourceAttr("porch_application.a", "client.access_token_ttl_seconds", "900"),
					resource.TestCheckResourceAttr("porch_application.a", "client.signing_alg", "RS256"),
					resource.TestCheckResourceAttrPtr("porch_application.a", "client_secret", &secret1),
				),
			},
			{
				// Rotate the secret.
				Config: cfg("Jellyfin Media", `redirect_uris = ["https://jellyfin.example.com/sso/callback", "https://media.example.com/cb"]
    grant_types              = ["authorization_code", "refresh_token"]
    allowed_scopes           = ["openid", "profile", "groups"]
    access_token_ttl_seconds = 900
    signing_alg              = "RS256"`, `access_mode = "assigned"
  launch_url  = "https://jellyfin.example.com"
  disclosure  = { groups = true }
  secret_version = "2"`),
				ConfigPlanChecks: expectUpdate("porch_application.a"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("porch_application.a", "client_secret", regexp.MustCompile(`^porch_cs_`)),
					func(s *terraform.State) error {
						if s.RootModule().Resources["porch_application.a"].Primary.Attributes["client_secret"] == secret1 {
							return fmt.Errorf("client secret was not rotated")
						}
						return nil
					},
				),
			},
			{ResourceName: "porch_application.a", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc:       importID("porch_application.a", "tenant", "id"),
				ImportStateVerifyIgnore: []string{"client_secret", "secret_version"}},
			{
				// Back to server defaults by removing the optional settings.
				Config:           cfg("Jellyfin", `redirect_uris = ["https://jellyfin.example.com/sso/callback"]`, `enabled = false`),
				ConfigPlanChecks: expectUpdate("porch_application.a"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_application.a", "enabled", "false"),
					resource.TestCheckResourceAttr("porch_application.a", "access_mode", "all_members"),
					resource.TestCheckResourceAttr("porch_application.a", "disclosure.groups", "false"),
					resource.TestCheckResourceAttr("porch_application.a", "client.grant_types.#", "1"),
					resource.TestCheckResourceAttr("porch_application.a", "client.allowed_scopes.#", "4"),
					resource.TestCheckResourceAttr("porch_application.a", "client.signing_alg", "ES256"),
				),
			},
			{
				PreConfig: oob(t, func(ctx context.Context, c *client.Client) error {
					return c.DeleteApplication(ctx, tenantID, appID, "")
				}),
				Config:           cfg("Jellyfin", `redirect_uris = ["https://jellyfin.example.com/sso/callback"]`, `enabled = false`),
				ConfigPlanChecks: expectRecreate("porch_application.a"),
				Check:            resource.TestMatchResourceAttr("porch_application.a", "client_secret", regexp.MustCompile(`^porch_cs_`)),
			},
			{
				// A public client has no secret; changing the client type replaces the application.
				Config:           cfg("Jellyfin", `client_type = "public"`+"\n"+`redirect_uris = ["https://jellyfin.example.com/sso/callback"]`, ""),
				ConfigPlanChecks: expectReplace("porch_application.a"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("porch_application.a", "client_secret"),
					resource.TestCheckResourceAttr("porch_application.a", "client.require_pkce", "true"),
				),
			},
		},
	})
}

func TestAccApplicationForwardAuthAndShare(t *testing.T) {
	testAccPreCheck(t)
	owner, other := randSlug("own"), randSlug("oth")
	host := randSlug("whoami") + ".example.com"
	var appID, ownerID, otherID string
	cfg := func(hosts, headers string) string {
		return fmt.Sprintf(`
resource "porch_tenant" "owner" {
  slug = %q
  name = "Owner"
}

resource "porch_tenant" "other" {
  slug = %q
  name = "Other"
}

resource "porch_application" "fa" {
  tenant          = porch_tenant.owner.id
  slug            = "whoami"
  name            = "Whoami"
  kind            = "forward_auth"
  hosts           = %s
  forward_headers = %s
}

resource "porch_application_share" "s" {
  tenant         = porch_tenant.owner.id
  application_id = porch_application.fa.id
  target_tenant  = porch_tenant.other.slug
}
`, owner, other, hosts, headers)
	}
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: checkGone(t, "porch_application_share", func(ctx context.Context, c *client.Client, a map[string]string) error {
			_, err := c.GetApplicationShare(ctx, a["tenant"], a["application_id"], a["target_tenant"])
			return err
		}),
		Steps: []resource.TestStep{
			{
				Config: cfg(fmt.Sprintf("[%q]", host), `{ "X-User" = "sub", "X-Family" = "tenant" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_application.fa", "hosts.#", "1"),
					resource.TestCheckTypeSetElemAttr("porch_application.fa", "hosts.*", host),
					resource.TestCheckResourceAttr("porch_application.fa", "forward_headers.X-User", "sub"),
					resource.TestCheckNoResourceAttr("porch_application.fa", "client_id"),
					resource.TestCheckNoResourceAttr("porch_application.fa", "client_secret"),
					resource.TestCheckResourceAttrPair("porch_application_share.s", "target_tenant_id", "porch_tenant.other", "id"),
					capture("porch_application.fa", "id", &appID),
					capture("porch_tenant.owner", "id", &ownerID),
					capture("porch_tenant.other", "id", &otherID),
				),
			},
			{
				Config:           cfg(fmt.Sprintf("[%q, %q]", host, "b-"+host), `{ "X-User" = "email" }`),
				ConfigPlanChecks: expectUpdate("porch_application.fa"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_application.fa", "hosts.#", "2"),
					resource.TestCheckResourceAttr("porch_application.fa", "forward_headers.%", "1"),
					resource.TestCheckResourceAttr("porch_application.fa", "forward_headers.X-User", "email"),
				),
			},
			{ResourceName: "porch_application.fa", ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: importID("porch_application.fa", "tenant", "id"), ImportStateVerifyIgnore: []string{"secret_version"}},
			{ResourceName: "porch_application_share.s", ImportState: true, ImportStateVerify: true},
			{
				// Clearing forward_headers (back to Porch's defaults) is an in-place update.
				Config:           cfg(fmt.Sprintf("[%q]", host), `{}`),
				ConfigPlanChecks: expectUpdate("porch_application.fa"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_application.fa", "forward_headers.%", "0"),
					capture("porch_application.fa", "id", &appID),
				),
			},
			{
				PreConfig: oob(t, func(ctx context.Context, c *client.Client) error {
					return c.UnshareApplication(ctx, ownerID, appID, otherID)
				}),
				Config:           cfg(fmt.Sprintf("[%q]", host), `{}`),
				ConfigPlanChecks: expectRecreate("porch_application_share.s"),
			},
			{
				PreConfig: oob(t, func(ctx context.Context, c *client.Client) error {
					return c.DeleteApplication(ctx, ownerID, appID, "")
				}),
				Config:           cfg(fmt.Sprintf("[%q]", host), `{}`),
				ConfigPlanChecks: expectRecreate("porch_application.fa"),
			},
		},
	})
}
