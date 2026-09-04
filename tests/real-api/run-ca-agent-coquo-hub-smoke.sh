#!/usr/bin/env bash
set -Eeuo pipefail

# Opt-in cross-provider smoke. It validates one real ca-agent A2A Provider and
# one independently deployed Coquo A2A Provider through the Hub's sequential
# template. Coquo defaults to an explicit deterministic fixture; set
# AFH_COQUO_PROFILE or AFH_COQUO_MODEL for a separately authorized model-backed
# run. The ca-agent leg always uses its existing configured model/API route.

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
ca_agent_root=${CA_AGENT_ROOT:-/home/wugang/Data/Projects/ca-agent}
ca_agent_backend="$ca_agent_root/backend"
coquo_root=${COQUO_ROOT:-/home/wugang/Data/Projects/coquo-code}
ca_port=${AFH_CA_COQUO_CA_PORT:-18761}
coquo_port=${AFH_CA_COQUO_COQUO_PORT:-18762}
hub_port=${AFH_CA_COQUO_HUB_PORT:-18763}
tenant_id=${AFH_CA_COQUO_TENANT:-ca-coquo-smoke}
prompt=${AFH_CA_COQUO_PROMPT:-"比较 Cursor 和 GitHub Copilot，重点关注团队采用成本、功能差异和适用场景"}
timeout_seconds=${AFH_CA_COQUO_TIMEOUT_SECONDS:-300}
startup_timeout=${AFH_CA_COQUO_STARTUP_TIMEOUT_SECONDS:-180}
go_bin=${GO_BIN:-go}
run_dir=$(mktemp -d -t agent-federation-hub-ca-coquo.XXXXXX)
coquo_workspace="$run_dir/coquo-workspace"
hub_bin="$run_dir/federation-hub"
ca_pid=""
coquo_pid=""
hub_pid=""
coquo_config_home=""

log() {
  printf '\n[%s] %s\n' "$(date '+%H:%M:%S')" "$*"
}

print_json() {
  local label=$1
  local payload=$2
  printf '\n--- %s ---\n' "$label"
  jq . <<<"$payload" || printf '%s\n' "$payload"
}

show_logs() {
  local file
  for file in "$run_dir"/*.log; do
    [[ -f "$file" ]] || continue
    printf '\n--- %s (last 100 lines) ---\n' "$(basename "$file")" >&2
    tail -n 100 "$file" >&2 || true
  done
}

fail() {
  printf '\nERROR: %s\n' "$*" >&2
  show_logs
  exit 1
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  for pid in "$hub_pid" "$coquo_pid" "$ca_pid"; do
    [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
  done
  wait "$hub_pid" "$coquo_pid" "$ca_pid" 2>/dev/null || true
  if [[ "$status" -ne 0 ]]; then
    show_logs
  fi
  rm -rf -- "$run_dir"
  exit "$status"
}
trap cleanup EXIT INT TERM

if [[ ${AFH_ALLOW_LIVE_CA_AGENT:-} != "1" ]]; then
  cat >&2 <<'NOTICE'
This smoke starts ca-agent with its existing model/API configuration and can incur
external API cost. Re-run only after authorization with AFH_ALLOW_LIVE_CA_AGENT=1.
NOTICE
  exit 2
fi

command -v curl >/dev/null || fail "curl is required"
command -v jq >/dev/null || fail "jq is required"
command -v uv >/dev/null || fail "uv is required"
[[ -d "$ca_agent_backend" ]] || fail "ca-agent backend not found: $ca_agent_backend"
[[ -d "$coquo_root" ]] || fail "Coquo project not found: $coquo_root"
if [[ "$go_bin" == */* ]]; then
  [[ -x "$go_bin" ]] || fail "Go binary is not executable: $go_bin"
else
  command -v "$go_bin" >/dev/null || fail "Go binary not found: $go_bin"
fi
for port in "$ca_port" "$coquo_port" "$hub_port"; do
  if curl --silent --show-error --max-time 1 "http://127.0.0.1:$port/healthz" >/dev/null 2>&1 ||
    curl --silent --show-error --max-time 1 "http://127.0.0.1:$port/.well-known/agent-card.json" >/dev/null 2>&1; then
    fail "local port $port is already serving HTTP; choose unused AFH_CA_COQUO_*_PORT values"
  fi
