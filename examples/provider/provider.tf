terraform {
  required_providers {
    porch = {
      source = "aurimasniekis/porch"
    }
  }
}

# The endpoint and token can also come from PORCH_ENDPOINT and PORCH_TOKEN.
provider "porch" {
  endpoint = "https://auth.example.com"
  token    = var.porch_token # an automation token (porch_at_…) issued by an installation owner

  # Optional: trust a private CA in addition to the system roots.
  # ca_file = "/etc/ssl/private-ca.pem"
}

variable "porch_token" {
  type      = string
  sensitive = true
}
