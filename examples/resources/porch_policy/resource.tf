# Condition policy: referenced by assignments, which then apply only while it is true.
resource "porch_policy" "daytime" {
  tenant     = porch_tenant.smiths.id
  slug       = "daytime"
  name       = "Between 07:00 and 21:00 UTC"
  effect     = "condition"
  expression = "context.hour >= 7 && context.hour < 21"
}

# Deny policy: overrides every grant of its actions.
resource "porch_policy" "no_delete" {
  tenant     = porch_tenant.smiths.id
  slug       = "no-delete"
  name       = "Nobody deletes media"
  effect     = "deny"
  actions    = ["media:delete"]
  expression = "true"
}
