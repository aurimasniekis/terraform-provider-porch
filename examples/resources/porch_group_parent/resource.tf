# Members of "kids" inherit everything assigned to "family".
resource "porch_group_parent" "kids_in_family" {
  tenant          = porch_tenant.smiths.id
  group_id        = porch_group.kids.id
  parent_group_id = porch_group.family.id
}
