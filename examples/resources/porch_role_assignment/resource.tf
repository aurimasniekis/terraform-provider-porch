# Kids may use the media apps, but only during the day.
resource "porch_role_assignment" "kids_media" {
  tenant              = porch_tenant.smiths.id
  subject_type        = "group"
  subject_id          = porch_group.kids.id
  role_id             = porch_role.media_viewer.id
  condition_policy_id = porch_policy.daytime.id
}

# Access to one application only (resource scope).
resource "porch_role_assignment" "ada_whoami" {
  tenant              = porch_tenant.smiths.id
  subject_type        = "user"
  subject_id          = porch_tenant_member.ada.user_id
  role_id             = porch_role.media_viewer.id
  scope_type          = "resource"
  scope_resource_type = "app"
  scope_resource_id   = porch_application.whoami.id
  expires_at          = "2030-01-01T00:00:00Z"
}
