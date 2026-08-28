#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
hub_port=${AFH_MULTI_HUB_PORT:-4300}
agent_a_port=${AFH_MULTI_AGENT_A_PORT:-4301}
agent_b_port=${AFH_MULTI_AGENT_B_PORT:-4302}
tenant_id=${AFH_MULTI_TENANT:-multi-agent-smoke}
run_dir=$(mktemp -d -t agent-federation-hub-multi.XXXXXX)
hub_bin="$run_dir/federation-hub"
agent_bin="$run_dir/a2a-go-fixture"
hub_pid=""
agent_a_pid=""
agent_b_pid=""

log() { printf '\n[%s] %s\n' "$(date '+%H:%M:%S')" "$*"; }
fail() {
  printf '\nERROR: %s\n' "$*" >&2
  [[ -f "$run_dir/hub.log" ]] && tail -n 80 "$run_dir/hub.log" >&2 || true
  exit 1
}
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  for pid in "$hub_pid" "$agent_a_pid" "$agent_b_pid"; do
    [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
  done
  wait "$hub_pid" "$agent_a_pid" "$agent_b_pid" 2>/dev/null || true
  rm -rf -- "$run_dir"
  exit "$status"
}
trap cleanup EXIT INT TERM

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v jq >/dev/null 2>&1 || fail "jq is required"
if [[ "$go_bin" == */* ]]; then
  [[ -x "$go_bin" ]] || fail "Go binary is not executable: $go_bin"
else
  command -v "$go_bin" >/dev/null 2>&1 || fail "Go binary not found: $go_bin"
fi

cd "$repo_root"
log "Building Hub and deterministic A2A Agent fixture"
"$go_bin" build -o "$hub_bin" ./cmd/federation-hub
"$go_bin" build -o "$agent_bin" ./cmd/a2a-go-fixture

log "Starting two independent A2A Agents"
"$agent_bin" -listen "127.0.0.1:$agent_a_port" -public-url "http://127.0.0.1:$agent_a_port" >"$run_dir/agent-a.log" 2>&1 &
agent_a_pid=$!
"$agent_bin" -listen "127.0.0.1:$agent_b_port" -public-url "http://127.0.0.1:$agent_b_port" >"$run_dir/agent-b.log" 2>&1 &
agent_b_pid=$!
"$hub_bin" -listen "127.0.0.1:$hub_port" -journal "$run_dir/hub.journal" -agent-config "" \
  -auth-mode development -allow-private-agent-urls -reconcile-interval 1s \
  >"$run_dir/hub.log" 2>&1 &
hub_pid=$!

wait_url() {
  local url=$1
  for _ in $(seq 1 100); do
    curl --fail --silent --max-time 1 "$url" >/dev/null && return 0
    sleep 0.1
  done
  fail "endpoint did not become ready: $url"
}

wait_url "http://127.0.0.1:$agent_a_port/.well-known/agent-card.json"
wait_url "http://127.0.0.1:$agent_b_port/.well-known/agent-card.json"
wait_url "http://127.0.0.1:$hub_port/healthz"

log "Registering both Agent Cards in tenant $tenant_id"
for entry in "agent-a:$agent_a_port" "agent-b:$agent_b_port"; do
  id=${entry%%:*}
  port=${entry##*:}
  curl --fail-with-body --silent --show-error -X POST \
    -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant_id" \
    --data "{\"id\":\"$id\",\"cardUrl\":\"http://127.0.0.1:$port/.well-known/agent-card.json\"}" \
    "http://127.0.0.1:$hub_port/v1/agents" \
    | jq -e --arg id "$id" '.id == $id and .protocolVersion == "1.0" and .streaming == true' >/dev/null \
    || fail "Agent registration failed: $id"
done

agents=$(curl --fail --silent -H "X-AFH-Tenant-ID: $tenant_id" "http://127.0.0.1:$hub_port/v1/agents")
printf '%s\n' "$agents" | jq .
jq -e 'length == 2 and (map(.id) | sort == ["agent-a", "agent-b"])' <<<"$agents" >/dev/null \
  || fail "registry did not return both tenant Agents"

log "Submitting one Task to each independent Agent (Agent A pauses for input)"
for id in agent-a agent-b; do
	prompt="artifact-text"
	if [[ "$id" == "agent-a" ]]; then
		prompt="input-required"
	fi
	task=$(curl --fail-with-body --silent --show-error -X POST \
		-H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant_id" \
		--data "{\"agentId\":\"$id\",\"text\":\"$prompt\"}" \
		"http://127.0.0.1:$hub_port/v1/tasks") || fail "Task submission failed: $id"
	printf '%s\n' "$task" | jq .
	task_id=$(jq -r '.id // empty' <<<"$task")
	jq -e --arg id "$id" '.agentId == $id and .delivery == "ACKNOWLEDGED" and (.remoteTaskId | length > 0) and (.remoteContextId | length > 0)' <<<"$task" >/dev/null \
		|| fail "Task was not acknowledged with a remote Task ID: $id"
	if [[ "$id" == "agent-a" ]]; then
		for _ in $(seq 1 100); do
			state=$(jq -r '.state // "UNKNOWN"' <<<"$task")
			log "Agent A Task state before continuation: $state"
			[[ "$state" == "INPUT_REQUIRED" ]] && break
			sleep 0.1
			task=$(curl --fail --silent -H "X-AFH-Tenant-ID: $tenant_id" \
				"http://127.0.0.1:$hub_port/v1/tasks/$task_id") || fail "Agent A Task read failed"
		 done
		jq -e '.state == "INPUT_REQUIRED"' <<<"$task" >/dev/null || fail "Agent A did not pause for input"
		log "Continuing Agent A Task with the existing remote Task and Context IDs"
		task=$(curl --fail-with-body --silent --show-error -X POST \
			-H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant_id" \
			--data '{"text":"artifact-data"}' \
			"http://127.0.0.1:$hub_port/v1/tasks/$task_id/messages") || fail "Task continuation failed"
		printf '%s\n' "$task" | jq .
		jq -e '.delivery == "ACKNOWLEDGED" and (.remoteTaskId | length > 0) and (.remoteContextId | length > 0) and (.state != "INPUT_REQUIRED")' <<<"$task" >/dev/null || fail "Task continuation was not acknowledged"
	fi
	final="$task"
  for _ in $(seq 1 100); do
    state=$(jq -r '.state // "UNKNOWN"' <<<"$final")
    log "Task $id state: $state"
    [[ "$state" == "COMPLETED" ]] && break
    sleep 0.1
    final=$(curl --fail --silent -H "X-AFH-Tenant-ID: $tenant_id" \
      "http://127.0.0.1:$hub_port/v1/tasks/$task_id") || fail "Task read failed: $id"
  done
  printf '%s\n' "$final" | jq .
  jq -e --arg id "$id" '.agentId == $id and .state == "COMPLETED" and ([.artifacts[]?] | length > 0)' <<<"$final" >/dev/null \
    || fail "Task did not complete with an Artifact: $id"
done

log "Checking tenant isolation"
other_tenant=$(curl --fail --silent -H 'X-AFH-Tenant-ID: other-tenant' "http://127.0.0.1:$hub_port/v1/agents")
jq -e 'length == 0' <<<"$other_tenant" >/dev/null || fail "Agent registry leaked across tenants"

log "Multi-Agent Hub smoke passed: two Agents, two Tasks, tenant isolation"
