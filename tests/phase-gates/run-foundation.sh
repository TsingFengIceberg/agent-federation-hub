#!/usr/bin/env bash
set -Eeuo pipefail

# Reproducible entry point for the first four readiness phases. External
# qualification is recorded as skipped, never silently treated as a pass.
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
report_root=${AFH_PHASE_REPORT_ROOT:-"$repo_root/var/phase-gates"}
manifest="$report_root/manifest.json"
summary="$report_root/summary.md"
mkdir -p "$report_root"
cd "$repo_root"

if [[ "$go_bin" == */* ]]; then
  [[ -x "$go_bin" ]] || { printf 'Go binary is not executable: %s\n' "$go_bin" >&2; exit 2; }
else
  command -v "$go_bin" >/dev/null 2>&1 || { printf 'Go binary not found: %s\n' "$go_bin" >&2; exit 2; }
fi

declare -a names=() statuses=() evidence=() details=()
record() {
  names+=("$1")
  statuses+=("$2")
  evidence+=("$3")
  details+=("$4")
}
run_step() {
  local name=$1 evidence_status=$2
  shift 2
  printf '\n[phase-gates] %s\n' "$name"
  printf '[phase-gates] command:'
  printf ' %q' "$@"
  printf '\n'
  set +e
  "$@"
  local status=$?
  set -e
  if ((status == 0)); then
    record "$name" passed "$evidence_status" "exit=0"
    return 0
  fi
  record "$name" failed "$evidence_status" "exit=$status"
  return "$status"
}
skip_step() {
  record "$1" skipped "$2" "$3"
  printf '[phase-gates] %s: skipped (%s)\n' "$1" "$3"
}

failures=0
run_step 'Go unit and contract tests' verified-local "$go_bin" test ./... || failures=$((failures + 1))
run_step 'Go vet' verified-local "$go_bin" vet ./... || failures=$((failures + 1))
run_step 'Go race tests' verified-local "$go_bin" test -race ./internal/... || failures=$((failures + 1))
run_step 'Shell syntax checks' verified-local bash -c 'find tests -type f -name "*.sh" -print0 | xargs -0 -n1 bash -n' || failures=$((failures + 1))
run_step 'Generality scenario matrix' verified-local tests/scenarios/run-matrix.sh || failures=$((failures + 1))
run_step 'Partner-style trust integration' verified-local tests/trust/run-partner-integration.sh || failures=$((failures + 1))
if [[ ${AFH_RUN_DR_TESTS:-0} == 1 ]]; then
  run_step 'Local HA/DR drills' verified-local tests/dr/run-all.sh || failures=$((failures + 1))
else
  skip_step 'Local HA/DR drills' evidence-boundary 'set AFH_RUN_DR_TESTS=1 for Docker-backed HA/DR drills'
fi

if [[ -n "${A2A_TCK_DIR:-}" ]]; then
  run_step 'Pinned three-Binding A2A TCK matrix' verified-tck tests/conformance/run-matrix.sh || failures=$((failures + 1))
else
  skip_step 'Pinned three-Binding A2A TCK matrix' evidence-boundary 'A2A_TCK_DIR is not configured'
fi
if [[ ${AFH_RUN_POSTGRES_TESTS:-0} == 1 ]]; then
  run_step 'PostgreSQL integration' verified-local tests/postgres/run-integration.sh || failures=$((failures + 1))
else
  skip_step 'PostgreSQL integration' evidence-boundary 'set AFH_RUN_POSTGRES_TESTS=1 for Docker-backed database checks'
fi
if [[ ${AFH_RUN_EXTERNAL_TRUST_TESTS:-0} == 1 ]]; then
  run_step 'External partner trust profile' external-qualification tests/trust/run-external-profile.sh || failures=$((failures + 1))
else
  skip_step 'External partner trust profile' external-qualification 'set AFH_RUN_EXTERNAL_TRUST_TESTS=1 with partner endpoints and token material'
fi

"$go_bin" run ./cmd/hub-preflight --output json >"$report_root/preflight.json" || failures=$((failures + 1))
"$go_bin" run ./cmd/a2a-compat-report --output json >"$report_root/a2a-compatibility.json" || failures=$((failures + 1))

export AFH_PHASE_NAMES AFH_PHASE_STATUSES AFH_PHASE_EVIDENCE AFH_PHASE_DETAILS
AFH_PHASE_NAMES=$(printf '%s\n' "${names[@]}" | python3 -c 'import json,sys; print(json.dumps([x.rstrip("\n") for x in sys.stdin]))')
AFH_PHASE_STATUSES=$(printf '%s\n' "${statuses[@]}" | python3 -c 'import json,sys; print(json.dumps([x.rstrip("\n") for x in sys.stdin]))')
AFH_PHASE_EVIDENCE=$(printf '%s\n' "${evidence[@]}" | python3 -c 'import json,sys; print(json.dumps([x.rstrip("\n") for x in sys.stdin]))')
AFH_PHASE_DETAILS=$(printf '%s\n' "${details[@]}" | python3 -c 'import json,sys; print(json.dumps([x.rstrip("\n") for x in sys.stdin]))')
python3 - "$manifest" "$summary" "$failures" <<'PY'
import json
import os
import sys
from datetime import datetime, timezone

manifest_path, summary_path, failures = sys.argv[1:]
steps = [
    {"name": name, "status": status, "evidence": evidence, "detail": detail}
    for name, status, evidence, detail in zip(
        json.loads(os.environ["AFH_PHASE_NAMES"]),
        json.loads(os.environ["AFH_PHASE_STATUSES"]),
        json.loads(os.environ["AFH_PHASE_EVIDENCE"]),
        json.loads(os.environ["AFH_PHASE_DETAILS"]),
    )
]
payload = {
    "version": 1,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "evidenceStatus": "local-readiness-evidence",
    "failedSteps": int(failures),
    "productionQualified": False,
    "steps": steps,
    "qualificationNote": "External identity, managed HA/DR, and partner multi-organization evidence remain separate qualification gates.",
}
with open(manifest_path, "w", encoding="utf-8") as handle:
    json.dump(payload, handle, indent=2)
    handle.write("\n")
with open(summary_path, "w", encoding="utf-8") as handle:
    handle.write("# Foundation phase evidence\n\n")
    handle.write("> This report records execution evidence. It does not grant production qualification.\n\n")
    for step in steps:
        handle.write(f"- **{step['status']}** `{step['name']}`: {step['evidence']} ({step['detail']})\n")
    handle.write("\nProduction qualification remains false until external trust, managed HA/DR, and independent partner runs are evidenced.\n")
PY

printf '\n[phase-gates] manifest: %s\n' "$manifest"
printf '[phase-gates] summary: %s\n' "$summary"
if ((failures > 0)); then
  printf '[phase-gates] failed steps: %d\n' "$failures" >&2
  exit 1
fi
printf '[phase-gates] executable local foundation checks passed; external qualification remains open\n'
