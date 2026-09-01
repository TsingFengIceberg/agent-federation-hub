#!/usr/bin/env bash
set -Eeuo pipefail

# Cross-runtime Hub smoke: a Go SDK Provider and a Python SDK Provider are
# independently deployed and expose only their standard A2A AgentCards.
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
python_project="$repo_root/tests/interop/python-agent"
go_bin=${GO_BIN:-go}
go_port=${AFH_RUNTIME_GO_PORT:-4331}; python_port=${AFH_RUNTIME_PYTHON_PORT:-4332}; hub_port=${AFH_RUNTIME_HUB_PORT:-4333}
tenant=${AFH_RUNTIME_TENANT:-runtime-smoke}
run_dir=$(mktemp -d -t afh-runtime.XXXXXX)
hub_pid=""; go_pid=""; python_pid=""

log() { printf '\n[%s] %s\n' "$(date '+%H:%M:%S')" "$*"; }
fail() { printf '\nERROR: %s\n' "$*" >&2; for file in "$run_dir"/*.log; do [[ -f "$file" ]] && { printf '\n--- %s ---\n' "$file" >&2; tail -n 100 "$file" >&2; }; done; exit 1; }
cleanup() { local status=$?; trap - EXIT INT TERM; for pid in "$hub_pid" "$go_pid" "$python_pid"; do [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true; done; wait "$hub_pid" "$go_pid" "$python_pid" 2>/dev/null || true; rm -rf -- "$run_dir"; exit "$status"; }
trap cleanup EXIT INT TERM

for command_name in curl jq uv; do command -v "$command_name" >/dev/null || fail "$command_name is required"; done
if [[ "$go_bin" == */* ]]; then [[ -x "$go_bin" ]] || fail "Go binary is not executable: $go_bin"; else command -v "$go_bin" >/dev/null || fail "Go binary not found: $go_bin"; fi

cd "$repo_root"
log "Preparing Python A2A SDK fixture and building the Go fixture plus Hub"
UV_CACHE_DIR="${UV_CACHE_DIR:-$run_dir/uv-cache}" uv sync --locked --project "$python_project" >/dev/null
"$go_bin" build -o "$run_dir/hub" ./cmd/federation-hub
"$go_bin" build -o "$run_dir/go-provider" ./cmd/a2a-go-fixture
"$run_dir/go-provider" -listen "127.0.0.1:$go_port" -public-url "http://127.0.0.1:$go_port" \
  -name "Go Runtime Provider" -description "Independent Go A2A SDK Provider" -skills "software-development" >"$run_dir/go.log" 2>&1 & go_pid=$!
UV_CACHE_DIR="${UV_CACHE_DIR:-$run_dir/uv-cache}" "$python_project/.venv/bin/python" "$python_project/agent.py" \
  --port "$python_port" --public-url "http://127.0.0.1:$python_port" >"$run_dir/python.log" 2>&1 & python_pid=$!
"$run_dir/hub" -listen "127.0.0.1:$hub_port" -journal "$run_dir/hub.journal" -agent-config "" \
  -auth-mode development -allow-private-agent-urls -reconcile-interval 1s >"$run_dir/hub.log" 2>&1 & hub_pid=$!

wait_url() { local url=$1 label=$2; for _ in $(seq 1 300); do curl --fail --silent --max-time 1 "$url" >/dev/null && { log "$label ready"; return; }; sleep 0.1; done; fail "$label did not become ready: $url"; }
wait_url "http://127.0.0.1:$go_port/.well-known/agent-card.json" "Go Provider"
wait_url "http://127.0.0.1:$python_port/.well-known/agent-card.json" "Python Provider"
wait_url "http://127.0.0.1:$hub_port/healthz" "Hub"

register() {
  local id=$1 port=$2 skill=$3
  curl --fail-with-body --silent --show-error -X POST -H 'Content-Type: application/json' \
    -H "X-AFH-Tenant-ID: $tenant" --data "{\"id\":\"$id\",\"cardUrl\":\"http://127.0.0.1:$port/.well-known/agent-card.json\"}" \
    "http://127.0.0.1:$hub_port/v1/agents" | jq --arg id "$id" --arg skill "$skill" -e '.id == $id and (.skills | index($skill)) != null' >/dev/null \
    || fail "registration failed: $id"
}
log "Registering Go and Python Providers under one tenant"
register go-provider "$go_port" software-development
register python-provider "$python_port" interop-scenarios

