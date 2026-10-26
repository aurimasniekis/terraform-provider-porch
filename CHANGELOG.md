# Changelog

All notable changes to this provider are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.2.0] - 2026-10-26

For [Porch 0.3.0](https://github.com/aurimasniekis/go-porch/releases/tag/v0.3.0).

### Added

- `porch_automation_token`: issues an installation token (`admin`, `auditor` or the new read-only `inspector`) or a
  tenant token with a permission set, such as `["app:read", "service_account:read"]`, and keeps its secret in state
  (sensitive). The provider's own token must be an installation admin token created by an owner, or a tenant token
  issuing narrower tokens of its tenant. Expired or revoked tokens are issued again on the next apply; destroying the
  resource revokes the token. Needs Porch 0.3.0 or later.

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

[Unreleased]: https://github.com/aurimasniekis/terraform-provider-porch/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/aurimasniekis/terraform-provider-porch/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/aurimasniekis/terraform-provider-porch/releases/tag/v0.1.0
