# An OpenID Connect client.
resource "porch_application" "jellyfin" {
  tenant     = porch_tenant.smiths.id
  slug       = "jellyfin"
  name       = "Jellyfin"
  kind       = "oidc"
  launch_url = "https://jellyfin.smith.example"

  disclosure = {
    groups = true
  }

  client = {
    redirect_uris  = ["https://jellyfin.smith.example/sso/OID/redirect/porch"]
    grant_types    = ["authorization_code", "refresh_token"]
    allowed_scopes = ["openid", "profile", "email", "groups"]
  }

  # Bump to rotate the client secret.
  secret_version = "1"
}

# A web app protected by Porch's forward-auth outpost.
resource "porch_application" "whoami" {
  tenant      = porch_tenant.smiths.id
  slug        = "whoami"
  name        = "Whoami"
  kind        = "forward_auth"
  access_mode = "assigned"
  hosts       = ["whoami.smith.example"]

  forward_headers = {
    "X-User"   = "sub"
    "X-Email"  = "email"
    "X-Family" = "tenant"
  }
}

output "jellyfin_client_id" {
  value = porch_application.jellyfin.client_id
}

output "jellyfin_client_secret" {
  value     = porch_application.jellyfin.client_secret
  sensitive = true
}
