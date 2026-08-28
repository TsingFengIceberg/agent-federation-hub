#!/usr/bin/env bash
set -Eeuo pipefail

# Local integration smoke: start ca-agent, let the Hub discover it from its
# Agent Card, submit one real analysis, and print the resulting SSE/event data.

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
ca_agent_root=${CA_AGENT_ROOT:-/home/wugang/Data/Projects/ca-agent}
ca_agent_backend="$ca_agent_root/backend"
ca_agent_host=127.0.0.1
ca_agent_port=18731
hub_host=127.0.0.1
hub_port=18732
tenant_id=local-dev
agent_id=competitive-analysis-agent
prompt=${AFH_CA_AGENT_PROMPT:-"比较 Cursor 和 GitHub Copilot，重点关注团队采用成本、功能差异和适用场景"}
task_timeout=${AFH_CA_AGENT_TASK_TIMEOUT_SECONDS:-300}
startup_timeout=${AFH_CA_AGENT_STARTUP_TIMEOUT_SECONDS:-180}
go_bin=${GO_BIN:-go}
agent_config="$repo_root/agent_config.yaml"
run_dir=$(mktemp -d -t agent-federation-hub-ca-agent.XXXXXX)
uv_cache_dir=${UV_CACHE_DIR:-$run_dir/uv-cache}
ca_agent_runtime_dir="$ca_agent_backend"
isolated_ca_agent=false
hub_bin="$run_dir/federation-hub"
ca_agent_pid=""
hub_pid=""

log() {
  printf '\n[%s] %s\n' "$(date '+%H:%M:%S')" "$*"
}

print_json() {
  local label=$1
  local payload=$2
  printf '\n--- %s ---\n' "$label"
  if ! jq . <<<"$payload"; then
    printf '%s\n' "$payload"
  fi
}

print_process_logs() {
  local name=$1
  local path=$2
  if [[ -f "$path" ]]; then
    printf '\n--- %s log (last 80 lines) ---\n' "$name"
    tail -n 80 "$path" || true
  fi
}

show_logs_and_exit() {
  local message=$1
  printf '\nERROR: %s\n' "$message" >&2
  if [[ -f "$run_dir/ca-agent.log" ]]; then
    printf '\n--- ca-agent log (last 100 lines) ---\n' >&2
    tail -n 100 "$run_dir/ca-agent.log" >&2 || true
  fi
  if [[ -f "$run_dir/hub.log" ]]; then
    printf '\n--- Hub log (last 100 lines) ---\n' >&2
    tail -n 100 "$run_dir/hub.log" >&2 || true
  fi
  exit 1
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [[ -n "$hub_pid" ]]; then
    kill "$hub_pid" 2>/dev/null || true
    wait "$hub_pid" 2>/dev/null || true
  fi
  if [[ -n "$ca_agent_pid" ]]; then
    kill "$ca_agent_pid" 2>/dev/null || true
    wait "$ca_agent_pid" 2>/dev/null || true
  fi
  print_process_logs "ca-agent" "$run_dir/ca-agent.log"
  print_process_logs "Hub" "$run_dir/hub.log"
  if [[ "$status" -eq 0 ]]; then
    log "Cleaned up ca-agent and Hub processes"
  else
    printf '\nTemporary run directory will be removed after the diagnostic logs above.\n' >&2
  fi
  rm -rf -- "$run_dir"
  exit "$status"
}
trap cleanup EXIT INT TERM

