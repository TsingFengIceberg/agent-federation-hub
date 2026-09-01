#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
matrix="$repo_root/tests/scenarios/scenarios.json"
go_bin=${GO_BIN:-go}
scenario_filter=${1:-}

log() { printf '[scenarios] %s\n' "$*"; }
fail() { printf '[scenarios] ERROR: %s\n' "$*" >&2; exit 2; }

command -v jq >/dev/null 2>&1 || fail "jq is required"
[[ -f "$matrix" ]] || fail "scenario matrix not found: $matrix"
jq -e '
  .schemaVersion == 1 and (.scenarios | length > 0) and
  all(.scenarios[];
    (.id | type == "string" and length > 0) and
    (.domain | type == "string" and length > 0) and
    (.providerCount | type == "number" and . >= 1) and
    (.requirements | type == "object") and
    ((.requirements | keys) as $keys | (["streaming","artifacts","humanApproval","events","recovery","partialFailure","tenantIsolation"] | all(.[]; . as $k | ($keys | index($k)) != null))) and
    (.hubInvariants | type == "array" and length > 0) and
    (.adapterEntrypoint | type == "string" and length > 0) and
    (.runner | type == "string" and length > 0) and
    (.status | IN("planned","runnable","verified","external"))
  )
' "$matrix" >/dev/null || fail "scenario matrix schema validation failed"

if [[ -z "$scenario_filter" || "$scenario_filter" == "--list" ]]; then
  jq -r '.scenarios[] | [.id, .status, (.providerCount|tostring), .runner] | @tsv' "$matrix" |
    while IFS=$'\t' read -r id status providers runner; do
      printf '[scenarios] %-28s status=%-9s providers=%s runner=%s\n' "$id" "$status" "$providers" "$runner"
    done
fi
[[ "$scenario_filter" == "--list" ]] && exit 0

run_one() {
  local id=$1 runner
  runner=$(jq -r --arg id "$id" '.scenarios[] | select(.id == $id) | .runner' "$matrix")
  [[ -n "$runner" && "$runner" != "null" ]] || fail "unknown scenario: $id"
  case "$runner" in
    tests/hub/run-federation-workflow-smoke.sh|tests/hub/run-domain-provider-matrix-smoke.sh|tests/hub/run-multi-runtime-provider-smoke.sh|tests/real-api/run-cross-domain-smoke.sh)
      log "running $id via $runner"
      GO_BIN="$go_bin" "$repo_root/$runner"
      ;;
    external)
      log "$id is defined but requires an external Provider adapter; not executed"
      ;;
    *) fail "scenario $id has unsupported runner $runner" ;;
  esac
}

if [[ -n "$scenario_filter" ]]; then
  run_one "$scenario_filter"
else
  run_one multi-provider-core
  run_one domain-provider-matrix
  run_one multi-runtime-provider
  if [[ ${AFH_RUN_EXTERNAL_SCENARIOS:-0} == 1 ]]; then
    run_one cross-domain-real-provider
  else
    log "cross-domain-real-provider is opt-in; set AFH_RUN_EXTERNAL_SCENARIOS=1"
  fi
fi

log "scenario matrix validation and selected runs passed"
