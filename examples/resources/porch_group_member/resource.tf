resource "porch_group_member" "ada_parent" {
  tenant   = porch_tenant.smiths.id
  group_id = porch_group.parents.id
  user_id  = porch_tenant_member.ada.user_id # the user must be a member of the tenant
}
