# Everyone signing in with a verified Google Workspace account of smith.example joins the tenant.
resource "porch_domain_rule" "smiths" {
  tenant        = porch_tenant.smiths.id
  domain        = "smith.example"
  provider_id   = porch_identity_provider.google.id
  evidence_kind = "workspace_hd"
  mode          = "jit"
}
