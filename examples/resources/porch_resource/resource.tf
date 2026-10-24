resource "porch_resource" "movies" {
  tenant = porch_tenant.smiths.id
  type   = "library"
  slug   = "movies"
  name   = "Movies"

  attributes = {
    rating = "pg"
  }
}

resource "porch_resource" "cartoons" {
  tenant    = porch_tenant.smiths.id
  type      = "library"
  slug      = "cartoons"
  name      = "Cartoons"
  parent_id = porch_resource.movies.id
}
