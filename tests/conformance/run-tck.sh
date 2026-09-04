#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
tck_dir=${A2A_TCK_DIR:-}
report_dir=${A2A_TCK_REPORT_DIR:-"$repo_root/var/tck"}
transport=${A2A_TCK_TRANSPORT:-jsonrpc}
binding=${A2A_TCK_BINDING:-$transport}
profile_matrix="$repo_root/tests/conformance/profile-matrix.json"

# The TCK discovers the Agent Card over HTTP even when the selected A2A
# interface is gRPC. Keep discovery and gRPC listeners independent and give
# each matrix run a distinct default port so a stale process cannot make a run
# accidentally exercise a previous Binding.
case "$transport" in
  jsonrpc) default_port=4999 ;;
  http_json) default_port=4998 ;;
  grpc) default_port=4997 ;;
  *)
    printf 'unsupported A2A TCK transport: %s\n' "$transport" >&2
    exit 2
    ;;
esac
port=${A2A_TCK_PORT:-${A2A_TCK_HTTP_PORT:-$default_port}}
grpc_port=${A2A_TCK_GRPC_PORT:-5000}
if [[ "$binding" != "$transport" ]]; then
  printf 'TCK transport and binding selectors must match: transport=%s binding=%s\n' "$transport" "$binding" >&2
  exit 2
fi

if [[ -z "$tck_dir" || ! -d "$tck_dir" ]]; then
  printf 'A2A_TCK_DIR must point to a checked-out a2a-tck repository\n' >&2
  exit 2
fi

run_dir=$(mktemp -d -t agent-federation-hub-tck.XXXXXX)
sut_bin="$run_dir/a2a-tck-sut"
sut_pid=""
cleanup() {
  if [[ -n "$sut_pid" ]]; then
    kill "$sut_pid" 2>/dev/null || true
    wait "$sut_pid" 2>/dev/null || true
  fi
  rm -rf -- "$run_dir"
}
trap cleanup EXIT

cd "$repo_root"
readarray -t profile_pins < <(python3 - "$profile_matrix" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    profile = json.load(handle)
print(profile["tckCommit"])
print(profile["protocolSourceCommit"])
print(profile["tckProtocolCommit"])
PY
)
expected_tck_commit=${profile_pins[0]}
expected_protocol_commit=${profile_pins[1]}
expected_tck_protocol_commit=${profile_pins[2]}
"$go_bin" build -o "$sut_bin" ./cmd/a2a-tck-sut
"$sut_bin" --listen "127.0.0.1:$port" --public-url "http://127.0.0.1:$port" --grpc-listen "127.0.0.1:$grpc_port" --binding "$binding" >"$run_dir/sut.log" 2>&1 &
sut_pid=$!

wait_for_sut() {
  local card_url="http://127.0.0.1:$port/.well-known/agent-card.json"
  local attempts=80
  for _ in $(seq 1 "$attempts"); do
    if ! kill -0 "$sut_pid" 2>/dev/null; then
      printf 'A2A TCK SUT exited before readiness (binding=%s)\n' "$binding" >&2
      sed -n '1,160p' "$run_dir/sut.log" >&2 || true
      return 1
    fi
    if curl --fail --silent --show-error "$card_url" >"$run_dir/agent-card.json"; then
      return 0
    fi
    sleep 0.1
  done
  printf 'A2A TCK SUT did not expose AgentCard after %s attempts (binding=%s port=%s)\n' "$attempts" "$binding" "$port" >&2
  sed -n '1,160p' "$run_dir/sut.log" >&2 || true
  return 1
}

validate_card_binding() {
  python3 - "$run_dir/agent-card.json" "$binding" "$grpc_port" <<'PY'
import json
import sys
from urllib.parse import urlparse

path, selected, grpc_port = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    card = json.load(handle)
interfaces = card.get("supportedInterfaces") or []
expected = {"jsonrpc": "JSONRPC", "http_json": "HTTP+JSON", "grpc": "GRPC"}[selected]
matches = [item for item in interfaces if item.get("protocolBinding") == expected]
if len(matches) != 1:
    raise SystemExit(f"AgentCard binding mismatch: selected={expected}, declared={[item.get('protocolBinding') for item in interfaces]}")
endpoint = str(matches[0].get("url", ""))
if expected == "GRPC":
    if not endpoint or ":" not in endpoint or "/" in endpoint:
        raise SystemExit(f"gRPC AgentCard endpoint must be host:port, got {endpoint!r}")
    if not endpoint.endswith(":" + grpc_port):
        raise SystemExit(f"gRPC AgentCard endpoint {endpoint!r} does not match listener port {grpc_port}")
else:
    parsed = urlparse(endpoint)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise SystemExit(f"HTTP A2A AgentCard endpoint is invalid: {endpoint!r}")
print(f"AgentCard binding verified: {expected} -> {endpoint}")
PY
}

wait_for_sut
validate_card_binding

