#!/usr/bin/env bash
set -Eeuo pipefail

# Provider-agnostic cross-organization smoke. The two A2A Providers are
# expected to be started independently by their own operators; this script
# only starts a disposable Hub and uses public AgentCards plus the A2A
# contract. It never reads provider configuration or private runtime state.

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
provider_a_card=${AFH_PROVIDER_A_CARD_URL:-}
provider_b_card=${AFH_PROVIDER_B_CARD_URL:-}
provider_a_id=${AFH_PROVIDER_A_ID:-provider-a}
provider_b_id=${AFH_PROVIDER_B_ID:-provider-b}
tenant_id=${AFH_EXTERNAL_PROVIDER_TENANT:-external-provider-smoke}
hub_port=${AFH_EXTERNAL_PROVIDER_HUB_PORT:-18771}
timeout_seconds=${AFH_EXTERNAL_PROVIDER_TIMEOUT_SECONDS:-120}
go_bin=${GO_BIN:-go}
report_root=${AFH_EXTERNAL_PROVIDER_REPORT_ROOT:-}
run_dir=$(mktemp -d -t agent-federation-hub-external.XXXXXX)
hub_bin="$run_dir/federation-hub"
hub_pid=""

log() { printf '\n[%s] %s\n' "$(date '+%H:%M:%S')" "$*"; }
print_json() { local label=$1 payload=$2; printf '\n--- %s ---\n' "$label"; jq . <<<"$payload" || printf '%s\n' "$payload"; }
show_logs() { [[ -f "$run_dir/hub.log" ]] && { printf '\n--- Hub log ---\n' >&2; tail -n 120 "$run_dir/hub.log" >&2; }; }
fail() { printf '\nERROR: %s\n' "$*" >&2; show_logs; exit 1; }
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  [[ -n "$hub_pid" ]] && kill "$hub_pid" 2>/dev/null || true
  [[ -n "$hub_pid" ]] && wait "$hub_pid" 2>/dev/null || true
  if [[ -n "$report_root" && -f "$run_dir/report.json" ]]; then
    mkdir -p "$report_root"
    cp -- "$run_dir/report.json" "$report_root/report.json"
  fi
  if [[ "$status" -ne 0 ]]; then show_logs; fi
  rm -rf -- "$run_dir"
  exit "$status"
}
trap cleanup EXIT INT TERM

