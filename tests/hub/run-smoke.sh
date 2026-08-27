#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
hub_port=${AFH_HUB_SMOKE_PORT:-4200}
agent_port=${AFH_AGENT_SMOKE_PORT:-4201}
run_dir=$(mktemp -d -t agent-federation-hub-service.XXXXXX)
hub_bin="$run_dir/federation-hub"
agent_bin="$run_dir/a2a-go-fixture"
hub_pid=""
agent_pid=""

cleanup() {
  if [[ -n "$hub_pid" ]]; then
    kill "$hub_pid" 2>/dev/null || true
  fi
  if [[ -n "$agent_pid" ]]; then
    kill "$agent_pid" 2>/dev/null || true
  fi
  wait "$hub_pid" "$agent_pid" 2>/dev/null || true
  rm -rf -- "$run_dir"
}
trap cleanup EXIT

cd "$repo_root"
"$go_bin" build -o "$hub_bin" ./cmd/federation-hub
"$go_bin" build -o "$agent_bin" ./cmd/a2a-go-fixture

"$agent_bin" \
  -listen "127.0.0.1:$agent_port" \
  -public-url "http://127.0.0.1:$agent_port" \
  >"$run_dir/agent.log" 2>&1 &
agent_pid=$!

"$hub_bin" \
  -listen "127.0.0.1:$hub_port" \
  -journal "$run_dir/hub.journal" \
  -auth-mode development \
  -allow-private-agent-urls \
  -reconcile-interval 0 \
  >"$run_dir/hub.log" 2>&1 &
hub_pid=$!

attempts=0
until curl --fail --silent "http://127.0.0.1:$agent_port/.well-known/agent-card.json" >/dev/null; do
  attempts=$((attempts + 1))
  if [[ $attempts -ge 50 ]]; then
    exit 1
  fi
  sleep 0.1
done

attempts=0
until curl --fail --silent \
  -H 'X-AFH-Tenant-ID: smoke-tenant' \
  "http://127.0.0.1:$hub_port/v1/agents" >/dev/null; do
  attempts=$((attempts + 1))
  if [[ $attempts -ge 50 ]]; then
    exit 1
  fi
  sleep 0.1
done

curl --fail-with-body --silent --show-error \
  -X POST \
  -H 'Content-Type: application/json' \
  -H 'X-AFH-Tenant-ID: smoke-tenant' \
  --data "{\"id\":\"go-fixture\",\"cardUrl\":\"http://127.0.0.1:$agent_port/.well-known/agent-card.json\"}" \
  "http://127.0.0.1:$hub_port/v1/agents" \
  | jq -e '.protocolVersion == "1.0" and .protocolBinding == "JSONRPC" and .streaming == true' >/dev/null

task=$(curl --fail-with-body --silent --show-error \
  -X POST \
  -H 'Content-Type: application/json' \
  -H 'X-AFH-Tenant-ID: smoke-tenant' \
  --data '{"agentId":"go-fixture","text":"artifact-data"}' \
  "http://127.0.0.1:$hub_port/v1/tasks")

jq -e '
  .state == "COMPLETED" and
  .delivery == "ACKNOWLEDGED" and
  .remoteTaskId != "" and
  .remoteContextId != "" and
  .artifacts[0].parts[0].data.ok == true and
  (.pushTokenHash | not)
' <<<"$task" >/dev/null

task_id=$(jq -r '.id' <<<"$task")
events=$(curl --fail-with-body --silent --show-error \
  -H 'Accept: text/event-stream' \
  -H 'Last-Event-ID: 2' \
  -H 'X-AFH-Tenant-ID: smoke-tenant' \
  "http://127.0.0.1:$hub_port/v1/tasks/$task_id/events")

[[ "$events" == *"event: task.artifact"* ]]
[[ "$events" == *'"state":"COMPLETED"'* ]]

printf 'Federation Hub service smoke: pass\n'