mkdir -p "$report_dir"
python_bin=${A2A_TCK_PYTHON:-.venv/bin/python}
if [[ "$python_bin" != /* ]]; then
  python_bin="$tck_dir/$python_bin"
fi
if [[ ! -x "$python_bin" ]]; then
  python_bin=python3
fi
set +e
(cd "$tck_dir" && "$python_bin" run_tck.py --sut-host "http://127.0.0.1:$port" --transport "$transport" --level must)
tck_status=$?
set -e

report_file="$report_dir/owned-sut-result.json"
compatibility_report="$tck_dir/reports/compatibility.json"
if [[ -f "$compatibility_report" ]]; then
  # Keep the upstream machine-readable report next to the run result. The TCK
  # checkout is an external working directory and may be reused for another
  # transport, so relying on its global reports/ path would make an old result
  # indistinguishable from the current run.
  cp -- "$compatibility_report" "$report_dir/compatibility-report.json"
  "$python_bin" - "$report_file" "$compatibility_report" "$tck_status" "$transport" "$binding" "$expected_tck_commit" "$expected_protocol_commit" "$expected_tck_protocol_commit" <<'PY'
import json
import sys

(
    output_path,
    compatibility_path,
    exit_code,
    transport,
    binding,
    expected_tck_commit,
    expected_protocol_commit,
    expected_tck_protocol_commit,
) = sys.argv[1:]
with open(compatibility_path, encoding="utf-8") as handle:
    compatibility = json.load(handle)
per_requirement = compatibility.get("per_requirement", {})
status_counts = {}
must_status_counts = {}
non_passing_must = []
for entry in per_requirement.values():
    status = entry.get("status", "UNKNOWN")
    status_counts[status] = status_counts.get(status, 0) + 1
    if entry.get("level") == "MUST":
        must_status_counts[status] = must_status_counts.get(status, 0) + 1
for requirement_id, entry in sorted(per_requirement.items()):
    if entry.get("level") == "MUST" and entry.get("status") != "PASS":
        non_passing_must.append({
            "id": requirement_id,
            "status": entry.get("status", "UNKNOWN"),
            "testIds": entry.get("test_ids", []),
        })
# Keep the four-state contract explicit even when the TCK omits a zero-count
# status from its JSON object. This makes downstream reports schema-stable.
for status in ("PASS", "SKIPPED", "NOT TESTED", "FAIL"):
    must_status_counts.setdefault(status, 0)
transport_result = compatibility.get("per_transport", {}).get(transport, {})
result = {
    "tckCommit": expected_tck_commit,
    "selectedProtocolCommit": expected_protocol_commit,
    "tckProtocolCommit": expected_tck_protocol_commit,
    "sut": "cmd/a2a-tck-sut",
    "transport": transport,
    "binding": binding,
    "requestedLevel": "must",
    "exitCode": int(exit_code),
    "waiverFile": "tests/conformance/tck-waivers.json",
    "interpretedAs": "evidence-with-waivers-and-skips",
    "compatibilitySummary": compatibility.get("summary", {}),
    "requirementStatusCounts": status_counts,
    "mustRequirementStatusCounts": must_status_counts,
    "nonPassingMustRequirements": non_passing_must,
    "completeMUSTCoverage": not any(must_status_counts.get(status, 0) for status in ("SKIPPED", "NOT TESTED", "FAIL")),
    "transportStatusCounts": {
        "PASS": int(transport_result.get("passed", 0)),
        "SKIPPED": int(transport_result.get("skipped", 0)),
        "FAIL": int(transport_result.get("failed", 0)),
        "TOTAL": int(transport_result.get("total", 0)),
    },
}
with open(output_path, "w", encoding="utf-8") as handle:
    json.dump(result, handle, indent=2)
    handle.write("\n")
PY
else
  cat >"$report_file" <<EOF
{
  "tckCommit": "$expected_tck_commit",
  "selectedProtocolCommit": "$expected_protocol_commit",
  "tckProtocolCommit": "$expected_tck_protocol_commit",
  "sut": "cmd/a2a-tck-sut",
  "transport": "$transport",
  "binding": "$binding",
  "requestedLevel": "must",
  "exitCode": $tck_status,
  "waiverFile": "tests/conformance/tck-waivers.json",
  "interpretedAs": "evidence-with-waivers-and-skips",
  "mustRequirementStatusCounts": {},
  "nonPassingMustRequirements": [],
  "transportStatusCounts": {}
}
EOF
fi

printf 'Owned A2A TCK run exit code: %d\n' "$tck_status"
printf 'Machine-readable result: %s\n' "$report_file"
verification_status=0
if [[ ${A2A_TCK_VERIFY_RESULT:-1} == 1 ]]; then
  if [[ ${A2A_TCK_REQUIRE_COMPLETE:-0} == 1 ]]; then
    export A2A_TCK_REQUIRE_COMPLETE
  fi
  "$repo_root/tests/conformance/verify-tck-result.sh" "$report_file" "$transport" || verification_status=$?
fi
if ((tck_status != 0)); then
  exit "$tck_status"
fi
if ((verification_status != 0)); then
  exit "$verification_status"
fi
exit "$tck_status"
