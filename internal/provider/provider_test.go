package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestSchemasAreValid(t *testing.T) {
	ctx := context.Background()
	p := New("test")()
	var ps fwprovider.SchemaResponse
	p.Schema(ctx, fwprovider.SchemaRequest{}, &ps)
	if ps.Diagnostics.HasError() {
		t.Fatalf("provider schema: %v", ps.Diagnostics)
	}
	if d := ps.Schema.ValidateImplementation(ctx); d.HasError() {
		t.Fatalf("provider schema: %v", d)
	}
	seen := map[string]bool{}
	for _, f := range p.Resources(ctx) {
		r := f()
		var md fwresource.MetadataResponse
		r.Metadata(ctx, fwresource.MetadataRequest{ProviderTypeName: "porch"}, &md)
		if seen[md.TypeName] {
			t.Fatalf("duplicate resource %s", md.TypeName)
		}
		seen[md.TypeName] = true
		var sr fwresource.SchemaResponse
		r.Schema(ctx, fwresource.SchemaRequest{}, &sr)
		if sr.Diagnostics.HasError() {
			t.Fatalf("%s: %v", md.TypeName, sr.Diagnostics)
		}
		if d := sr.Schema.ValidateImplementation(ctx); d.HasError() {
			t.Fatalf("%s: %v", md.TypeName, d)
		}
		if _, ok := r.(fwresource.ResourceWithImportState); !ok {
			t.Errorf("%s does not support import", md.TypeName)
		}
		if _, ok := sr.Schema.Attributes["id"]; !ok {
			t.Errorf("%s has no id attribute", md.TypeName)
		}
	}
	if len(seen) != 16 {
		t.Errorf("got %d resources, want 16", len(seen))
	}
	for _, f := range p.DataSources(ctx) {
		d := f()
		var md datasource.MetadataResponse
		d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "porch"}, &md)
		var sr datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &sr)
		if diags := sr.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Fatalf("%s: %v", md.TypeName, diags)
		}
	}
}

func TestSplitID(t *testing.T) {
	if p, err := splitID("ten_1/app_2", 2, "<tenant>/<id>"); err != nil || p[0] != "ten_1" || p[1] != "app_2" {
		t.Fatalf("got %v %v", p, err)
	}
	for _, bad := range []string{"", "x", "a/", "/b", "a/b/c"} {
		if _, err := splitID(bad, 2, "<tenant>/<id>"); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestProviderConfigValidation(t *testing.T) {
	t.Setenv(EnvEndpoint, "")
	t.Setenv(EnvToken, "")
	const ds = `
data "porch_tenant" "x" {
  slug = "x"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      `provider "porch" { endpoint = "https://auth.example.com" }` + ds,
				ExpectError: errRE("Missing Porch automation token"),
			},
			{
				Config:      `provider "porch" { token = "porch_at_abc" }` + ds,
				ExpectError: errRE("Missing Porch endpoint"),
			},
			{
				Config:      "provider \"porch\" {\n  endpoint = \"https://auth.example.com\"\n  token = \"porch_setup_abc\"\n}\n" + ds,
				ExpectError: errRE("Invalid Porch automation token", `token must be a Porch automation token starting with "porch_at_"`),
			},
			{
				Config:      "provider \"porch\" {\n  endpoint = \"auth.example.com\"\n  token = \"porch_at_abc\"\n}\n" + ds,
				ExpectError: errRE("Cannot configure the Porch client", "endpoint must be an absolute http(s) URL"),
			},
			{
				Config: `provider "porch" {
  endpoint = "https://auth.example.com"
  token    = "porch_at_abc"
  ca_file  = "/nonexistent/ca.pem"
}` + ds,
				ExpectError: errRE("Cannot configure the Porch client", "ca_file"),
			},
		},
	})
}
