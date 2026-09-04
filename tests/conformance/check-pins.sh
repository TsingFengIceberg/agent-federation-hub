#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
profile="$repo_root/tests/conformance/profile-matrix.json"
a2a_dir=${A2A_SOURCE_DIR:-"$repo_root/submodules/a2a"}
tck_dir=${A2A_TCK_DIR:-}
require_tck=${A2A_TCK_REQUIRE_PIN:-0}

if [[ ! -d "$a2a_dir/.git" && ! -f "$a2a_dir/.git" ]]; then
  printf 'A2A source directory is not a Git checkout: %s\n' "$a2a_dir" >&2
  exit 2
fi

readarray -t pins < <(python3 - "$profile" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    profile = json.load(handle)
if profile.get("schemaVersion") != 1:
    raise SystemExit("profile matrix schemaVersion must be 1")
if not profile.get("goSDKModule"):
    raise SystemExit("profile matrix goSDKModule is required")
for key in ("protocolSourceCommit", "goSDKVersion", "tckCommit", "tckProtocolCommit"):
    print(profile[key])
PY
)
expected_protocol=${pins[0]}
expected_sdk=${pins[1]}
expected_tck=${pins[2]}
expected_tck_protocol=${pins[3]}
actual_protocol=$(git -C "$a2a_dir" rev-parse HEAD)
if [[ "$actual_protocol" != "$expected_protocol" ]]; then
  printf 'A2A protocol pin mismatch: expected %s, got %s\n' "$expected_protocol" "$actual_protocol" >&2
  exit 1
fi

actual_sdk=$(awk '$1 == "github.com/a2aproject/a2a-go/v2" { print $2; exit }' "$repo_root/go.mod")
if [[ "$actual_sdk" != "$expected_sdk" ]]; then
  printf 'A2A Go SDK pin mismatch: expected %s, got %s\n' "$expected_sdk" "$actual_sdk" >&2
  exit 1
fi

if [[ -z "$tck_dir" ]]; then
  if [[ "$require_tck" == "1" ]]; then
    printf 'A2A_TCK_DIR is required when A2A_TCK_REQUIRE_PIN=1\n' >&2
    exit 2
  fi
  printf 'A2A protocol and Go SDK pins: pass\n'
  printf 'TCK pin: not checked (set A2A_TCK_DIR to a checkout at %s)\n' "$expected_tck"
  printf 'TCK embedded protocol pin recorded: %s\n' "$expected_tck_protocol"
  exit 0
fi
if [[ ! -d "$tck_dir" ]]; then
  printf 'A2A_TCK_DIR is not a directory: %s\n' "$tck_dir" >&2
  exit 2
fi
actual_tck=$(git -C "$tck_dir" rev-parse HEAD)
if [[ "$actual_tck" != "$expected_tck" ]]; then
  printf 'A2A TCK pin mismatch: expected %s, got %s\n' "$expected_tck" "$actual_tck" >&2
  exit 1
fi
version_file="$tck_dir/specification/version.json"
if [[ -f "$version_file" ]]; then
  actual_tck_protocol=$(python3 - "$version_file" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    print(json.load(handle).get("commitHash", ""))
PY
  )
  if [[ "$actual_tck_protocol" != "$expected_tck_protocol" ]]; then
    printf 'TCK embedded protocol pin mismatch: expected %s, got %s\n' "$expected_tck_protocol" "$actual_tck_protocol" >&2
    exit 1
  fi
elif [[ "$require_tck" == "1" ]]; then
  printf 'TCK checkout is missing specification/version.json\n' >&2
  exit 2
fi
printf 'A2A protocol, Go SDK, and TCK repository pins: pass\n'
printf 'TCK embedded protocol pin recorded: %s\n' "$expected_tck_protocol"
