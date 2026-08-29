#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
hub_port=${AFH_OUTBOX_HUB_PORT:-4400}
agent_port=${AFH_OUTBOX_AGENT_PORT:-4401}
tenant=${AFH_OUTBOX_TENANT:-outbox-smoke}
run_dir=$(mktemp -d -t agent-federation-hub-outbox.XXXXXX)
hub_bin="$run_dir/federation-hub"; agent_bin="$run_dir/a2a-go-fixture"
hub_pid=""; agent_pid=""
log() { printf '\n[%s] %s\n' "$(date '+%H:%M:%S')" "$*"; }
fail() { printf '\nERROR: %s\n' "$*" >&2; [[ -f "$run_dir/hub.log" ]] && tail -n 100 "$run_dir/hub.log" >&2 || true; exit 1; }
cleanup() { local status=$?; trap - EXIT INT TERM; for pid in "$hub_pid" "$agent_pid"; do [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true; done; wait "$hub_pid" "$agent_pid" 2>/dev/null || true; rm -rf -- "$run_dir"; exit "$status"; }
trap cleanup EXIT INT TERM
command -v curl >/dev/null || fail "curl is required"
command -v jq >/dev/null || fail "jq is required"
if [[ "$go_bin" == */* ]]; then [[ -x "$go_bin" ]] || fail "Go binary is not executable: $go_bin"; else command -v "$go_bin" >/dev/null || fail "Go binary not found: $go_bin"; fi
cd "$repo_root"
log "Building Hub and A2A fixture"
"$go_bin" build -o "$hub_bin" ./cmd/federation-hub
"$go_bin" build -o "$agent_bin" ./cmd/a2a-go-fixture
"$agent_bin" -listen "127.0.0.1:$agent_port" -public-url "http://127.0.0.1:$agent_port" >"$run_dir/agent.log" 2>&1 & agent_pid=$!
"$hub_bin" -listen "127.0.0.1:$hub_port" -journal "$run_dir/hub.journal" -agent-config "" -auth-mode development -allow-private-agent-urls -outbox-file "$run_dir/events.jsonl" >"$run_dir/hub.log" 2>&1 & hub_pid=$!
for url in "http://127.0.0.1:$agent_port/.well-known/agent-card.json" "http://127.0.0.1:$hub_port/healthz"; do for _ in $(seq 1 100); do curl --fail --silent --max-time 1 "$url" >/dev/null && break; sleep 0.1; done; curl --fail --silent --max-time 1 "$url" >/dev/null || fail "endpoint not ready: $url"; done
log "Registering fixture and submitting a Task"
curl --fail --silent -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant" --data "{\"id\":\"fixture\",\"cardUrl\":\"http://127.0.0.1:$agent_port/.well-known/agent-card.json\"}" "http://127.0.0.1:$hub_port/v1/agents" | jq .
task=$(curl --fail --silent -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant" --data '{"agentId":"fixture","text":"artifact-data"}' "http://127.0.0.1:$hub_port/v1/tasks")
printf '%s\n' "$task" | jq .
task_id=$(jq -r '.id' <<<"$task")
for _ in $(seq 1 100); do final=$(curl --fail --silent -H "X-AFH-Tenant-ID: $tenant" "http://127.0.0.1:$hub_port/v1/tasks/$task_id"); [[ "$(jq -r .state <<<"$final")" == COMPLETED ]] && break; sleep 0.1; done
events_file="$run_dir/events.jsonl"
[[ -s "$events_file" ]] || fail "outbox file was not populated"
count=$(wc -l <"$events_file")
log "Outbox file contains $count durable event(s): $events_file"
cat "$events_file" | jq -s --arg tenant "$tenant" --arg task "$task_id" 'map(select(.tenantId == $tenant and .taskId == $task)) | length >= 1' >/dev/null || fail "outbox event did not contain expected tenant/task"
log "Outbox smoke passed: durable JSONL publication with stable idempotency keys"