done

coquo_mode=(--fixture-provider)
coquo_evidence="deterministic Coquo fixture; no Coquo model evidence"
if [[ -n ${AFH_COQUO_PROFILE:-} || -n ${AFH_COQUO_MODEL:-} ]]; then
  coquo_mode=()
  [[ -n ${AFH_COQUO_PROFILE:-} ]] && coquo_mode+=(--profile "$AFH_COQUO_PROFILE")
  [[ -n ${AFH_COQUO_MODEL:-} ]] && coquo_mode+=(--model "$AFH_COQUO_MODEL")
  coquo_evidence="model-backed Coquo Provider selected by explicit local route"
  if [[ -n ${AFH_COQUO_CONFIG_HOME:-} ]]; then
    coquo_config_home=$AFH_COQUO_CONFIG_HOME
    [[ -d "$coquo_config_home" ]] || fail "AFH_COQUO_CONFIG_HOME is not a directory: $coquo_config_home"
  elif [[ -n ${XDG_CONFIG_HOME:-} ]]; then
    coquo_config_home=$XDG_CONFIG_HOME
  fi
else
  # The fixture must not consult a user's configured Provider profiles.
  coquo_config_home="$run_dir/coquo-config"
fi

log "Preparing the opt-in real ca-agent + Coquo A2A federation run"
log "ca-agent: http://127.0.0.1:$ca_port; Coquo: http://127.0.0.1:$coquo_port; Hub: http://127.0.0.1:$hub_port"
log "Tenant: $tenant_id"
log "Coquo mode: $coquo_evidence"
if [[ -n "$coquo_config_home" ]]; then
  log "Coquo configuration: explicit profile configuration home selected"
else
  log "Coquo configuration: default user configuration home selected"
fi
log "No credential values will be printed"
mkdir -p "$coquo_workspace"
[[ "$coquo_config_home" == "$run_dir/coquo-config" ]] && mkdir -p "$coquo_config_home"

log "Building the Hub binary"
(
  cd "$repo_root"
  "$go_bin" build -o "$hub_bin" ./cmd/federation-hub
) >"$run_dir/hub-build.log" 2>&1 || fail "Hub build failed"

