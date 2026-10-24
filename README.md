# Terraform provider for Porch

Manage a [Porch](https://github.com/aurimasniekis/go-porch) installation (self-hosted IAM/SSO) as code: tenants, users and
memberships, upstream identity providers and domain enrollment, OIDC and forward-auth applications (and cross-tenant
shares), groups, roles, policies, resources, role assignments and service accounts.

Built with [terraform-plugin-framework](https://github.com/hashicorp/terraform-plugin-framework) (protocol 6). Tested
against Porch commit `4fb6c45` with Terraform 1.11.

## Usage

```hcl
terraform {
  required_providers {
    porch = {
      source = "aurimasniekis/porch"
    }
  }
}

provider "porch" {
  endpoint = "https://auth.example.com" # or PORCH_ENDPOINT
  token    = var.porch_token            # or PORCH_TOKEN
  # ca_file = "/etc/ssl/private-ca.pem"  # or PORCH_CA_FILE
}
```

The provider authenticates with a Porch **automation token** (`porch_at_…`, ADR-0041), sent as
`Authorization: Bearer …`. An installation owner issues one (it needs a recent sign-in):

- an installation token with role `admin` manages everything, including installation-wide objects (tenants, users,
  identity providers, domain rules, cross-tenant shares);
- a tenant token carries one tenant and a list of permissions the issuer holds. Delegated administrators can issue
  them for their tenant; every change is then subject to Porch's escalation rule (ADR-0038) and fails with
  `403 privilege_escalation` when it would grant more than the issuer holds.

See [`examples/`](examples) for one example per resource and [`examples/families`](examples/families/main.tf) for a
complete scenario: three families, a shared Jellyfin, a forward-auth photo gallery, nested groups, roles, a daytime-only
condition policy for the kids and a backup service account.

### Resources and data sources

| Resource | Import ID |
|---|---|
| `porch_tenant` | `<tenant id or slug>` |
| `porch_user` | `<user id>` |
| `porch_tenant_member` | `<tenant>/<membership id>` |
| `porch_invite` | `<tenant>/<invite id>` |
| `porch_identity_provider` | `<provider id or slug>` |
| `porch_domain_rule` | `<tenant>/<rule id>` |
| `porch_application` | `<tenant>/<application id>` |
| `porch_application_share` | `<tenant>/<application id>/<target tenant>` |
| `porch_group` | `<tenant>/<group id>` |
| `porch_group_member` | `<tenant>/<group id>/<user id>` |
| `porch_group_parent` | `<tenant>/<group id>/<parent group id>` |
| `porch_role` | `<tenant>/<role id>` |
| `porch_role_assignment` | `<tenant>/<assignment id>` |
| `porch_resource` | `<tenant>/<resource id>` |
| `porch_policy` | `<tenant>/<policy id>` |
| `porch_service_account` | `<tenant>/<service account id>` |

Data sources: `porch_tenant` (by ID or slug), `porch_role` (by ID or slug, including the built-in `admin` and
`member` roles), `porch_user` (by primary email), `porch_group` (by ID or slug).

`tenant` arguments accept a tenant ID or slug. Full documentation is generated into [`docs/`](docs).

### Behaviour worth knowing

- **Optimistic concurrency.** Every mutable object's `ETag` is kept in private state and sent as `If-Match` on
  PATCH/PUT/DELETE, so a change made outside Terraform between refresh and apply fails with `412 precondition_failed`
  instead of being overwritten.
- **Safe retries.** Creates carry a random `Idempotency-Key` that is reused across the provider's own retries (network
  errors, 5xx, 429, `idempotency_in_progress`) with exponential backoff. Conditional writes (PUT/PATCH) are retried only
  when the server certainly did not apply them (429, 503).
- **Secrets are shown once.** `porch_application.client_secret` and `porch_service_account.client_secret` are only
  returned by Porch at creation; they are stored in state (sensitive) and never re-read. Change `secret_version` to
  rotate. If a create succeeded but its response was lost, Porch withholds the secret on the retry
  (`409 idempotent_secret_withheld`); the provider then reads the object from `Location` and rotates the secret
  (with a warning). After `terraform import` the secret is unknown until rotated. `porch_identity_provider.client_secret`
  is write-only in Porch and kept from configuration.
- **Drift.** Objects deleted outside Terraform are removed from state on refresh and planned for re-creation.
- **Immutable objects.** Domain rules, invitations, shares, group memberships, nesting edges and role assignments
  cannot be edited in Porch; any change replaces them. Slugs, kinds, client types and effects force replacement too.
- **Defaults.** Optional attributes default to Porch's server-side defaults (for example `grant_types =
  ["authorization_code"]`, `signing_alg = "ES256"`), so removing a setting from configuration restores the default.
- **References are IDs.** `includes`, `condition_policy_id`, `parent_id`, `provider_id` and `user_id` take Porch IDs.
  `role_id` in assignments and `group_id` in membership resources may also be slugs.

## Development

Requirements: Go 1.27, Terraform ≥ 1.5, `golangci-lint` v2, and a local checkout of Porch for acceptance tests.

```sh
make build          # bin/terraform-provider-porch
make test           # unit tests (API client against httptest, schema and config validation)
make lint           # golangci-lint
make docs           # regenerate docs/ with tfplugindocs
make porch-src      # export Porch @ PORCH_COMMIT from PORCH_REPO (default ../porch) into .porch-src
make porch-bin      # build bin/porch and bin/fakegoogle from .porch-src
make testacc        # acceptance tests (TF_ACC=1) against a real porch
make families       # init/plan/apply/plan/destroy examples/families against a throwaway porch
```

### Acceptance tests

`TF_ACC=1 go test ./...` starts a real `porch serve` from `TestMain` (development mode, free ports, temporary data
directory), completes the first-run setup, issues an installation `admin` token, an `auditor` token and a limited
tenant token, and points the provider at it. When `bin/fakegoogle` (Porch's test OpenID provider) is present, a
non-operator user also signs in through it and becomes a delegated administrator holding only `role:manage`, so the
`privilege_escalation` path is exercised end to end. Set `PORCH_BINARY`/`PORCH_FAKEGOOGLE` to use other binaries and
`TF_ACC_TERRAFORM_PATH` to choose the Terraform CLI (the harness uses the one on `PATH`, so nothing is downloaded).

Every resource is tested for create, read, in-place update (where Porch supports it), import with
`ImportStateVerify`, destroy, and drift (the object is deleted through the API and the next plan must re-create it).

### Using a local build

Point Terraform at the build with a CLI configuration file and `TF_CLI_CONFIG_FILE` (leave `~/.terraformrc` alone):

```hcl
provider_installation {
  dev_overrides {
    "aurimasniekis/porch" = "/path/to/terraform-provider-porch/bin"
  }
  direct {}
}
```

With `dev_overrides`, skip `terraform init` (or add a `filesystem_mirror` for the provider, as
[`scripts/verify-families.sh`](scripts/verify-families.sh) does).

## License

Apache License 2.0. Copyright 2026 Aurimas Niekis.
