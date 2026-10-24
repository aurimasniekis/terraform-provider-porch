resource "porch_tenant_member" "ada" {
  tenant  = porch_tenant.smiths.id
  user_id = porch_user.ada.id
  status  = "active" # or "suspended"
}
