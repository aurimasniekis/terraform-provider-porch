resource "porch_role" "media_viewer" {
  tenant      = porch_tenant.smiths.id
  slug        = "media-viewer"
  name        = "Media viewer"
  permissions = ["app:access", "media:read"]
}

resource "porch_role" "media_admin" {
  tenant      = porch_tenant.smiths.id
  slug        = "media-admin"
  name        = "Media admin"
  permissions = ["media:*"]
  includes    = [porch_role.media_viewer.id]
}
