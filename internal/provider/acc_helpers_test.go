package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/aurimasniekis/terraform-provider-porch/internal/client"
)

func randSlug(prefix string) string {
	return prefix + "-" + acctest.RandStringFromCharSet(8, "abcdefghijklmnopqrstuvwxyz0123456789")
}

// capture stores an attribute of a resource in *dst (for out-of-band drift steps).
func capture(name, attr string, dst *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("%s not in state", name)
		}
		*dst = rs.Primary.Attributes[attr]
		if *dst == "" {
			return fmt.Errorf("%s.%s is empty", name, attr)
		}
		return nil
	}
}

// importID builds "<tenant>/<id>"-style import IDs from state attributes.
func importID(name string, attrs ...string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return "", fmt.Errorf("%s not in state", name)
		}
		parts := make([]string, 0, len(attrs))
		for _, a := range attrs {
			parts = append(parts, rs.Primary.Attributes[a])
		}
		return strings.Join(parts, "/"), nil
	}
}

// expectRecreate asserts that the next plan creates the resource again (after an out-of-band deletion).
func expectRecreate(name string) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionCreate)}}
}

func expectUpdate(name string) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)}}
}

func expectReplace(name string) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionReplace)}}
}

// oob runs an out-of-band API call and fails the test on error.
func oob(t *testing.T, f func(ctx context.Context, c *client.Client) error) func() {
	return func() {
		t.Helper()
		if err := f(context.Background(), adminClient(t)); err != nil {
			t.Fatalf("out-of-band change: %v", err)
		}
	}
}

// checkGone verifies that every resource of the given type was deleted from Porch.
func checkGone(t *testing.T, typ string, get func(ctx context.Context, c *client.Client, attrs map[string]string) error) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		c := adminClient(t)
		for _, rs := range s.RootModule().Resources {
			if rs.Type != typ {
				continue
			}
			err := get(context.Background(), c, rs.Primary.Attributes)
			if err == nil {
				return fmt.Errorf("%s %s still exists", typ, rs.Primary.ID)
			}
			if !client.IsNotFound(err) {
				return fmt.Errorf("%s %s: %w", typ, rs.Primary.ID, err)
			}
		}
		return nil
	}
}

func tenantConfig(slug string) string {
	return fmt.Sprintf(`
resource "porch_tenant" "t" {
  slug = %q
  name = "Acceptance %s"
}
`, slug, slug)
}

func idRE(prefix string) *regexp.Regexp { return regexp.MustCompile(`^` + prefix + `_[0-9a-z]+$`) }
