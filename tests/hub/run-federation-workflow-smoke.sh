#!/usr/bin/env bash
set -Eeuo pipefail

# Provider-opaque deterministic multi-Provider federation workflow smoke.
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}; hub_port=${AFH_WORKFLOW_HUB_PORT:-4313}
agent_a_port=${AFH_WORKFLOW_AGENT_A_PORT:-4311}; agent_b_port=${AFH_WORKFLOW_AGENT_B_PORT:-4312}
tenant_id=${AFH_WORKFLOW_TENANT:-workflow-smoke}; run_dir=$(mktemp -d -t afh-workflow.XXXXXX)
hub_bin="$run_dir/hub"; agent_bin="$run_dir/agent"; hub_pid=""; agent_a_pid=""; agent_b_pid=""
log() { printf '\n[%s] %s\n' "$(date '+%H:%M:%S')" "$*"; }
fail() { printf '\nERROR: %s\n' "$*" >&2; for f in "$run_dir"/*.log; do [[ -f "$f" ]] && tail -n 80 "$f" >&2; done; exit 1; }
cleanup() { local status=$?; trap - EXIT INT TERM; for p in "$hub_pid" "$agent_a_pid" "$agent_b_pid"; do [[ -n "$p" ]] && kill "$p" 2>/dev/null || true; done; wait "$hub_pid" "$agent_a_pid" "$agent_b_pid" 2>/dev/null || true; rm -rf -- "$run_dir"; exit "$status"; }
trap cleanup EXIT INT TERM
command -v curl >/dev/null || fail "curl is required"; command -v jq >/dev/null || fail "jq is required"
if [[ "$go_bin" == */* ]]; then [[ -x "$go_bin" ]] || fail "Go binary is not executable: $go_bin"; else command -v "$go_bin" >/dev/null || fail "Go binary not found: $go_bin"; fi
cd "$repo_root"; log "Building Hub and two independent Provider fixtures"; "$go_bin" build -o "$hub_bin" ./cmd/federation-hub; "$go_bin" build -o "$agent_bin" ./cmd/a2a-go-fixture
"$agent_bin" -listen "127.0.0.1:$agent_a_port" -public-url "http://127.0.0.1:$agent_a_port" >"$run_dir/agent-a.log" 2>&1 & agent_a_pid=$!
"$agent_bin" -listen "127.0.0.1:$agent_b_port" -public-url "http://127.0.0.1:$agent_b_port" >"$run_dir/agent-b.log" 2>&1 & agent_b_pid=$!
"$hub_bin" -listen "127.0.0.1:$hub_port" -journal "$run_dir/hub.journal" -agent-config "" -auth-mode development -allow-private-agent-urls -reconcile-interval 1s >"$run_dir/hub.log" 2>&1 & hub_pid=$!
wait_url() { local u=$1 label=$2; for _ in $(seq 1 200); do curl --fail --silent --max-time 1 "$u" >/dev/null && { log "$label ready"; return; }; sleep 0.1; done; fail "$label did not become ready: $u"; }
wait_url "http://127.0.0.1:$agent_a_port/.well-known/agent-card.json" "Provider A"; wait_url "http://127.0.0.1:$agent_b_port/.well-known/agent-card.json" "Provider B"; wait_url "http://127.0.0.1:$hub_port/healthz" "Hub"
log "Registering both AgentCards in tenant $tenant_id"
for entry in "agent-a:$agent_a_port" "agent-b:$agent_b_port"; do id=${entry%%:*}; port=${entry##*:}; curl --fail-with-body --silent --show-error -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant_id" --data "{\"id\":\"$id\",\"cardUrl\":\"http://127.0.0.1:$port/.well-known/agent-card.json\"}" "http://127.0.0.1:$hub_port/v1/agents" | jq -e --arg id "$id" '.id == $id and .streaming == true' >/dev/null || fail "registration failed: $id"; done
submit() { local id=$1 text=$2 output=$3; curl --fail-with-body --silent --show-error -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant_id" --data "{\"agentId\":\"$id\",\"text\":\"$text\"}" "http://127.0.0.1:$hub_port/v1/tasks" >"$output"; }
log "Fan-out: submitting two independent branches concurrently"; submit agent-a input-required "$run_dir/a.json" & a_submit=$!; submit agent-b artifact-data "$run_dir/b.json" & b_submit=$!; wait "$a_submit" || fail "branch A submission failed"; wait "$b_submit" || fail "branch B submission failed"; jq . "$run_dir/a.json"; jq . "$run_dir/b.json"
task_a=$(jq -r '.id' "$run_dir/a.json"); task_b=$(jq -r '.id' "$run_dir/b.json"); for f in "$run_dir/a.json" "$run_dir/b.json"; do jq -e '.delivery == "ACKNOWLEDGED" and (.remoteTaskId | length > 0) and (.remoteContextId | length > 0)' "$f" >/dev/null || fail "remote correlation missing in $f"; done
read_task() { curl --fail --silent -H "X-AFH-Tenant-ID: $tenant_id" "http://127.0.0.1:$hub_port/v1/tasks/$1"; }
wait_state() { local id=$1 expected=$2 label=$3 response state; for _ in $(seq 1 200); do response=$(read_task "$id") || fail "$label read failed"; state=$(jq -r '.state' <<<"$response"); log "$label state=$state"; if [[ "$state" == "$expected" ]]; then printf '%s\n' "$response" >"$run_dir/$label.json"; return; fi; sleep 0.1; done; fail "$label did not reach $expected"; }
wait_state "$task_a" INPUT_REQUIRED branch-a-input; log "Human approval: continuing branch A with existing remote IDs"; continued=$(curl --fail-with-body --silent --show-error -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant_id" --data '{"text":"artifact-data"}' "http://127.0.0.1:$hub_port/v1/tasks/$task_a/messages") || fail "continuation failed"; jq . <<<"$continued"; jq -e '.delivery == "ACKNOWLEDGED" and (.remoteTaskId | length > 0) and (.remoteContextId | length > 0)' <<<"$continued" >/dev/null || fail "continuation lost remote IDs"
wait_state "$task_a" COMPLETED branch-a-complete; wait_state "$task_b" COMPLETED branch-b-complete; log "Fan-in: verifying both branch snapshots and Artifacts"; jq -n --slurpfile a "$run_dir/branch-a-complete.json" --slurpfile b "$run_dir/branch-b-complete.json" '$a + $b' | jq -e 'length == 2 and all(.[]; .state == "COMPLETED" and ([.artifacts[]?] | length > 0))' >/dev/null || fail "fan-in invariant failed"
log "Partial failure: stop Provider B and confirm completed branch A remains readable"; kill "$agent_b_pid" 2>/dev/null || true; wait "$agent_b_pid" 2>/dev/null || true; set +e; curl --fail --silent --show-error -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant_id" --data '{"agentId":"agent-b","text":"provider-outage"}' "http://127.0.0.1:$hub_port/v1/tasks" >/dev/null; failure_status=$?; set -e; [[ $failure_status -ne 0 ]] || fail "unavailable Provider B unexpectedly accepted work"; survivor=$(read_task "$task_a"); jq -e '.state == "COMPLETED" and ([.artifacts[]?] | length > 0)' <<<"$survivor" >/dev/null || fail "successful branch was lost"
other=$(curl --fail --silent -H 'X-AFH-Tenant-ID: another-tenant' "http://127.0.0.1:$hub_port/v1/agents"); jq -e 'length == 0' <<<"$other" >/dev/null || fail "tenant isolation failed"; log "Multi-Provider workflow smoke passed: fan-out, HITL, fan-in, Artifacts, partial failure, and tenant isolation"
