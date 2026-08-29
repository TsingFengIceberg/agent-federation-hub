#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
hub_port=${AFH_PUSH_HUB_PORT:-4400}
agent_port=${AFH_PUSH_AGENT_PORT:-4401}
tenant_id=${AFH_PUSH_TENANT:-push-smoke}
run_dir=$(mktemp -d -t agent-federation-hub-push.XXXXXX)
hub_bin="$run_dir/federation-hub"
agent_bin="$run_dir/a2a-go-fixture"
hub_pid=""
agent_pid=""

log() { printf '\n[%s] %s\n' "$(date '+%H:%M:%S')" "$*"; }
fail() {
  printf '\nERROR: %s\n' "$*" >&2
  [[ -f "$run_dir/hub.log" ]] && tail -n 100 "$run_dir/hub.log" >&2 || true
  [[ -f "$run_dir/agent.log" ]] && tail -n 100 "$run_dir/agent.log" >&2 || true
  [[ -f "$run_dir/hub.journal" ]] && { log '--- Hub journal ---' >&2; tail -n 100 "$run_dir/hub.journal" >&2; } || true
  exit 1
}
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  [[ -n "$hub_pid" ]] && kill "$hub_pid" 2>/dev/null || true
  [[ -n "$agent_pid" ]] && kill "$agent_pid" 2>/dev/null || true
  wait "$hub_pid" "$agent_pid" 2>/dev/null || true
  rm -rf -- "$run_dir"
  exit "$status"
}
trap cleanup EXIT INT TERM

command -v curl >/dev/null 2>&1 || fail 'curl is required'
command -v jq >/dev/null 2>&1 || fail 'jq is required'
if [[ "$go_bin" == */* ]]; then
  [[ -x "$go_bin" ]] || fail "Go binary is not executable: $go_bin"
else
  command -v "$go_bin" >/dev/null 2>&1 || fail "Go binary not found: $go_bin"
fi

cd "$repo_root"
log 'Building Hub and Push-capable A2A Agent fixture'
"$go_bin" build -o "$hub_bin" ./cmd/federation-hub
"$go_bin" build -o "$agent_bin" ./cmd/a2a-go-fixture

log 'Starting provider with the SDK HTTP Push sender enabled'
"$agent_bin" -listen "127.0.0.1:$agent_port" -public-url "http://127.0.0.1:$agent_port" -push >"$run_dir/agent.log" 2>&1 &
agent_pid=$!
"$hub_bin" -listen "127.0.0.1:$hub_port" -public-base-url "http://127.0.0.1:$hub_port" \
  -journal "$run_dir/hub.journal" -agent-config '' -auth-mode development \
  -allow-private-agent-urls -reconcile-interval 0 >"$run_dir/hub.log" 2>&1 &
hub_pid=$!

wait_url() {
  local endpoint=$1
  for _ in $(seq 1 100); do
    curl --fail --silent --max-time 1 "$endpoint" >/dev/null && return 0
    sleep 0.1
  done
  fail "endpoint did not become ready: $endpoint"
}
wait_url "http://127.0.0.1:$agent_port/.well-known/agent-card.json"
wait_url "http://127.0.0.1:$hub_port/healthz"

log 'Registering the Push-capable Agent Card'
curl --fail-with-body --silent --show-error -X POST \
  -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant_id" \
  --data "{\"id\":\"push-fixture\",\"cardUrl\":\"http://127.0.0.1:$agent_port/.well-known/agent-card.json\"}" \
  "http://127.0.0.1:$hub_port/v1/agents" | jq -e '.pushNotifications == true' >/dev/null \
  || fail 'Agent Card did not advertise Push support'

log 'Submitting a Task with Hub-generated A2A Push callback'
task=$(curl --silent --show-error -X POST \
  -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant_id" \
  --data '{"agentId":"push-fixture","text":"artifact-data","enablePush":true}' \
  "http://127.0.0.1:$hub_port/v1/tasks") || fail 'Push-enabled Task submission transport failed'
printf '%s\n' "$task" | jq .
jq -e '(.id // "") != ""' <<<"$task" >/dev/null || fail 'Push-enabled Task submission returned an error response'
jq -e '.pushTokenHash != "" and .remoteTaskId != "" and .remoteContextId != ""' <<<"$task" >/dev/null \
  || fail 'Hub did not persist Push credentials and remote correlation IDs'
task_id=$(jq -r '.id' <<<"$task")

log 'Waiting for a terminal Task state observed through the Push receiver'
for _ in $(seq 1 120); do
  events=$(curl --fail --silent -H "X-AFH-Tenant-ID: $tenant_id" \
    "http://127.0.0.1:$hub_port/v1/tasks/$task_id/events") || fail 'Task event read failed'
  if jq -e 'any(.[]; .source == "a2a-push" and .type == "task.status" and .state == "COMPLETED")' <<<"$events" >/dev/null; then
    break
  fi
  sleep 0.1
done
jq -e 'any(.[]; .source == "a2a-push" and .type == "task.status" and .state == "COMPLETED")' <<<"$events" >/dev/null \
  || fail 'no completed task.status event was accepted from the A2A Push sender'
jq -e 'any(.[]; .source == "a2a-push" and .type == "task.artifact")' <<<"$events" >/dev/null \
  || fail 'no task.artifact event was accepted from the A2A Push sender'
log 'pass: provider SDK Push sender delivered authenticated status and Artifact events to the Hub'
