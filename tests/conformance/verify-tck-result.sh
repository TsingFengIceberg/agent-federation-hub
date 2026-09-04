#!/usr/bin/env bash
set -Eeuo pipefail

# Verify one persisted TCK run against the repository-owned profile matrix.
# This is intentionally separate from the upstream TCK: it checks that the
# result we recorded is attributable to the selected pins and that skipped or
# untested MUST requirements remain visible instead of being reported as a
# complete conformance claim.
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
result=${1:?usage: verify-tck-result.sh RESULT_JSON [TRANSPORT]}
transport=${2:-}
matrix="$repo_root/tests/conformance/profile-matrix.json"

[[ -f "$result" ]] || { printf 'TCK result does not exist: %s\n' "$result" >&2; exit 2; }
[[ -f "$matrix" ]] || { printf 'profile matrix does not exist: %s\n' "$matrix" >&2; exit 2; }

python3 - "$result" "$matrix" "$transport" <<'PY'
import json
import os
import pathlib
import sys

result_path, matrix_path, transport = sys.argv[1:]
result_file = pathlib.Path(result_path)
with result_file.open(encoding="utf-8") as handle:
    result = json.load(handle)
with open(matrix_path, encoding="utf-8") as handle:
    matrix = json.load(handle)

if not transport:
    transport = result.get("transport", "")
profile_by_transport = {
    "jsonrpc": "JSONRPC",
    "http_json": "HTTP+JSON",
    "grpc": "GRPC",
}
expected_binding = profile_by_transport.get(transport)
if expected_binding is None:
    raise SystemExit(f"unsupported TCK transport {transport!r}")
if result.get("transport") != transport:
    raise SystemExit("persisted TCK result transport does not match requested transport")
if result.get("binding") != transport:
    raise SystemExit("persisted TCK result binding selector is not explicit")

for key in ("tckCommit", "selectedProtocolCommit", "tckProtocolCommit"):
    if result.get(key) != matrix.get({
        "tckCommit": "tckCommit",
        "selectedProtocolCommit": "protocolSourceCommit",
        "tckProtocolCommit": "tckProtocolCommit",
    }[key]):
        raise SystemExit(f"TCK result pin mismatch for {key}")

profiles = [entry for entry in matrix.get("profiles", []) if entry.get("binding") == expected_binding]
if len(profiles) != 1:
    raise SystemExit(f"profile matrix must contain exactly one {expected_binding} entry")
profile = profiles[0]
if result.get("exitCode") != 0:
    raise SystemExit(f"TCK process exited with {result.get('exitCode')}")

raw_counts = result.get("mustRequirementStatusCounts", {})
counts = {status: int(raw_counts.get(status, 0)) for status in ("PASS", "SKIPPED", "NOT TESTED", "FAIL")}
for status in ("PASS", "SKIPPED", "NOT TESTED", "FAIL"):
    value = counts[status]
    if not isinstance(value, int) or value < 0:
        raise SystemExit(f"invalid MUST status count for {status}")
expected = {
    "PASS": profile.get("tckMustPassed"),
    "SKIPPED": profile.get("tckMustSkipped"),
    "NOT TESTED": profile.get("tckMustNotTested"),
    "FAIL": profile.get("tckMustFailed"),
}
if counts != {key: value for key, value in expected.items() if value is not None}:
    raise SystemExit(f"MUST counts differ from profile matrix: result={counts} expected={expected}")
non_passing = result.get("nonPassingMustRequirements", [])
if len(non_passing) != counts.get("SKIPPED", 0) + counts.get("NOT TESTED", 0) + counts.get("FAIL", 0):
    raise SystemExit("nonPassingMustRequirements does not enumerate every non-PASS MUST")
if counts.get("FAIL", 0) != 0:
    raise SystemExit("a TCK run with failed MUST requirements cannot pass the profile gate")
if os.environ.get("A2A_TCK_REQUIRE_COMPLETE") == "1" and (counts["SKIPPED"] or counts["NOT TESTED"]):
    raise SystemExit("complete TCK coverage was requested but skipped/not-tested MUST requirements remain")

transport_counts = result.get("transportStatusCounts", {})
if transport_counts.get("FAIL", 0) != 0 or transport_counts.get("TOTAL", 0) <= 0:
    raise SystemExit(f"invalid transport counts: {transport_counts}")

compatibility_path = result_file.with_name("compatibility-report.json")
if not compatibility_path.is_file():
    raise SystemExit(f"raw compatibility report is missing: {compatibility_path}")
with compatibility_path.open(encoding="utf-8") as handle:
    compatibility = json.load(handle)
transport_result = compatibility.get("per_transport", {}).get(transport)
if not isinstance(transport_result, dict):
    raise SystemExit(f"raw compatibility report has no {transport} result")
if int(transport_result.get("failed", 0)) != transport_counts.get("FAIL"):
    raise SystemExit("persisted transport failure count differs from raw compatibility report")
if int(transport_result.get("total", 0)) != transport_counts.get("TOTAL"):
    raise SystemExit("persisted transport total differs from raw compatibility report")

print(f"TCK result verified: {transport} ({expected_binding}); MUST pass={counts['PASS']} skipped={counts['SKIPPED']} not-tested={counts['NOT TESTED']} failed={counts['FAIL']}")
PY
