resource "porch_group" "parents" {
  tenant      = porch_tenant.smiths.id
  slug        = "parents"
  name        = "Parents"
  description = "Grown-ups of the house"
}