log "Checking prerequisites and fixed local configuration"
command -v curl >/dev/null 2>&1 || show_logs_and_exit "curl is required"
command -v jq >/dev/null 2>&1 || show_logs_and_exit "jq is required"
command -v uv >/dev/null 2>&1 || show_logs_and_exit "uv is required"
[[ -d "$ca_agent_backend" ]] || show_logs_and_exit "ca-agent backend not found: $ca_agent_backend"
[[ -f "$agent_config" ]] || show_logs_and_exit "Hub agent config not found: $agent_config"
if [[ "$go_bin" == */* ]]; then
  [[ -x "$go_bin" ]] || show_logs_and_exit "Go binary is not executable: $go_bin"
else
  command -v "$go_bin" >/dev/null 2>&1 || show_logs_and_exit "Go binary not found: $go_bin"
fi
if curl --silent --show-error --max-time 1 \
  "http://$ca_agent_host:$ca_agent_port/.well-known/agent-card.json" >/dev/null 2>&1; then
  show_logs_and_exit "ca-agent port $ca_agent_port is already in use; stop the existing service first"
fi
if curl --silent --show-error --max-time 1 \
  "http://$hub_host:$hub_port/healthz" >/dev/null 2>&1; then
  show_logs_and_exit "Hub port $hub_port is already in use; stop the existing service first"
fi

log "Using ca-agent at $ca_agent_root"
log "ca-agent A2A URL: http://$ca_agent_host:$ca_agent_port"
log "Hub URL: http://$hub_host:$hub_port"
log "Tenant: $tenant_id; Agent: $agent_id"
log "Model/API credentials are inherited from ca-agent's existing environment; no secret is printed"
log "uv cache: $uv_cache_dir"
if [[ ! -d "$ca_agent_root/.ci-agent" || ! -w "$ca_agent_root/.ci-agent" ]]; then
  isolated_ca_agent=true
  ca_agent_runtime_dir="$run_dir/ca-agent-runtime"
  mkdir -p "$ca_agent_runtime_dir/.ci-agent"
  log "ca-agent .ci-agent is not writable; using isolated smoke runtime state"
fi

log "Building Hub binary"
cd "$repo_root"
"$go_bin" build -o "$hub_bin" ./cmd/federation-hub \
  >"$run_dir/hub-build.log" 2>&1 || show_logs_and_exit "Hub build failed"

log "Starting ca-agent A2A Provider (local no-auth mode)"
(
  cd "$ca_agent_runtime_dir"
  if [[ -f "$ca_agent_root/.env" ]]; then
    set -a
    # shellcheck disable=SC1091
    source "$ca_agent_root/.env"
    set +a
  fi
  export CI_AGENT_A2A_ENABLED=true
  export CI_AGENT_A2A_AUTH_REQUIRED=false
  export CI_AGENT_A2A_PUBLIC_URL="http://$ca_agent_host:$ca_agent_port"
  export UV_CACHE_DIR="$uv_cache_dir"
  if [[ "$isolated_ca_agent" == true ]]; then
    export CI_AGENT_DB_PATH="$run_dir/competition.db"
    export CI_AGENT_KNOWLEDGE_ROOT="$run_dir/knowledge"
    export PYTHONPATH="$ca_agent_backend${PYTHONPATH:+:$PYTHONPATH}"
  fi
  exec uv run --project "$ca_agent_backend" --locked --no-dev --no-sync \
    uvicorn app.main:app --host "$ca_agent_host" --port "$ca_agent_port"
) >"$run_dir/ca-agent.log" 2>&1 &
ca_agent_pid=$!

wait_for_url() {
  local url=$1
  local label=$2
  local attempts=0
  local max_attempts=$((startup_timeout * 4))
  (( max_attempts > 0 )) || show_logs_and_exit "startup timeout must be greater than zero"
  log "Waiting for $label: $url"
  until curl --fail --silent --connect-timeout 1 --max-time 2 "$url" >/dev/null; do
    attempts=$((attempts + 1))
    if (( attempts % 20 == 0 )); then
      log "Still waiting for $label (${attempts}/$max_attempts checks)"
    fi
    if [[ "$attempts" -ge "$max_attempts" ]]; then
      show_logs_and_exit "$label did not become ready within ${startup_timeout}s"
    fi
    sleep 0.25
  done
  log "$label is ready"
}

wait_for_url "http://$ca_agent_host:$ca_agent_port/.well-known/agent-card.json" "ca-agent AgentCard"

log "Reading and validating the advertised AgentCard"
log "HTTP request: GET http://$ca_agent_host:$ca_agent_port/.well-known/agent-card.json"
card=$(curl --fail --silent --show-error \
  "http://$ca_agent_host:$ca_agent_port/.well-known/agent-card.json")
print_json "ca-agent -> smoke script: AgentCard response" "$card"
if ! jq -e '
  (.supportedInterfaces | any(.[]; .protocolVersion == "1.0" and
    (.protocolBinding == "JSON_RPC" or .protocolBinding == "JSONRPC"))) and
  .capabilities.streaming == true and
  ([.skills[]?.id] | index("competitive-analysis")) != null
' <<<"$card" >/dev/null; then
  show_logs_and_exit "AgentCard does not satisfy the Hub's A2A 1.0 JSON-RPC/SSE policy"
fi
log "AgentCard check passed"

log "Starting Hub with agent_config.yaml and development inbound auth"
"$hub_bin" \
  --listen "$hub_host:$hub_port" \
  --journal "$run_dir/hub.journal" \
  --auth-mode development \
  --allow-private-agent-urls \
  --agent-config "$agent_config" \
  --remote-timeout "${task_timeout}s" \
  --reconcile-interval 5s \
  >"$run_dir/hub.log" 2>&1 &
hub_pid=$!

wait_for_url "http://$hub_host:$hub_port/healthz" "Hub health endpoint"

log "Checking that Hub startup discovery registered the remote Agent"
log "HTTP request: GET http://$hub_host:$hub_port/v1/agents (X-AFH-Tenant-ID: $tenant_id)"
agents=$(curl --fail --silent --show-error \
  -H "X-AFH-Tenant-ID: $tenant_id" \
  "http://$hub_host:$hub_port/v1/agents")
print_json "Hub -> smoke script: registered Agent response" "$agents"
if ! jq -e --arg id "$agent_id" 'any(.[]; .id == $id and .protocolVersion == "1.0" and .streaming == true)' <<<"$agents" >/dev/null; then
  show_logs_and_exit "Hub did not register the configured Agent"
fi
log "Hub registration check passed"

log "Submitting one real competitive-analysis Task"
printf 'Prompt: %s\n' "$prompt"
task_payload=$(jq -cn --arg agent "$agent_id" --arg text "$prompt" '{agentId: $agent, text: $text}')
print_json "smoke script -> Hub: POST /v1/tasks request" "$task_payload"
task_response=$(curl --fail-with-body --silent --show-error \
  --max-time "$task_timeout" \
  -X POST \
  -H 'Content-Type: application/json' \
  -H "X-AFH-Tenant-ID: $tenant_id" \
  --data "$task_payload" \
  "http://$hub_host:$hub_port/v1/tasks") || show_logs_and_exit "Hub Task submission failed"
print_json "Hub -> smoke script: Task acceptance/final response" "$task_response"
task_id=$(jq -r '.id // empty' <<<"$task_response")
[[ -n "$task_id" ]] || show_logs_and_exit "Hub response did not contain a Task ID"
log "Hub Task ID: $task_id"

log "Streaming Hub Task events until the remote Task reaches a terminal state"
log "HTTP request: GET /v1/tasks/$task_id/events?follow=true (Accept: text/event-stream)"
curl --fail-with-body --silent --show-error --no-buffer \
  --max-time "$task_timeout" \
  -H 'Accept: text/event-stream' \
  -H "X-AFH-Tenant-ID: $tenant_id" \
  "http://$hub_host:$hub_port/v1/tasks/$task_id/events?follow=true" \
  | sed 's/^/[SSE] /' || true
printf '\n'

log "Reading the final Hub Task state"
log "HTTP request: GET /v1/tasks/$task_id (X-AFH-Tenant-ID: $tenant_id)"
final_task=$(curl --fail --silent --show-error \
  -H "X-AFH-Tenant-ID: $tenant_id" \
  "http://$hub_host:$hub_port/v1/tasks/$task_id")
print_json "Hub -> smoke script: final Task response" "$final_task"
final_state=$(jq -r '.state // "UNKNOWN"' <<<"$final_task")
case "$final_state" in
  COMPLETED)
    if ! jq -e '(.remoteTaskId | length > 0) and ([.artifacts[]?] | length > 0)' <<<"$final_task" >/dev/null; then
      show_logs_and_exit "Task completed without a remote Task ID and Artifact"
    fi
    log "ca-agent -> Hub real A2A smoke passed: COMPLETED with Artifact"
    ;;
  INPUT_REQUIRED)
    show_logs_and_exit "Task requires an A2A continuation; this one-shot smoke prompt was not sufficient"
    ;;
  *)
    show_logs_and_exit "Task ended in unexpected state: $final_state"
    ;;
esac
