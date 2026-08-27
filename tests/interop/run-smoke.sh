#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
run_dir=$(mktemp -d -t agent-federation-hub-interop.XXXXXX)
hub_bin="$run_dir/interop-hub"
go_agent_bin="$run_dir/a2a-go-fixture"
python_project="$repo_root/tests/interop/python-agent"
go_agent_pid=""
python_agent_pid=""

cleanup() {
  if [[ -n "$go_agent_pid" ]]; then
    kill "$go_agent_pid" 2>/dev/null || true
  fi
  if [[ -n "$python_agent_pid" ]]; then
    kill "$python_agent_pid" 2>/dev/null || true
  fi
  wait "$go_agent_pid" "$python_agent_pid" 2>/dev/null || true
  rm -rf -- "$run_dir"
}
trap cleanup EXIT

cd "$repo_root"
"$go_bin" build -o "$hub_bin" ./cmd/interop-hub
"$go_bin" build -o "$go_agent_bin" ./cmd/a2a-go-fixture
uv sync --locked --project "$python_project"

"$go_agent_bin" >"$run_dir/go-agent.log" 2>&1 &
go_agent_pid=$!
"$python_project/.venv/bin/python" "$python_project/agent.py" \
  >"$run_dir/python-agent.log" 2>&1 &
python_agent_pid=$!

wait_for_card() {
  local card_url=$1
  local attempts=0
  until curl --fail --silent "$card_url" >/dev/null; do
    attempts=$((attempts + 1))
    if [[ $attempts -ge 50 ]]; then
      return 1
    fi
    sleep 0.1
  done
}

wait_for_card "http://127.0.0.1:4101/.well-known/agent-card.json"
wait_for_card "http://127.0.0.1:4102/.well-known/agent-card.json"

probe_agent() {
  local name=$1
  local base_url=$2
  local result
  local task_id
  local context_id
  local subscription_file
  local subscription_pid

  "$hub_bin" --agent-card-url "$base_url" --operation discover \
    | jq -e '.kind == "agent-card" and .card.supportedInterfaces[0].protocolVersion == "1.0"' >/dev/null

  "$hub_bin" --agent-card-url "$base_url" --operation send --text message \
    | jq -e '.kind == "message" and .event.parts[0].text == "fixture message response"' >/dev/null

  result=$("$hub_bin" --agent-card-url "$base_url" --operation stream --text artifact-data)
  jq -e -s 'map(.kind) == ["task", "status-update", "artifact-update", "status-update"]' \
    <<<"$result" >/dev/null
  jq -e -s '.[2].event.artifact.parts[0].data.ok == true and .[3].event.status.state == "TASK_STATE_COMPLETED"' \
    <<<"$result" >/dev/null

  "$hub_bin" --agent-card-url "$base_url" --operation send --text artifact-text \
    | jq -e '.event.artifacts[0].parts[0].text == "fixture task response: artifact-text"' >/dev/null
  "$hub_bin" --agent-card-url "$base_url" --operation send --text artifact-file \
    | jq -e '.event.artifacts[0].parts[0].filename == "fixture.txt" and .event.artifacts[0].parts[0].raw != null' >/dev/null
  "$hub_bin" --agent-card-url "$base_url" --operation send --text artifact-file-url \
    | jq -e '.event.artifacts[0].parts[0].url == "https://example.invalid/fixture.txt"' >/dev/null

  result=$("$hub_bin" --agent-card-url "$base_url" --operation send --text input-required)
  jq -e '.event.status.state == "TASK_STATE_INPUT_REQUIRED"' <<<"$result" >/dev/null
  task_id=$(jq -r '.event.id' <<<"$result")
  context_id=$(jq -r '.event.contextId' <<<"$result")
  "$hub_bin" --agent-card-url "$base_url" --operation send --text artifact-data \
    --task-id "$task_id" --context-id "$context_id" \
    | jq -e '.event.status.state == "TASK_STATE_COMPLETED"' >/dev/null

  result=$("$hub_bin" --agent-card-url "$base_url" --operation send --text long-running --return-immediately)
  task_id=$(jq -r '.event.id' <<<"$result")
  subscription_file="$run_dir/${name,,}-subscription.ndjson"
  "$hub_bin" --agent-card-url "$base_url" --operation subscribe \
    --task-id "$task_id" >"$subscription_file" &
  subscription_pid=$!
  sleep 0.2
  "$hub_bin" --agent-card-url "$base_url" --operation cancel --task-id "$task_id" \
    | jq -e '.event.status.state == "TASK_STATE_CANCELED"' >/dev/null
  wait "$subscription_pid"
  jq -e -s 'any(.[]; .event.status.state == "TASK_STATE_CANCELED")' \
    "$subscription_file" >/dev/null
  "$hub_bin" --agent-card-url "$base_url" --operation get --task-id "$task_id" \
    | jq -e '.event.status.state == "TASK_STATE_CANCELED"' >/dev/null

  printf '%s fixture: pass\n' "$name"
}

probe_agent "Go" "http://127.0.0.1:4101"
probe_agent "Python" "http://127.0.0.1:4102"
