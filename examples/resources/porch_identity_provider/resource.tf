resource "porch_identity_provider" "google" {
  slug          = "google"
  kind          = "google"
  name          = "Google"
  client_id     = "1234567890-abc.apps.googleusercontent.com"
  client_secret = var.google_client_secret
  allowed_hds   = ["smith.example"] # Google Workspace domains; omit to accept any account
}

resource "porch_identity_provider" "keycloak" {
  slug          = "keycloak"
  kind          = "oidc"
  name          = "Keycloak"
  issuer        = "https://sso.example.com/realms/home"
  client_id     = "porch"
  client_secret = var.keycloak_client_secret
  scopes        = "openid email profile"
}

variable "google_client_secret" {
  type      = string
  sensitive = true
}

variable "keycloak_client_secret" {
  type      = string
  sensitive = true
}

# Register this redirect URI at Google.
output "google_callback_url" {
  value = porch_identity_provider.google.callback_url
}
