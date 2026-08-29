#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
hub_port=${AFH_CE_HUB_PORT:-4410}
agent_port=${AFH_CE_AGENT_PORT:-4411}
collector_port=${AFH_CE_COLLECTOR_PORT:-4412}
tenant=${AFH_CE_TENANT:-cloudevents-smoke}
run_dir=$(mktemp -d -t agent-federation-hub-ce.XXXXXX)
hub_bin="$run_dir/federation-hub"
agent_bin="$run_dir/a2a-go-fixture"
hub_pid=""; agent_pid=""; collector_pid=""

log() { printf '\n[%s] %s\n' "$(date '+%H:%M:%S')" "$*"; }
fail() {
  printf '\nERROR: %s\n' "$*" >&2
  for file in "$run_dir"/*.log; do
    [[ -f "$file" ]] && { printf '\n--- %s ---\n' "$file" >&2; tail -n 100 "$file" >&2; }
  done
  exit 1
}
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  for pid in "$hub_pid" "$agent_pid" "$collector_pid"; do
    [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
  done
  wait "$hub_pid" "$agent_pid" "$collector_pid" 2>/dev/null || true
  rm -rf -- "$run_dir"
  exit "$status"
}
trap cleanup EXIT INT TERM

command -v curl >/dev/null || fail "curl is required"
command -v jq >/dev/null || fail "jq is required"
command -v openssl >/dev/null || fail "openssl is required"
command -v python3 >/dev/null || fail "python3 is required"
if [[ "$go_bin" == */* ]]; then [[ -x "$go_bin" ]] || fail "Go binary is not executable: $go_bin"; else command -v "$go_bin" >/dev/null || fail "Go binary not found: $go_bin"; fi

cat >"$run_dir/collector.py" <<'PY'
import json
import ssl
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

output, certificate, key, port = sys.argv[1:]

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        try:
            event = json.loads(body)
            with open(output, "a", encoding="utf-8") as handle:
                json.dump({"headers": dict(self.headers), "event": event}, handle)
                handle.write("\n")
            self.send_response(202)
        except Exception:
            self.send_response(400)
        self.end_headers()

    def log_message(self, format, *args):
        return

server = ThreadingHTTPServer(("127.0.0.1", int(port)), Handler)
context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
context.load_cert_chain(certificate, key)
server.socket = context.wrap_socket(server.socket, server_side=True)
server.serve_forever()
PY

openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj "/CN=127.0.0.1" -addext "subjectAltName=IP:127.0.0.1" \
  -keyout "$run_dir/collector.key" -out "$run_dir/collector.crt" >/dev/null 2>&1
touch "$run_dir/events.jsonl"

cd "$repo_root"
log "Building Hub and A2A fixture"
"$go_bin" build -o "$hub_bin" ./cmd/federation-hub
"$go_bin" build -o "$agent_bin" ./cmd/a2a-go-fixture

python3 "$run_dir/collector.py" "$run_dir/events.jsonl" "$run_dir/collector.crt" "$run_dir/collector.key" "$collector_port" >"$run_dir/collector.log" 2>&1 & collector_pid=$!
"$agent_bin" -listen "127.0.0.1:$agent_port" -public-url "http://127.0.0.1:$agent_port" >"$run_dir/agent.log" 2>&1 & agent_pid=$!
"$hub_bin" -listen "127.0.0.1:$hub_port" -journal "$run_dir/hub.journal" -agent-config "" \
  -auth-mode development -allow-private-agent-urls -reconcile-interval 1s \
  -outbox-cloudevents-url "https://127.0.0.1:$collector_port/events" \
  -outbox-cloudevents-ca-file "$run_dir/collector.crt" \
  -outbox-cloudevents-source "urn:test:agent-federation-hub" >"$run_dir/hub.log" 2>&1 & hub_pid=$!

for endpoint in \
  "https://127.0.0.1:$collector_port/events" \
  "http://127.0.0.1:$agent_port/.well-known/agent-card.json" \
  "http://127.0.0.1:$hub_port/healthz"; do
  ready=""
  for _ in $(seq 1 100); do
    if [[ "$endpoint" == https:* ]]; then
      kill -0 "$collector_pid" 2>/dev/null && ready=1 && break
    elif curl --fail --silent --max-time 1 "$endpoint" >/dev/null 2>&1; then
      ready=1; break
    fi
    sleep 0.1
  done
  [[ -n "$ready" ]] || fail "endpoint not ready: $endpoint"
done

log "Registering fixture and submitting a Task"
curl --fail --silent -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant" \
  --data "{\"id\":\"fixture\",\"cardUrl\":\"http://127.0.0.1:$agent_port/.well-known/agent-card.json\"}" \
  "http://127.0.0.1:$hub_port/v1/agents" | jq .
task=$(curl --fail --silent -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant" \
  --data '{"agentId":"fixture","text":"artifact-data"}' "http://127.0.0.1:$hub_port/v1/tasks")
printf '%s\n' "$task" | jq .
task_id=$(jq -r '.id' <<<"$task")

for _ in $(seq 1 100); do
  [[ -s "$run_dir/events.jsonl" ]] && break
  sleep 0.1
done
[[ -s "$run_dir/events.jsonl" ]] || fail "CloudEvents collector received no events"
jq -e --arg tenant "$tenant" --arg task "$task_id" \
  'select(.event.specversion == "1.0" and .event.source == "urn:test:agent-federation-hub" and .event.subject == $task and .event.data.tenantId == $tenant and (.headers["Idempotency-Key"] | startswith($tenant + ":")))' \
  "$run_dir/events.jsonl" >/dev/null || { cat "$run_dir/events.jsonl" >&2; fail "collector received an invalid CloudEvent envelope"; }
metrics=$(curl --fail --silent -H "X-AFH-Tenant-ID: $tenant" "http://127.0.0.1:$hub_port/metrics")
grep -q '^afh_outbox_published_total [1-9]' <<<"$metrics" || fail "Hub metrics did not record CloudEvent publication"
log "CloudEvents smoke passed: structured event delivery, tenant identity, and stable idempotency"
