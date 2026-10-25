# Changelog

All notable changes to this provider are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-10-25

First release, for [Porch 0.1.0](https://github.com/aurimasniekis/go-porch/releases/tag/v0.1.0).

- Resources: `porch_tenant`, `porch_user`, `porch_tenant_member`, `porch_invite`, `porch_identity_provider`,
  `porch_domain_rule`, `porch_application` (OIDC and forward-auth, with launcher group and icon),
  `porch_application_share`, `porch_group`, `porch_group_member`, `porch_group_parent`, `porch_role`,
  `porch_role_assignment`, `porch_policy`, `porch_resource` and `porch_service_account`.
- Data sources: `porch_tenant`, `porch_user`, `porch_group` and `porch_role`.
- Every resource supports import; client secrets are returned once and kept in state as sensitive values.
- Authenticates with a Porch automation token (installation or tenant scoped); changes a tenant token is not allowed
  to make fail with `privilege_escalation`.

[Unreleased]: https://github.com/aurimasniekis/terraform-provider-porch/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/aurimasniekis/terraform-provider-porch/releases/tag/v0.1.0
