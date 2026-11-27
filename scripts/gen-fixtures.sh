#!/usr/bin/env bash
# Regenerates the plan JSON fixtures in testdata/plans from the real Terraform
# workspaces in testdata/workspaces. Requires terraform >= 1.6 and network
# access to registry.terraform.io for the null, random and local providers.
#
# Scenarios:
#   clean.json         apply defaults, plan again: no changes (exit code 0)
#   update-only.json   only var.owner changes: one in-place update
#   refresh-only.json  inventory file deleted outside Terraform, -refresh-only plan
#   drift.json         inventory deleted plus changed variables: create, update,
#                      replace (including sensitive values) and delete
#   sensitive.json     (workspace "sensitive") objects with a sensitive key are
#                      replaced by a wholly unknown value and by a string
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WS="$ROOT/testdata/workspaces/drift"
WS_SENSITIVE="$ROOT/testdata/workspaces/sensitive"
OUT="$ROOT/testdata/plans"
TF="${TERRAFORM:-terraform}"

mkdir -p "$OUT"
cd "$WS"

cleanup() {
  for dir in "$WS" "$WS_SENSITIVE"; do
    (cd "$dir" && rm -rf .terraform terraform.tfstate terraform.tfstate.backup out ./*.tfplan)
  done
}
trap cleanup EXIT
cleanup

"$TF" init -input=false -no-color >/dev/null
"$TF" apply -input=false -no-color -auto-approve >/dev/null

# plan_json <name> <expected-exit-code> [plan args...]
plan_json() {
  local name="$1" expected="$2"
  shift 2
  set +e
  "$TF" plan -input=false -no-color -detailed-exitcode -out="$name.tfplan" "$@" >/dev/null
  local code=$?
  set -e
  if [[ "$code" != "$expected" ]]; then
    echo "plan for $name exited $code, expected $expected" >&2
    exit 1
  fi
  "$TF" show -json "$name.tfplan" >"$OUT/$name.json"
  echo "wrote testdata/plans/$name.json (plan exit code $code)"
}

plan_json clean 0
plan_json update-only 2 -var owner=data-platform

rm -f out/inventory.json
plan_json refresh-only 2 -refresh-only

plan_json drift 2 \
  -var release=2026.11.1 \
  -var owner=data-platform \
  -var enable_cache=false \
  -var password_rotation=2027-q1 \
  -var 'services=["api"]'

cd "$WS_SENSITIVE"
"$TF" init -input=false -no-color >/dev/null
"$TF" apply -input=false -no-color -auto-approve >/dev/null
plan_json sensitive 2 -var unknown_mode=unknown -var retype_mode=text
