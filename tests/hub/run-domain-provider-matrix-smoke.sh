#!/usr/bin/env bash
set -Eeuo pipefail

# Three independently deployed, domain-labelled A2A Providers exercise the
# same Hub contract. Their domain names only affect AgentCards and routing;
# the Hub never receives provider-internal state.
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
hub_port=${AFH_DOMAIN_HUB_PORT:-4323}
travel_port=${AFH_DOMAIN_TRAVEL_PORT:-4321}
procurement_port=${AFH_DOMAIN_PROCUREMENT_PORT:-4322}
incident_port=${AFH_DOMAIN_INCIDENT_PORT:-4324}
tenant=${AFH_DOMAIN_TENANT:-domain-matrix}
run_dir=$(mktemp -d -t afh-domain-matrix.XXXXXX)
hub_pid=""; travel_pid=""; procurement_pid=""; incident_pid=""

log() { printf '\n[%s] %s\n' "$(date '+%H:%M:%S')" "$*"; }
fail() {
  printf '\nERROR: %s\n' "$*" >&2
  for file in "$run_dir"/*.log; do [[ -f "$file" ]] && { printf '\n--- %s ---\n' "$file" >&2; tail -n 100 "$file" >&2; }; done
  exit 1
}
cleanup() {
  local status=$?; trap - EXIT INT TERM
  for pid in "$hub_pid" "$travel_pid" "$procurement_pid" "$incident_pid"; do [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true; done
  wait "$hub_pid" "$travel_pid" "$procurement_pid" "$incident_pid" 2>/dev/null || true
  rm -rf -- "$run_dir"
  exit "$status"
}
trap cleanup EXIT INT TERM

command -v curl >/dev/null || fail "curl is required"
command -v jq >/dev/null || fail "jq is required"
if [[ "$go_bin" == */* ]]; then [[ -x "$go_bin" ]] || fail "Go binary is not executable: $go_bin"; else command -v "$go_bin" >/dev/null || fail "Go binary not found: $go_bin"; fi

cd "$repo_root"
log "Building Hub and three independent domain Provider fixtures"
"$go_bin" build -o "$run_dir/hub" ./cmd/federation-hub
"$go_bin" build -o "$run_dir/provider" ./cmd/a2a-go-fixture

"$run_dir/provider" -listen "127.0.0.1:$travel_port" -public-url "http://127.0.0.1:$travel_port" \
  -name "Travel Research Agent" -description "Independent travel research Provider" -skills "travel-research" >"$run_dir/travel.log" 2>&1 & travel_pid=$!
"$run_dir/provider" -listen "127.0.0.1:$procurement_port" -public-url "http://127.0.0.1:$procurement_port" \
  -name "Procurement Agent" -description "Independent procurement Provider" -skills "procurement-finance" >"$run_dir/procurement.log" 2>&1 & procurement_pid=$!
"$run_dir/provider" -listen "127.0.0.1:$incident_port" -public-url "http://127.0.0.1:$incident_port" \
  -name "Incident Response Agent" -description "Independent incident response Provider" -skills "incident-response" >"$run_dir/incident.log" 2>&1 & incident_pid=$!
"$run_dir/hub" -listen "127.0.0.1:$hub_port" -journal "$run_dir/hub.journal" -agent-config "" \
  -auth-mode development -allow-private-agent-urls -reconcile-interval 1s >"$run_dir/hub.log" 2>&1 & hub_pid=$!

wait_url() {
  local url=$1 label=$2
  for _ in $(seq 1 200); do
    curl --fail --silent --max-time 1 "$url" >/dev/null && { log "$label ready"; return; }
    sleep 0.1
  done
  fail "$label did not become ready: $url"
}
wait_url "http://127.0.0.1:$travel_port/.well-known/agent-card.json" "Travel Provider"
wait_url "http://127.0.0.1:$procurement_port/.well-known/agent-card.json" "Procurement Provider"
wait_url "http://127.0.0.1:$incident_port/.well-known/agent-card.json" "Incident Provider"
wait_url "http://127.0.0.1:$hub_port/healthz" "Hub"

