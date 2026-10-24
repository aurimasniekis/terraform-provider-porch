resource "porch_tenant" "smiths" {
  slug = "smiths"
  name = "The Smiths"

  # Optional per-tenant session limits (0 = installation default).
  session_idle_seconds     = 7200
  session_absolute_seconds = 604800
}
