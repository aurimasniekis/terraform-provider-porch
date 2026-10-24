# Accepted when someone signs in with a matching verified upstream email.
resource "porch_invite" "grandma" {
  tenant        = porch_tenant.smiths.id
  email         = "grandma@example.com"
  valid_seconds = 1209600 # 14 days
}