log "Starting ca-agent A2A Provider in local no-auth mode"
(
  cd "$run_dir"
  if [[ -f "$ca_agent_root/.env" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$ca_agent_root/.env"
    set +a
  fi
  export CI_AGENT_A2A_ENABLED=true
  export CI_AGENT_A2A_AUTH_REQUIRED=false
  export CI_AGENT_A2A_PUBLIC_URL="http://127.0.0.1:$ca_port"
  export CI_AGENT_DB_PATH="$run_dir/competition.db"
  export CI_AGENT_KNOWLEDGE_ROOT="$run_dir/knowledge"
  export PYTHONPATH="$ca_agent_backend${PYTHONPATH:+:$PYTHONPATH}"
  export UV_CACHE_DIR="${UV_CACHE_DIR:-$run_dir/ca-uv-cache}"
  exec uv run --project "$ca_agent_backend" --locked --no-dev --no-sync \
    uvicorn app.main:app --host 127.0.0.1 --port "$ca_port"
) >"$run_dir/ca-agent.log" 2>&1 &
ca_pid=$!

log "Starting independently deployed Coquo A2A Provider"
(
  cd "$coquo_root"
  if [[ -n "$coquo_config_home" ]]; then
    export XDG_CONFIG_HOME="$coquo_config_home"
  else
    unset XDG_CONFIG_HOME
  fi
  export UV_CACHE_DIR="${UV_CACHE_DIR:-$run_dir/coquo-uv-cache}"
  exec uv run --project "$coquo_root" --locked --no-sync coquo-a2a \
    --workspace "$coquo_workspace" --host 127.0.0.1 --port "$coquo_port" \
    "${coquo_mode[@]}"
) >"$run_dir/coquo.log" 2>&1 &
coquo_pid=$!

log "Starting Hub with development inbound authentication"
"$hub_bin" \
  --listen "127.0.0.1:$hub_port" \
  --journal "$run_dir/hub.journal" \
  --agent-config "" \
  --auth-mode development \
  --allow-private-agent-urls \
  --remote-timeout "${timeout_seconds}s" \
  --reconcile-interval 2s \
  >"$run_dir/hub.log" 2>&1 &
hub_pid=$!

wait_for_url() {
  local url=$1
  local label=$2
  local attempt=0
  local maximum=$((startup_timeout * 4))
  while ! curl --fail --silent --connect-timeout 1 --max-time 2 "$url" >/dev/null; do
    attempt=$((attempt + 1))
    (( attempt < maximum )) || fail "$label did not become ready within ${startup_timeout}s"
    if (( attempt % 20 == 0 )); then
      log "Still waiting for $label ($attempt/$maximum checks)"
    fi
    sleep 0.25
  done
  log "$label is ready"
}

wait_for_url "http://127.0.0.1:$ca_port/.well-known/agent-card.json" "ca-agent AgentCard"
wait_for_url "http://127.0.0.1:$coquo_port/.well-known/agent-card.json" "Coquo AgentCard"
wait_for_url "http://127.0.0.1:$hub_port/healthz" "Hub liveness"

for provider in "ca-agent:$ca_port:competitive-analysis" "Coquo:$coquo_port:coding-assistance"; do
  name=${provider%%:*}
  remainder=${provider#*:}
  port=${remainder%%:*}
  skill=${remainder##*:}
  card=$(curl --fail --silent --show-error "http://127.0.0.1:$port/.well-known/agent-card.json")
  print_json "$name -> smoke script: AgentCard" "$card"
  jq -e --arg skill "$skill" '
    (.supportedInterfaces | any(.[]; .protocolVersion == "1.0" and
      (.protocolBinding == "JSON_RPC" or .protocolBinding == "JSONRPC"))) and
    .capabilities.streaming == true and
    ([.skills[]?.id] | index($skill)) != null
  ' <<<"$card" >/dev/null || fail "$name AgentCard does not satisfy A2A 1.0 JSON-RPC/SSE requirements"
done

register_provider() {
  local id=$1
  local port=$2
  local payload
  payload=$(jq -cn --arg id "$id" --arg card "http://127.0.0.1:$port/.well-known/agent-card.json" '{id:$id,cardUrl:$card}')
  log "Hub registration request for $id"
  print_json "smoke script -> Hub: POST /v1/agents" "$payload"
  response=$(curl --fail-with-body --silent --show-error \
    -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant_id" \
    --data "$payload" "http://127.0.0.1:$hub_port/v1/agents") || fail "registration failed for $id"
  print_json "Hub -> smoke script: registered $id" "$response"
  jq -e --arg id "$id" '.id == $id and .protocolVersion == "1.0" and .streaming == true' <<<"$response" >/dev/null || fail "Hub registration contract failed for $id"
}

register_provider "competitive-analysis-agent" "$ca_port"
register_provider "coquo-code-agent" "$coquo_port"

log "Inspecting the Hub-owned provider-opaque workflow template catalog"
templates=$(curl --fail --silent --show-error -H "X-AFH-Tenant-ID: $tenant_id" "http://127.0.0.1:$hub_port/v1/workflow-templates")
print_json "Hub -> smoke script: GET /v1/workflow-templates" "$templates"
jq -e 'any(.[]; .id == "sequential-pipeline" and .minAgents == 2)' <<<"$templates" >/dev/null || fail "sequential-pipeline template is unavailable"

workflow_payload=$(jq -cn --arg text "$prompt" '{
  name:"ca-agent-to-coquo-artifact-review",
  text:$text,
  parts:[{kind:"data",mediaType:"application/json",data:{target_products:["Cursor","GitHub Copilot"],source:"ca-coquo-smoke"}}],
  agents:[{agentId:"competitive-analysis-agent"},{agentId:"coquo-code-agent"}]
}')
log "Starting a sequential workflow: ca-agent report Artifact -> Coquo input"
print_json "smoke script -> Hub: POST /v1/workflow-templates/sequential-pipeline/runs" "$workflow_payload"
workflow_response=$(curl --fail-with-body --silent --show-error \
  -X POST -H 'Content-Type: application/json' -H "X-AFH-Tenant-ID: $tenant_id" \
  --data "$workflow_payload" "http://127.0.0.1:$hub_port/v1/workflow-templates/sequential-pipeline/runs") || fail "template workflow submission failed"
print_json "Hub -> smoke script: workflow acceptance" "$workflow_response"
workflow_id=$(jq -r '.workflow.id // empty' <<<"$workflow_response")
[[ -n "$workflow_id" ]] || fail "workflow response did not include an ID"

last_state=""
workflow_final=""
for attempt in $(seq 1 "$timeout_seconds"); do
  workflow_final=$(curl --fail --silent --show-error -H "X-AFH-Tenant-ID: $tenant_id" "http://127.0.0.1:$hub_port/v1/workflows/$workflow_id") || fail "workflow read failed"
  state=$(jq -r '.state // "UNKNOWN"' <<<"$workflow_final")
  if [[ "$state" != "$last_state" || $((attempt % 15)) -eq 0 ]]; then
    log "Workflow $workflow_id state: $state (${attempt}s)"
    last_state=$state
  fi
  case "$state" in
    COMPLETED)
      break
      ;;
    FAILED|PARTIALLY_FAILED|CANCELED)
      print_json "Hub -> smoke script: terminal workflow" "$workflow_final"
      fail "workflow entered unexpected state $state"
      ;;
  esac
  curl --fail-with-body --silent --show-error \
    -X POST -H "X-AFH-Tenant-ID: $tenant_id" \
    "http://127.0.0.1:$hub_port/v1/workflows/$workflow_id/reconcile" >/dev/null || fail "workflow reconciliation failed"
  sleep 1
done
[[ $(jq -r '.state // "UNKNOWN"' <<<"$workflow_final") == "COMPLETED" ]] || fail "workflow did not complete within ${timeout_seconds}s"
print_json "Hub -> smoke script: completed workflow" "$workflow_final"
jq -e '.state == "COMPLETED" and (.steps | length == 2) and all(.steps[]; .state == "COMPLETED" and (.taskId | length > 0))' <<<"$workflow_final" >/dev/null || fail "workflow topology or child state contract failed"

ca_task_id=$(jq -r '.steps[] | select(.id == "stage-1") | .taskId' <<<"$workflow_final")
coquo_task_id=$(jq -r '.steps[] | select(.id == "stage-2") | .taskId' <<<"$workflow_final")
ca_task=$(curl --fail --silent --show-error -H "X-AFH-Tenant-ID: $tenant_id" "http://127.0.0.1:$hub_port/v1/tasks/$ca_task_id")
coquo_task=$(curl --fail --silent --show-error -H "X-AFH-Tenant-ID: $tenant_id" "http://127.0.0.1:$hub_port/v1/tasks/$coquo_task_id")
print_json "Hub -> smoke script: ca-agent child Task" "$ca_task"
print_json "Hub -> smoke script: Coquo child Task" "$coquo_task"
for child in "$ca_task" "$coquo_task"; do
  jq -e '.state == "COMPLETED" and (.remoteTaskId | length > 0) and (.remoteContextId | length > 0) and ([.artifacts[]?] | length > 0)' <<<"$child" >/dev/null || fail "child Task lacks observable remote correlation or Artifact"
done

source_marker=$(jq -r '[.artifacts[]?.parts[]? | select(.kind == "text") | .text] | join("\n") | .[0:96]' <<<"$ca_task")
[[ -n "$source_marker" ]] || fail "ca-agent completed without a textual Artifact Part"
jq -e --arg marker "$source_marker" '
  [.artifacts[]?.parts[]? | select(.kind == "text") | .text] | join("\n") | contains($marker)
' <<<"$coquo_task" >/dev/null || fail "Coquo result does not demonstrate the observed ca-agent Artifact input"

log "Cross-Provider workflow passed: AgentCard discovery, opaque Task correlation, ca-agent Artifact projection, and Coquo completion"
log "Evidence boundary: $coquo_evidence"
