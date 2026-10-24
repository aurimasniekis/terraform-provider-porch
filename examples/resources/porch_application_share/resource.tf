# Members of the Joneses can use the Smiths' Jellyfin.
resource "porch_application_share" "jellyfin_joneses" {
  tenant         = porch_tenant.smiths.id
  application_id = porch_application.jellyfin.id
  target_tenant  = porch_tenant.joneses.id
}