log "Registering all domain Providers in tenant $tenant"
register() {
  local id=$1 port=$2 skill=$3
  curl --fail-with-body --silent --show-error -X POST -H 'Content-Type: application/json' \
    -H "X-AFH-Tenant-ID: $tenant" --data "{\"id\":\"$id\",\"cardUrl\":\"http://127.0.0.1:$port/.well-known/agent-card.json\"}" \
    "http://127.0.0.1:$hub_port/v1/agents" | jq --arg id "$id" --arg skill "$skill" -e '.id == $id and (.skills | index($skill)) != null' >/dev/null \
    || fail "registration failed: $id"
}
register travel-agent "$travel_port" travel-research
register procurement-agent "$procurement_port" procurement-finance
register incident-agent "$incident_port" incident-response

submit() {
  local skill=$1 text=$2
  curl --fail-with-body --silent --show-error -X POST -H 'Content-Type: application/json' \
    -H "X-AFH-Tenant-ID: $tenant" --data "$(jq -cn --arg skill "$skill" --arg text "$text" '{skill:$skill,text:$text}')" \
    "http://127.0.0.1:$hub_port/v1/tasks"
}
wait_completed() {
  local task_id=$1 label=$2 response state
  for _ in $(seq 1 200); do
    response=$(curl --fail --silent -H "X-AFH-Tenant-ID: $tenant" "http://127.0.0.1:$hub_port/v1/tasks/$task_id") || fail "$label task read failed"
    state=$(jq -r '.state' <<<"$response")
    if [[ "$state" == COMPLETED ]]; then
      # Status and Artifact observations are independent events. A terminal
      # status may be visible for one polling interval before the final
      # Artifact mutation is committed, so wait for both invariants.
      if jq -e '(.remoteTaskId | length > 0) and ([.artifacts[]?] | length > 0)' <<<"$response" >/dev/null; then
        return
      fi
    fi
    [[ "$state" == FAILED || "$state" == REJECTED || "$state" == CANCELED ]] && fail "$label reached terminal failure: $state"
    sleep 0.1
  done
  fail "$label did not complete"
}

log "Submitting one task per domain through skill routing"
travel=$(submit travel-research artifact-data)
procurement=$(submit procurement-finance artifact-file)
incident=$(submit incident-response input-required)
travel_id=$(jq -r '.id' <<<"$travel")
procurement_id=$(jq -r '.id' <<<"$procurement")
incident_id=$(jq -r '.id' <<<"$incident")
wait_completed "$travel_id" "travel-research"
wait_completed "$procurement_id" "procurement-finance"
incident_state=""
for _ in $(seq 1 200); do
  incident_state=$(curl --fail --silent -H "X-AFH-Tenant-ID: $tenant" "http://127.0.0.1:$hub_port/v1/tasks/$incident_id" | jq -r '.state')
  [[ "$incident_state" == INPUT_REQUIRED ]] && break
  sleep 0.1
done
[[ "$incident_state" == INPUT_REQUIRED ]] || fail "incident Provider did not reach INPUT_REQUIRED"
log "Continuing the incident branch with the same remote Task/Context"
curl --fail-with-body --silent --show-error -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant" \
  --data '{"text":"artifact-data"}' "http://127.0.0.1:$hub_port/v1/tasks/$incident_id/messages" >/dev/null
wait_completed "$incident_id" "incident-response"

log "Stopping the procurement Provider and checking partial failure isolation"
kill "$procurement_pid" 2>/dev/null || true
wait "$procurement_pid" 2>/dev/null || true
set +e
curl --fail --silent --show-error -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant" \
  --data '{"skill":"procurement-finance","text":"provider-outage"}' "http://127.0.0.1:$hub_port/v1/tasks" >/dev/null
failure_status=$?
set -e
[[ $failure_status -ne 0 ]] || fail "unavailable procurement Provider unexpectedly accepted work"

log "Checking tenant isolation and Provider-opaque invariants"
other=$(curl --fail --silent -H 'X-AFH-Tenant-ID: other-tenant' "http://127.0.0.1:$hub_port/v1/agents")
jq -e 'length == 0' <<<"$other" >/dev/null || fail "tenant isolation failed"
agents=$(curl --fail --silent -H "X-AFH-Tenant-ID: $tenant" "http://127.0.0.1:$hub_port/v1/agents")
jq -e 'length == 3 and all(.[]; (.endpoint | startswith("http://127.0.0.1:")) and (.skills | length == 1))' <<<"$agents" >/dev/null || fail "domain AgentCard projection failed"
log "Domain Provider matrix smoke passed: three independent skills, artifacts, HITL continuation, and tenant isolation"
