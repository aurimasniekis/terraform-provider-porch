# The provider's token must be an installation admin token created by an installation owner.

# Reads the configuration of every tenant and changes nothing (for example a catalogue or monitoring connector).
resource "porch_automation_token" "inspector" {
  name            = "deed"
  role            = "inspector"
  expires_in_days = 180
}

# Reads one tenant's applications and service accounts.
resource "porch_automation_token" "smith_apps" {
  name            = "smith-apps-reader"
  tenant          = porch_tenant.smiths.id
  permissions     = ["app:read", "service_account:read"]
  expires_in_days = 90
}

output "inspector_token" {
  value     = porch_automation_token.inspector.token
  sensitive = true
}
