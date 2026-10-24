# Three families share one Porch installation.
#
# - Each family is a tenant with its own members, groups (family > parents, kids), roles and policies.
# - The Smiths host Jellyfin (OIDC, open to all members) and share it with the Joneses and the Taylors.
# - The Smiths also protect a photo gallery with forward-auth (access_mode "assigned"); only their parents may open it.
# - Kids hold media:read only between 07:00 and 21:00 UTC (a condition policy on their assignment); apps can ask
#   Porch's authorization API (or read the roles claim) before streaming.
# - A backup job of the Smiths runs as a service account with a read-only role.
#
# Authenticate with PORCH_ENDPOINT and PORCH_TOKEN (an installation admin automation token).

terraform {
  required_version = ">= 1.5"
  required_providers {
    porch = {
      source = "aurimasniekis/porch"
    }
  }
}

provider "porch" {}

locals {
  families = {
    smiths = {
      name   = "The Smiths"
      domain = "smith.example"
      people = {
        ada  = { name = "Ada Smith", parent = true }
        john = { name = "John Smith", parent = true }
        tim  = { name = "Tim Smith", parent = false }
        sue  = { name = "Sue Smith", parent = false }
      }
    }
    joneses = {
      name   = "The Joneses"
      domain = "jones.example"
      people = {
        mary = { name = "Mary Jones", parent = true }
        bob  = { name = "Bob Jones", parent = true }
        lily = { name = "Lily Jones", parent = false }
      }
    }
    taylors = {
      name   = "The Taylors"
      domain = "taylor.example"
      people = {
        kate = { name = "Kate Taylor", parent = true }
        max  = { name = "Max Taylor", parent = false }
      }
    }
  }

  # One entry per person: "<family>/<person>".
  people = merge([
    for fam, f in local.families : {
      for p, person in f.people : "${fam}/${p}" => {
        family = fam
        name   = person.name
        email  = "${p}@${f.domain}"
        parent = person.parent
      }
    }
  ]...)
}

# Tenants

resource "porch_tenant" "family" {
  for_each = local.families

  slug                 = each.key
  name                 = each.value.name
  session_idle_seconds = 14400
}

# People and memberships

resource "porch_user" "person" {
  for_each = local.people

  display_name  = each.value.name
  primary_email = each.value.email
}

resource "porch_tenant_member" "person" {
  for_each = local.people

  tenant  = porch_tenant.family[each.value.family].id
  user_id = porch_user.person[each.key].id
}

# Groups: family > parents, family > kids

resource "porch_group" "family" {
  for_each = local.families

  tenant = porch_tenant.family[each.key].id
  slug   = "family"
  name   = "Everyone in ${each.value.name}"
}

resource "porch_group" "parents" {
  for_each = local.families

  tenant = porch_tenant.family[each.key].id
  slug   = "parents"
  name   = "Parents"
}

resource "porch_group" "kids" {
  for_each = local.families

  tenant = porch_tenant.family[each.key].id
  slug   = "kids"
  name   = "Kids"
}

resource "porch_group_parent" "parents_in_family" {
  for_each = local.families

  tenant          = porch_tenant.family[each.key].id
  group_id        = porch_group.parents[each.key].id
  parent_group_id = porch_group.family[each.key].id
}

resource "porch_group_parent" "kids_in_family" {
  for_each = local.families

  tenant          = porch_tenant.family[each.key].id
  group_id        = porch_group.kids[each.key].id
  parent_group_id = porch_group.family[each.key].id
}

resource "porch_group_member" "person" {
  for_each = local.people

  tenant   = porch_tenant.family[each.value.family].id
  group_id = each.value.parent ? porch_group.parents[each.value.family].id : porch_group.kids[each.value.family].id
  user_id  = porch_tenant_member.person[each.key].user_id
}

# Roles and policies

resource "porch_role" "media_viewer" {
  for_each = local.families

  tenant      = porch_tenant.family[each.key].id
  slug        = "media-viewer"
  name        = "Media viewer"
  permissions = ["media:read"]
}

