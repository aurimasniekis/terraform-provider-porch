package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

func TestAccTenant(t *testing.T) {
	testAccPreCheck(t)
	slug := randSlug("ten")
	var id string
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: checkGone(t, "porch_tenant", func(ctx context.Context, c *client.Client, a map[string]string) error {
			_, err := c.GetTenant(ctx, a["id"])
			return err
		}),
		Steps: []resource.TestStep{
			{
				Config: tenantConfig(slug),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_tenant.t", "slug", slug),
					resource.TestCheckResourceAttr("porch_tenant.t", "status", "active"),
					resource.TestCheckResourceAttr("porch_tenant.t", "session_idle_seconds", "0"),
					resource.TestMatchResourceAttr("porch_tenant.t", "id", idRE("ten")),
					capture("porch_tenant.t", "id", &id),
				),
			},
			{
				Config: fmt.Sprintf(`
resource "porch_tenant" "t" {
  slug                     = %q
  name                     = "Renamed"
  status                   = "disabled"
  session_idle_seconds     = 3600
  session_absolute_seconds = 86400
}
`, slug),
				ConfigPlanChecks: expectUpdate("porch_tenant.t"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_tenant.t", "name", "Renamed"),
					resource.TestCheckResourceAttr("porch_tenant.t", "status", "disabled"),
					resource.TestCheckResourceAttr("porch_tenant.t", "session_idle_seconds", "3600"),
					resource.TestCheckResourceAttr("porch_tenant.t", "session_absolute_seconds", "86400"),
				),
			},
			{ResourceName: "porch_tenant.t", ImportState: true, ImportStateVerify: true},
			// Import by slug as well.
			{ResourceName: "porch_tenant.t", ImportState: true, ImportStateVerify: true, ImportStateId: slug},
			// Back to defaults (0 = installation default) in place.
			{
				Config:           tenantConfig(slug),
				ConfigPlanChecks: expectUpdate("porch_tenant.t"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("porch_tenant.t", "status", "active"),
					resource.TestCheckResourceAttr("porch_tenant.t", "session_idle_seconds", "0"),
				),
			},
			// Drift: deleted out of band → recreated.
			{
				PreConfig: oob(t, func(ctx context.Context, c *client.Client) error {
					return c.DeleteTenant(ctx, id, "")
				}),
				Config:           tenantConfig(slug),
				ConfigPlanChecks: expectRecreate("porch_tenant.t"),
			},
			// Changing the slug replaces the tenant.
			{
				Config:           tenantConfig(slug + "-x"),
				ConfigPlanChecks: expectReplace("porch_tenant.t"),
			},
		},
	})
}
