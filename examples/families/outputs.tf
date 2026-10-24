output "tenants" {
  description = "Tenant IDs by family."
  value       = { for k, t in porch_tenant.family : k => t.id }
}

output "jellyfin" {
  description = "OIDC settings for Jellyfin."
  value = {
    client_id     = porch_application.jellyfin.client_id
    issuer        = porch_application.jellyfin.issuer
    discovery_url = porch_application.jellyfin.discovery_url
  }
}

output "jellyfin_client_secret" {
  description = "Jellyfin client secret (shown once by Porch, kept in state)."
  value       = porch_application.jellyfin.client_secret
  sensitive   = true
}

output "backup_service_account" {
  description = "Client credentials of the backup job (use with the client_credentials grant)."
  value = {
    client_id      = porch_service_account.backup.id
    token_endpoint = porch_service_account.backup.token_endpoint
  }
}

output "backup_client_secret" {
  value     = porch_service_account.backup.client_secret
  sensitive = true
}
