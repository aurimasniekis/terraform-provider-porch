resource "porch_service_account" "backup" {
  tenant                   = porch_tenant.smiths.id
  slug                     = "backup"
  name                     = "Nightly backup"
  audiences                = ["https://nas.smith.example"]
  access_token_ttl_seconds = 300
  secret_version           = "2026-10" # change to rotate the secret
}

resource "porch_role_assignment" "backup_reader" {
  tenant       = porch_tenant.smiths.id
  subject_type = "service_account"
  subject_id   = porch_service_account.backup.id
  role_id      = porch_role.media_viewer.id
}

# client_credentials: client_id = id, client_secret = client_secret, token URL = token_endpoint.
output "backup_client_secret" {
  value     = porch_service_account.backup.client_secret
  sensitive = true
}
