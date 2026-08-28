#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
env_file=${AFH_ENV_FILE:-$repo_root/.env}
model_config_file=${AFH_MODEL_CONFIG_FILE:-$repo_root/model_config.yaml}
agent_host=${AFH_AGENT_HOST:-127.0.0.1}
agent_port=${AFH_AGENT_PORT:-4103}
public_url=${AFH_AGENT_PUBLIC_URL:-http://127.0.0.1:4103}
prompt=${AFH_TEST_PROMPT:-Reply with one short sentence confirming this is a live API response.}
stream_prompt=${AFH_STREAM_TEST_PROMPT:-Reply with two short sentences about interoperable agents.}
timeout_seconds=${AFH_TEST_TIMEOUT_SECONDS:-90}
temperature=${AFH_TEST_TEMPERATURE:-0}
python_project="$repo_root/tests/interop/python-agent"
python_bin="$python_project/.venv/bin/python"
run_dir=$(mktemp -d -t agent-federation-hub-live-api.XXXXXX)
hub_bin="$run_dir/interop-hub"
agent_pid=""

cleanup() {
  if [[ -n "$agent_pid" ]]; then
    kill "$agent_pid" 2>/dev/null || true
    wait "$agent_pid" 2>/dev/null || true
  fi
  rm -rf -- "$run_dir"
}
trap cleanup EXIT

cd "$repo_root"
uv sync --locked --project "$python_project"
"$python_bin" tests/real-api/settings.py \
  --env-file "$env_file" --model-config "$model_config_file" >/dev/null
"$go_bin" build -o "$hub_bin" ./cmd/interop-hub

"$python_bin" tests/real-api/agent.py \
  --env-file "$env_file" --model-config "$model_config_file" \
  --host "$agent_host" --port "$agent_port" --public-url "$public_url" \
  --provider-timeout "$timeout_seconds" --temperature "$temperature" \
  >"$run_dir/live-agent.log" 2>&1 &
agent_pid=$!

attempts=0
until curl --fail --silent \
  "$public_url/.well-known/agent-card.json" >/dev/null; do
  attempts=$((attempts + 1))
  if [[ $attempts -ge 100 ]]; then
    echo "live-provider Agent did not start; log follows" >&2
    sed -n '1,120p' "$run_dir/live-agent.log" >&2
    exit 1
  fi
  sleep 0.1
done

result=$("$hub_bin" --agent-card-url "$public_url" \
  --operation send --text "$prompt" --timeout "${timeout_seconds}s")
if ! jq -e '
    .kind == "task" and
    .event.status.state == "TASK_STATE_COMPLETED" and
    ([.event.artifacts[].parts[].text] | join("") | length) > 0
  ' <<<"$result" >/dev/null; then
  echo "live-provider unary assertion failed; sanitized A2A result follows" >&2
  printf '%s\n' "$result" >&2
  exit 1
fi

result=$("$hub_bin" --agent-card-url "$public_url" \
  --operation stream --text "$stream_prompt" \
  --timeout "${timeout_seconds}s")
if ! jq -e -s '
    any(.[]; .kind == "artifact-update") and
    any(.[]; .kind == "status-update" and
      .event.status.state == "TASK_STATE_COMPLETED") and
    ([.[] | select(.kind == "artifact-update") |
      .event.artifact.parts[].text] | join("") | length) > 0
  ' <<<"$result" >/dev/null; then
  echo "live-provider streaming assertion failed; sanitized A2A events follow" >&2
  printf '%s\n' "$result" >&2
  exit 1
fi

printf 'Live provider A2A unary and streaming smoke: pass\n'