submit() {
  local skill=$1 text=$2
  curl --fail-with-body --silent --show-error -X POST -H 'Content-Type: application/json' \
    -H "X-AFH-Tenant-ID: $tenant" --data "$(jq -cn --arg skill "$skill" --arg text "$text" '{skill:$skill,text:$text}')" \
    "http://127.0.0.1:$hub_port/v1/tasks"
}
wait_completed() {
  local task_id=$1 label=$2 response state
  for _ in $(seq 1 250); do
    response=$(curl --fail --silent -H "X-AFH-Tenant-ID: $tenant" "http://127.0.0.1:$hub_port/v1/tasks/$task_id") || fail "$label task read failed"
    state=$(jq -r '.state' <<<"$response")
    if [[ "$state" == COMPLETED ]]; then
      if jq -e '(.remoteTaskId | length > 0) and ([.artifacts[]?] | length > 0)' <<<"$response" >/dev/null; then
        return
      fi
    fi
    [[ "$state" == FAILED || "$state" == REJECTED || "$state" == CANCELED ]] && fail "$label reached terminal failure: $state"
    sleep 0.1
  done
  fail "$label did not complete"
}

log "Submitting Tasks to both runtimes"
go_task=$(submit software-development artifact-data)
python_task=$(submit interop-scenarios artifact-file)
go_id=$(jq -r '.id' <<<"$go_task")
python_id=$(jq -r '.id' <<<"$python_task")
wait_completed "$go_id" "Go Provider"
wait_completed "$python_id" "Python Provider"

log "Verifying Python INPUT_REQUIRED continuation preserves the same remote IDs"
paused=$(submit interop-scenarios input-required)
paused_id=$(jq -r '.id' <<<"$paused")
state=""
for _ in $(seq 1 200); do
  state=$(curl --fail --silent -H "X-AFH-Tenant-ID: $tenant" "http://127.0.0.1:$hub_port/v1/tasks/$paused_id" | jq -r '.state')
  [[ "$state" == INPUT_REQUIRED ]] && break
  sleep 0.1
done
[[ "$state" == INPUT_REQUIRED ]] || fail "Python Provider did not reach INPUT_REQUIRED"
before=$(curl --fail --silent -H "X-AFH-Tenant-ID: $tenant" "http://127.0.0.1:$hub_port/v1/tasks/$paused_id")
before_remote=$(jq -r '.remoteTaskId' <<<"$before")
before_context=$(jq -r '.remoteContextId' <<<"$before")
curl --fail-with-body --silent --show-error -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant" \
  --data '{"text":"artifact-data"}' "http://127.0.0.1:$hub_port/v1/tasks/$paused_id/messages" >/dev/null
after=$(curl --fail --silent -H "X-AFH-Tenant-ID: $tenant" "http://127.0.0.1:$hub_port/v1/tasks/$paused_id")
jq -e --arg remote "$before_remote" --arg context "$before_context" '.remoteTaskId == $remote and .remoteContextId == $context' <<<"$after" >/dev/null || fail "continuation changed remote correlation"
wait_completed "$paused_id" "Python continuation"

log "Stopping the Go Provider and verifying failure isolation"
kill "$go_pid" 2>/dev/null || true
wait "$go_pid" 2>/dev/null || true
set +e
curl --fail --silent --show-error -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant" \
  --data '{"skill":"software-development","text":"provider-outage"}' "http://127.0.0.1:$hub_port/v1/tasks" >/dev/null
failure_status=$?
set -e
[[ $failure_status -ne 0 ]] || fail "stopped Go Provider unexpectedly accepted work"
other=$(curl --fail --silent -H 'X-AFH-Tenant-ID: other-tenant' "http://127.0.0.1:$hub_port/v1/agents")
jq -e 'length == 0' <<<"$other" >/dev/null || fail "tenant isolation failed"
log "Multi-runtime Provider smoke passed: Go/Python SDKs, skill routing, Artifact, HITL, correlation, and failure isolation"