command -v curl >/dev/null || fail "curl is required"
command -v jq >/dev/null || fail "jq is required"
[[ -n "$provider_a_card" && -n "$provider_b_card" ]] || {
  cat >&2 <<'NOTICE'
Set AFH_PROVIDER_A_CARD_URL and AFH_PROVIDER_B_CARD_URL to the two
independently deployed /.well-known/agent-card.json URLs before running.
NOTICE
  exit 2
}
if [[ "$go_bin" == */* ]]; then
  [[ -x "$go_bin" ]] || fail "Go binary is not executable: $go_bin"
else
  command -v "$go_bin" >/dev/null || fail "Go binary not found: $go_bin"
fi

for card_url in "$provider_a_card" "$provider_b_card"; do
  parsed_scheme=${card_url%%:*}
  [[ "$parsed_scheme" == https || ${AFH_ALLOW_PRIVATE_AGENT_URLS:-} == 1 ]] || fail "Provider Card URL must use HTTPS (or set AFH_ALLOW_PRIVATE_AGENT_URLS=1 for local testing): $card_url"
done
if curl --silent --show-error --max-time 1 "http://127.0.0.1:$hub_port/healthz" >/dev/null 2>&1; then
  fail "local Hub port $hub_port is already serving HTTP; choose AFH_EXTERNAL_PROVIDER_HUB_PORT"
fi

cd "$repo_root"
log "Building disposable Hub for externally deployed Providers"
"$go_bin" build -o "$hub_bin" ./cmd/federation-hub >"$run_dir/build.log" 2>&1 || fail "Hub build failed"

log "Starting Hub in explicit development mode (test only)"
"$hub_bin" -listen "127.0.0.1:$hub_port" -journal "$run_dir/hub.journal" -agent-config "" \
  -auth-mode development -allow-private-agent-urls -reconcile-interval 2s >"$run_dir/hub.log" 2>&1 &
hub_pid=$!

wait_url() {
  local url=$1 label=$2
  for _ in $(seq 1 240); do
    if curl --fail --silent --show-error --max-time 2 "$url" >/dev/null; then log "$label ready"; return; fi
    sleep 0.25
  done
  fail "$label did not become ready: $url"
}
wait_url "http://127.0.0.1:$hub_port/healthz" "Hub liveness"

discover_and_register() {
  local id=$1 card_url=$2 card response skill
  log "Discovering Provider $id from its public AgentCard"
  card=$(curl --fail --silent --show-error "$card_url") || fail "AgentCard discovery failed for $id"
  print_json "Provider $id -> smoke script: AgentCard" "$card"
  jq -e '
    (.supportedInterfaces | any(.[]?; .protocolVersion == "1.0" and
      ((.protocolBinding == "JSON_RPC") or (.protocolBinding == "JSONRPC")))) and
    (.skills | type == "array" and length > 0)
  ' <<<"$card" >/dev/null || fail "Provider $id does not advertise A2A 1.0 JSON-RPC and a Skill"
  skill=$(jq -r '.skills[0].id // empty' <<<"$card")
  response=$(curl --fail-with-body --silent --show-error -X POST \
    -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant_id" \
    --data "$(jq -cn --arg id "$id" --arg url "$card_url" '{id:$id,cardUrl:$url}')" \
    "http://127.0.0.1:$hub_port/v1/agents") || fail "Provider registration failed for $id"
  print_json "Hub -> smoke script: registered $id" "$response"
  jq -e --arg id "$id" --arg skill "$skill" \
    '.id == $id and .protocolVersion == "1.0" and (.skills | index($skill)) != null' \
    <<<"$response" >/dev/null || fail "Hub registration contract failed for $id"
  DISCOVERED_SKILL=$skill
}

DISCOVERED_SKILL=""
discover_and_register "$provider_a_id" "$provider_a_card"
skill_a=$DISCOVERED_SKILL
discover_and_register "$provider_b_id" "$provider_b_card"
skill_b=$DISCOVERED_SKILL

submit() {
  local id=$1 skill=$2 prompt=$3 output=$4
  local payload response
  payload=$(jq -cn --arg id "$id" --arg skill "$skill" --arg prompt "$prompt" \
    '{agentId:$id,skill:$skill,text:$prompt}')
  log "Submitting opaque Task to $id"
  print_json "smoke script -> Hub: POST /v1/tasks ($id)" "$payload"
  response=$(curl --fail-with-body --silent --show-error -X POST \
    -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant_id" \
    --data "$payload" "http://127.0.0.1:$hub_port/v1/tasks") || fail "Task submission failed for $id"
  print_json "Hub -> smoke script: accepted Task ($id)" "$response"
  jq -e --arg id "$id" '.agentId == $id and (.remoteTaskId | length > 0) and (.remoteContextId | length > 0)' <<<"$response" >/dev/null || fail "remote correlation missing for $id"
  printf '%s\n' "$response" >"$output"
}

submit "$provider_a_id" "$skill_a" "Federation interoperability probe A" "$run_dir/task-a.json" & pid_a=$!
submit "$provider_b_id" "$skill_b" "Federation interoperability probe B" "$run_dir/task-b.json" & pid_b=$!
wait "$pid_a" || fail "Provider A task failed"
wait "$pid_b" || fail "Provider B task failed"

read_task() { curl --fail --silent --show-error -H "X-AFH-Tenant-ID: $tenant_id" "http://127.0.0.1:$hub_port/v1/tasks/$1"; }
wait_terminal() {
  local label=$1 input=$2 task_id state response last_state=""
  task_id=$(jq -r '.id' "$input")
  for attempt in $(seq 1 "$timeout_seconds"); do
    response=$(read_task "$task_id") || fail "$label Task read failed"
    state=$(jq -r '.state // "UNKNOWN"' <<<"$response")
    if [[ "$state" != "$last_state" || $((attempt % 15)) -eq 0 ]]; then log "$label state=$state (${attempt}s)"; last_state="$state"; fi
    if [[ "$state" == COMPLETED || "$state" == FAILED || "$state" == REJECTED || "$state" == CANCELED ]]; then
      printf '%s\n' "$response" >"$input.final"
      return
    fi
    sleep 1
  done
  fail "$label did not reach a terminal state within ${timeout_seconds}s"
}
wait_terminal "$provider_a_id" "$run_dir/task-a.json"
wait_terminal "$provider_b_id" "$run_dir/task-b.json"

task_a=$(cat "$run_dir/task-a.json.final")
task_b=$(cat "$run_dir/task-b.json.final")
print_json "Hub -> smoke script: final Task ($provider_a_id)" "$task_a"
print_json "Hub -> smoke script: final Task ($provider_b_id)" "$task_b"
for pair in "$provider_a_id:$task_a" "$provider_b_id:$task_b"; do
  id=${pair%%:*}; value=${pair#*:}
  jq -e --arg id "$id" '.agentId == $id and .state == "COMPLETED" and (.remoteTaskId | length > 0) and (.remoteContextId | length > 0) and ([.artifacts[]?] | length > 0)' <<<"$value" >/dev/null || fail "Provider $id did not complete with remote correlation and Artifact"
done

other_tenant=$(curl --fail --silent --show-error -H 'X-AFH-Tenant-ID: unrelated-tenant' "http://127.0.0.1:$hub_port/v1/agents") || fail "tenant isolation request failed"
jq -e 'length == 0' <<<"$other_tenant" >/dev/null || fail "cross-tenant Agent visibility was not denied"

jq -n --arg a "$provider_a_id" --arg b "$provider_b_id" --arg tenant "$tenant_id" \
  --slurpfile taskA "$run_dir/task-a.json.final" --slurpfile taskB "$run_dir/task-b.json.final" \
  '{version:1,evidenceStatus:"external-provider-local-run",tenant:$tenant,providers:[$a,$b],tasks:{($a):$taskA[0],($b):$taskB[0]},productionQualified:false}' >"$run_dir/report.json"
log "External Provider smoke passed: two independently deployed AgentCards, opaque Task correlation, Artifacts, and tenant isolation"
log "Evidence boundary: this local run does not establish partner identity, production trust, or managed availability"
