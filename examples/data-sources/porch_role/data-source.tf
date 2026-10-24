# Built-in roles: "admin" (*:*) and "member".
data "porch_role" "admin" {
  tenant = "smiths"
  slug   = "admin"
}
