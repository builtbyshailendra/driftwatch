#!/usr/bin/env bash
# Entry point of the driftwatch composite action. Inputs arrive as INPUT_*
# environment variables; see action.yml.
set -euo pipefail

: "${DRIFTWATCH_BIN:?DRIFTWATCH_BIN must point at the driftwatch binary}"

report_dir="${RUNNER_TEMP:-/tmp}/driftwatch"
mkdir -p "$report_dir"
md="$report_dir/report.md"
json="$report_dir/report.json"

args=(check --dir . --format none --exit-code --markdown-out "$md" --json-out "$json")

if [[ -n "${INPUT_WORKSPACE:-}" ]]; then
  args+=(--workspace "$INPUT_WORKSPACE")
fi
if [[ -n "${INPUT_PLAN_JSON:-}" ]]; then
  args+=(--plan-json "$INPUT_PLAN_JSON")
fi
if [[ -n "${INPUT_IGNORE_FILE:-}" ]]; then
  args+=(--ignore-file "$INPUT_IGNORE_FILE")
fi
while IFS= read -r line; do
  line="${line#"${line%%[![:space:]]*}"}"
  line="${line%"${line##*[![:space:]]}"}"
  [[ -n "$line" ]] && args+=("--plan-arg=$line")
done <<<"${INPUT_PLAN_ARGS:-}"

if [[ "${INPUT_CREATE_ISSUE:-false}" == "true" ]]; then
  args+=(--github-issue --github-labels "${INPUT_ISSUE_LABELS:-driftwatch}")
  args+=("--close-resolved=${INPUT_CLOSE_RESOLVED:-true}")
fi
if [[ -n "${SLACK_WEBHOOK_URL:-}" ]]; then
  args+=(--slack)
fi

set +e
"$DRIFTWATCH_BIN" "${args[@]}"
code=$?
set -e

case "$code" in
  0) has_drift=false ;;
  2) has_drift=true ;;
  *)
    echo "::error::driftwatch failed with exit code $code"
    exit "$code"
    ;;
esac

{
  echo "has-drift=$has_drift"
  echo "report-markdown=$md"
  echo "report-json=$json"
} >>"${GITHUB_OUTPUT:-/dev/null}"

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  cat "$md" >>"$GITHUB_STEP_SUMMARY"
fi

if [[ "$has_drift" == "true" && "${INPUT_FAIL_ON_DRIFT:-false}" == "true" ]]; then
  echo "::error::Terraform drift detected"
  exit 1
fi
