#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
suffix=$$
nats_container="afh-nats-$suffix"
hub_port=${AFH_NATS_HUB_PORT:-4520}
agent_port=${AFH_NATS_AGENT_PORT:-4521}
subject=${AFH_NATS_SUBJECT:-afh.task-events}
tenant=${AFH_NATS_TENANT:-nats-smoke}
run_dir=$(mktemp -d -t afh-nats-smoke.XXXXXX)
hub_pid=""; agent_pid=""; subscriber_pid=""

log() { printf '[nats-smoke] %s\n' "$*"; }
fail() {
  log "ERROR: $*" >&2
  [[ -f "$run_dir/hub.log" ]] && { log '--- Hub log ---' >&2; tail -n 100 "$run_dir/hub.log" >&2; } || true
  [[ -f "$run_dir/messages.log" ]] && { log '--- NATS subscriber log ---' >&2; tail -n 100 "$run_dir/messages.log" >&2; } || true
  [[ -f "$run_dir/agent.log" ]] && { log '--- Agent log ---' >&2; tail -n 100 "$run_dir/agent.log" >&2; } || true
  exit 1
}
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  for pid in "$hub_pid" "$agent_pid" "$subscriber_pid"; do [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true; done
  wait "$hub_pid" "$agent_pid" "$subscriber_pid" 2>/dev/null || true
  docker rm -f "$nats_container" >/dev/null 2>&1 || true
  rm -rf -- "$run_dir"
  exit "$status"
}
trap cleanup EXIT INT TERM

command -v docker >/dev/null || fail 'docker is required'
command -v curl >/dev/null || fail 'curl is required'
command -v jq >/dev/null || fail 'jq is required'
[[ "$go_bin" == */* && -x "$go_bin" || "$go_bin" != */* ]] || fail 'Go binary is not executable'

cd "$repo_root"
log 'Building Hub and deterministic Agent fixture'
"$go_bin" build -o "$run_dir/federation-hub" ./cmd/federation-hub
"$go_bin" build -o "$run_dir/a2a-go-fixture" ./cmd/a2a-go-fixture
log 'Starting NATS Core event bus'
docker run --rm -d --name "$nats_container" -p 127.0.0.1::4222 nats:2.11-alpine >/dev/null
for _ in $(seq 1 120); do
  docker exec "$nats_container" sh -c 'wget -q -O- http://127.0.0.1:8222/varz' >/dev/null 2>&1 && break || true
  sleep 0.25
done
nats_endpoint=$(docker port "$nats_container" 4222/tcp)
nats_port=${nats_endpoint##*:}
touch "$run_dir/messages.log"
docker run --rm --network "container:$nats_container" natsio/nats-box:0.16.0 \
  nats sub "$subject" --server nats://127.0.0.1:4222 >"$run_dir/messages.log" 2>&1 & subscriber_pid=$!
"$run_dir/a2a-go-fixture" -listen "127.0.0.1:$agent_port" -public-url "http://127.0.0.1:$agent_port" >"$run_dir/agent.log" 2>&1 & agent_pid=$!
"$run_dir/federation-hub" -listen "127.0.0.1:$hub_port" -journal "$run_dir/hub.journal" -agent-config '' \
  -auth-mode development -allow-private-agent-urls -outbox-nats-url "nats://127.0.0.1:$nats_port" \
  -outbox-nats-subject "$subject" >"$run_dir/hub.log" 2>&1 & hub_pid=$!

for endpoint in "http://127.0.0.1:$agent_port/.well-known/agent-card.json" "http://127.0.0.1:$hub_port/healthz"; do
  for _ in $(seq 1 120); do curl -fsS --max-time 1 "$endpoint" >/dev/null && break; sleep 0.1; done
  curl -fsS --max-time 1 "$endpoint" >/dev/null || fail "endpoint not ready: $endpoint"
done
curl -fsS -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant" \
  --data "{\"id\":\"fixture\",\"cardUrl\":\"http://127.0.0.1:$agent_port/.well-known/agent-card.json\"}" \
  "http://127.0.0.1:$hub_port/v1/agents" >/dev/null
curl -fsS -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant" \
  --data '{"agentId":"fixture","text":"nats-event"}' "http://127.0.0.1:$hub_port/v1/tasks" >/dev/null
for _ in $(seq 1 120); do grep -q 'task.status' "$run_dir/messages.log" && break; sleep 0.1; done
grep -q 'task.status' "$run_dir/messages.log" || fail 'NATS subscriber received no Task event'
log 'pass: durable Hub Outbox published a Task event to NATS Core'