resource "porch_policy" "daytime" {
  for_each = local.families

  tenant     = porch_tenant.family[each.key].id
  slug       = "daytime"
  name       = "Daytime only (07:00-21:00 UTC)"
  effect     = "condition"
  expression = "context.hour >= 7 && context.hour < 21"
}

data "porch_role" "admin" {
  for_each = local.families

  tenant = porch_tenant.family[each.key].id
  slug   = "admin"
}

# Parents administer their own family.
resource "porch_role_assignment" "parents_admin" {
  for_each = local.families

  tenant       = porch_tenant.family[each.key].id
  subject_type = "group"
  subject_id   = porch_group.parents[each.key].id
  role_id      = data.porch_role.admin[each.key].id
}

# Everyone in the family may use media...
resource "porch_role_assignment" "parents_media" {
  for_each = local.families

  tenant       = porch_tenant.family[each.key].id
  subject_type = "group"
  subject_id   = porch_group.parents[each.key].id
  role_id      = porch_role.media_viewer[each.key].id
}

# ...but kids only during the day.
resource "porch_role_assignment" "kids_media" {
  for_each = local.families

  tenant              = porch_tenant.family[each.key].id
  subject_type        = "group"
  subject_id          = porch_group.kids[each.key].id
  role_id             = porch_role.media_viewer[each.key].id
  condition_policy_id = porch_policy.daytime[each.key].id
}

# Shared services: the Smiths' Jellyfin, shared with the other families

resource "porch_application" "jellyfin" {
  tenant      = porch_tenant.family["smiths"].id
  slug        = "jellyfin"
  name        = "Jellyfin"
  kind        = "oidc"
  description = "Family movie night"
  launch_url  = "https://jellyfin.smith.example"
  group       = "Media"
  icon_source = "dashboard_icons"
  icon_ref    = "jellyfin"

  disclosure = {
    groups = true
  }

  client = {
    redirect_uris  = ["https://jellyfin.smith.example/sso/OID/redirect/porch"]
    grant_types    = ["authorization_code", "refresh_token"]
    allowed_scopes = ["openid", "profile", "email", "groups"]
  }
}

resource "porch_application_share" "jellyfin" {
  for_each = toset(["joneses", "taylors"])

  tenant         = porch_tenant.family["smiths"].id
  application_id = porch_application.jellyfin.id
  target_tenant  = porch_tenant.family[each.key].id
}

# A forward-auth protected photo gallery only the Smith parents may open

resource "porch_application" "photos" {
  tenant      = porch_tenant.family["smiths"].id
  slug        = "photos"
  name        = "Family photos"
  kind        = "forward_auth"
  group       = "Media"
  access_mode = "assigned"
  hosts       = ["photos.smith.example"]
  launch_url  = "https://photos.smith.example"

  forward_headers = {
    "X-Remote-User"  = "email"
    "X-Remote-Name"  = "name"
    "X-Remote-Group" = "groups"
  }
}

resource "porch_role" "app_user" {
  tenant      = porch_tenant.family["smiths"].id
  slug        = "app-user"
  name        = "Application user"
  permissions = ["app:access"]
}

# app:access scoped to the photos application only.
resource "porch_role_assignment" "parents_photos" {
  tenant              = porch_tenant.family["smiths"].id
  subject_type        = "group"
  subject_id          = porch_group.parents["smiths"].id
  role_id             = porch_role.app_user.id
  scope_type          = "resource"
  scope_resource_type = "app"
  scope_resource_id   = porch_application.photos.id
}

# Machine access: the Smiths' nightly backup job

resource "porch_service_account" "backup" {
  tenant                   = porch_tenant.family["smiths"].id
  slug                     = "backup"
  name                     = "Nightly NAS backup"
  audiences                = ["https://nas.smith.example"]
  access_token_ttl_seconds = 300
  secret_version           = "1" # bump to rotate
}

resource "porch_role" "backup_reader" {
  tenant      = porch_tenant.family["smiths"].id
  slug        = "backup-reader"
  name        = "Backup reader"
  permissions = ["media:read", "photos:read"]
}

resource "porch_role_assignment" "backup" {
  tenant       = porch_tenant.family["smiths"].id
  subject_type = "service_account"
  subject_id   = porch_service_account.backup.id
  role_id      = porch_role.backup_reader.id
}
