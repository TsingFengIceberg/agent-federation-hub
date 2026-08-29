#!/usr/bin/env bash
set -Eeuo pipefail

# Cross-domain smoke: one real ca-agent Provider and one independent A2A
# fixture share a Hub registry and tenant, while the Hub remains provider-opaque.
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
ca_agent_root=${CA_AGENT_ROOT:-/home/wugang/Data/Projects/ca-agent}
ca_backend="$ca_agent_root/backend"
ca_port=${AFH_CROSS_CA_PORT:-18741}
fixture_port=${AFH_CROSS_FIXTURE_PORT:-18742}
hub_port=${AFH_CROSS_HUB_PORT:-18743}
tenant=${AFH_CROSS_TENANT:-cross-domain-smoke}
prompt=${AFH_CROSS_CA_PROMPT:-"比较 Cursor 和 GitHub Copilot，重点关注团队采用成本、功能差异和适用场景"}
timeout_seconds=${AFH_CROSS_TASK_TIMEOUT_SECONDS:-300}
go_bin=${GO_BIN:-go}
run_dir=$(mktemp -d -t agent-federation-hub-cross.XXXXXX)
hub_bin="$run_dir/federation-hub"
fixture_bin="$run_dir/a2a-go-fixture"
ca_pid=""; fixture_pid=""; hub_pid=""

log() { printf '\n[%s] %s\n' "$(date '+%H:%M:%S')" "$*"; }
fail() { printf '\nERROR: %s\n' "$*" >&2; for file in "$run_dir"/*.log; do [[ -f "$file" ]] && { printf '\n--- %s ---\n' "$file" >&2; tail -n 100 "$file" >&2; }; done; exit 1; }
cleanup() { local status=$?; trap - EXIT INT TERM; for pid in "$hub_pid" "$fixture_pid" "$ca_pid"; do [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true; done; wait "$hub_pid" "$fixture_pid" "$ca_pid" 2>/dev/null || true; rm -rf -- "$run_dir"; exit "$status"; }
trap cleanup EXIT INT TERM

command -v curl >/dev/null || fail "curl is required"
command -v jq >/dev/null || fail "jq is required"
command -v uv >/dev/null || fail "uv is required"
[[ -d "$ca_backend" ]] || fail "ca-agent backend not found: $ca_backend"
if [[ "$go_bin" == */* ]]; then [[ -x "$go_bin" ]] || fail "Go binary is not executable: $go_bin"; else command -v "$go_bin" >/dev/null || fail "Go binary not found: $go_bin"; fi

cd "$repo_root"
log "Building Hub and independent A2A fixture"
"$go_bin" build -o "$hub_bin" ./cmd/federation-hub
"$go_bin" build -o "$fixture_bin" ./cmd/a2a-go-fixture

log "Starting real ca-agent Provider and independent fixture"
(
  cd "$run_dir"
  if [[ -f "$ca_agent_root/.env" ]]; then set -a; source "$ca_agent_root/.env"; set +a; fi
  export CI_AGENT_A2A_ENABLED=true CI_AGENT_A2A_AUTH_REQUIRED=false
  export CI_AGENT_A2A_PUBLIC_URL="http://127.0.0.1:$ca_port"
  export CI_AGENT_DB_PATH="$run_dir/competition.db" CI_AGENT_KNOWLEDGE_ROOT="$run_dir/knowledge"
  export PYTHONPATH="$ca_backend${PYTHONPATH:+:$PYTHONPATH}" UV_CACHE_DIR="${UV_CACHE_DIR:-$run_dir/uv-cache}"
  exec uv run --project "$ca_backend" --locked --no-dev --no-sync uvicorn app.main:app --host 127.0.0.1 --port "$ca_port"
) >"$run_dir/ca-agent.log" 2>&1 & ca_pid=$!
"$fixture_bin" -listen "127.0.0.1:$fixture_port" -public-url "http://127.0.0.1:$fixture_port" >"$run_dir/fixture.log" 2>&1 & fixture_pid=$!
"$hub_bin" -listen "127.0.0.1:$hub_port" -journal "$run_dir/hub.journal" -agent-config "" -auth-mode development -allow-private-agent-urls -reconcile-interval 2s >"$run_dir/hub.log" 2>&1 & hub_pid=$!

wait_url() { local url=$1 label=$2; for _ in $(seq 1 1200); do curl --fail --silent --max-time 1 "$url" >/dev/null && { log "$label ready: $url"; return; }; sleep 0.25; done; fail "$label did not become ready: $url"; }
wait_url "http://127.0.0.1:$ca_port/.well-known/agent-card.json" "ca-agent AgentCard"
wait_url "http://127.0.0.1:$fixture_port/.well-known/agent-card.json" "fixture AgentCard"
wait_url "http://127.0.0.1:$hub_port/healthz" "Hub liveness"

log "Registering both independent Providers in tenant $tenant"
for entry in "competitive-analysis-agent:$ca_port" "interop-fixture:$fixture_port"; do
  id=${entry%%:*}; port=${entry##*:}
  curl --fail-with-body --silent --show-error -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant" \
    --data "{\"id\":\"$id\",\"cardUrl\":\"http://127.0.0.1:$port/.well-known/agent-card.json\"}" \
    "http://127.0.0.1:$hub_port/v1/agents" | jq --arg id "$id" -e '.id == $id and .protocolVersion == "1.0"' >/dev/null || fail "registration failed: $id"
done

submit_and_wait() {
  local label=$1 payload=$2 task task_id state final last_state="" attempt
  log "Submitting $label Task"
  printf '%s\n' "$payload" | jq .
  task=$(curl --fail-with-body --silent --show-error -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant" --data "$payload" "http://127.0.0.1:$hub_port/v1/tasks") || fail "$label submission failed"
  printf '%s\n' "$task" | jq .
  task_id=$(jq -r '.id // empty' <<<"$task"); [[ -n "$task_id" ]] || fail "$label response missing task ID"
  for attempt in $(seq 1 "$timeout_seconds"); do
    final=$(curl --fail --silent -H "X-AFH-Tenant-ID: $tenant" "http://127.0.0.1:$hub_port/v1/tasks/$task_id") || fail "$label task read failed"
    state=$(jq -r '.state // "UNKNOWN"' <<<"$final")
    if [[ "$state" != "$last_state" || $((attempt % 15)) -eq 0 ]]; then log "$label state: $state (${attempt}s)"; last_state="$state"; fi
    [[ "$state" == COMPLETED || "$state" == FAILED || "$state" == REJECTED || "$state" == CANCELED ]] && break
    sleep 1
  done
  printf '%s\n' "$final" | jq .
  jq -e '.state == "COMPLETED" and (.remoteTaskId | length > 0) and ([.artifacts[]?] | length > 0)' <<<"$final" >/dev/null || fail "$label did not complete with a remote Task ID and Artifact"
}

submit_and_wait "deterministic fixture (skill route)" '{"skill":"interop-scenarios","text":"artifact-data"}'
submit_and_wait "real ca-agent" "$(jq -cn --arg text "$prompt" '{agentId:"competitive-analysis-agent",text:$text}')"
log "Cross-domain Hub smoke passed: real ca-agent + independent A2A fixture"
