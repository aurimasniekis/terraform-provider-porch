#!/usr/bin/env bash
# Runs examples/families against a throwaway development `porch serve`:
# terraform init → plan → apply → plan (must be empty) → destroy.
# The provider is used through a dev_overrides CLI config in a temporary TF_CLI_CONFIG_FILE (~/.terraformrc is never
# touched); a local filesystem mirror lets `terraform init` succeed for the unpublished provider.
# Needs: bin/porch and bin/terraform-provider-porch (make build porch-bin), terraform, python3, curl.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
porch="${PORCH_BINARY:-$root/bin/porch}"
provider="$root/bin/terraform-provider-porch"
free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'; }
json() { python3 -c 'import json,sys; print(json.load(sys.stdin)[sys.argv[1]])' "$1"; }

work=$(mktemp -d)
chmod 700 "$work"
cleanup() {
  [[ -n "${porch_pid:-}" ]] && kill "$porch_pid" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

PUB=$(free_port); MGMT=$(free_port)
cat > "$work/porch.yaml" <<YAML
mode: development
data_dir: $work/data
public_url: http://localhost:$PUB
http:
  listen: 127.0.0.1:$PUB
management:
  listen: 127.0.0.1:$MGMT
log:
  format: text
  level: warn
YAML
# Porch refuses unknown PORCH_* variables (the provider's, CI's PORCH_COMMIT, ...), so run it without any of them.
unset_porch=()
while IFS= read -r v; do unset_porch+=(-u "$v"); done < <(compgen -e | grep '^PORCH_' || true)
run_porch() { env ${unset_porch[@]+"${unset_porch[@]}"} "$porch" "$@"; }
out=$(run_porch admin setup-token -config "$work/porch.yaml" 2>&1) || { echo "porch admin setup-token failed:"; echo "$out"; exit 1; }
setup=$(grep -o 'porch_setup_[A-Za-z0-9_-]*' <<<"$out") || { echo "no setup token in output:"; echo "$out"; exit 1; }
run_porch serve -config "$work/porch.yaml" >"$work/porch.log" 2>&1 &
porch_pid=$!
ready=false
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$MGMT/readyz" >/dev/null && { ready=true; break; }; sleep 0.2; done
$ready || { echo "porch did not become ready:"; cat "$work/porch.log"; exit 1; }

origin="http://localhost:$PUB"
me=$(curl -sf -D "$work/headers" -X POST "$origin/api/v1/setup" -H "Origin: $origin" -H 'Content-Type: application/json' \
  -d "{\"token\":\"$setup\",\"display_name\":\"Owner\"}")
cookie=$(grep -i '^set-cookie: __Host-porch_session' "$work/headers" | sed 's/^[^:]*: //; s/;.*//' | tr -d '\r')
csrf=$(json csrf_token <<<"$me")
token=$(curl -sf -X POST "$origin/api/v1/installation/automation-tokens" -H "Cookie: $cookie" -H "X-Porch-CSRF: $csrf" \
  -H "Origin: $origin" -H 'Content-Type: application/json' -d '{"name":"families","role":"admin","expires_in_days":1}' | json token)

mirror="$work/mirror/registry.terraform.io/aurimasniekis/porch/0.0.1/$(go env GOOS)_$(go env GOARCH)"
mkdir -p "$mirror"
cp "$provider" "$mirror/terraform-provider-porch_v0.0.1"
cat > "$work/dev.tfrc" <<HCL
provider_installation {
  dev_overrides {
    "aurimasniekis/porch" = "$root/bin"
  }
  filesystem_mirror {
    path    = "$work/mirror"
    include = ["aurimasniekis/porch"]
  }
  direct {
    exclude = ["aurimasniekis/porch"]
  }
}
HCL
mkdir "$work/tf"
cp "$root"/examples/families/*.tf "$work/tf/"
cd "$work/tf"
export TF_CLI_CONFIG_FILE="$work/dev.tfrc" CHECKPOINT_DISABLE=1 TF_IN_AUTOMATION=1
export PORCH_ENDPOINT="$origin" PORCH_TOKEN="$token"
terraform init -input=false -no-color >/dev/null
terraform plan -input=false -no-color -out=tfplan | grep -E '^Plan:'
terraform apply -input=false -no-color tfplan | grep -E '^Apply complete'
set +e
terraform plan -input=false -no-color -detailed-exitcode >"$work/replan.txt"
code=$?
set -e
if [[ $code -ne 0 ]]; then
  echo "plan after apply is not empty (exit $code):"; cat "$work/replan.txt"; exit 1
fi
echo "Plan after apply: no changes."
terraform destroy -auto-approve -input=false -no-color | grep -E '^Destroy complete'
