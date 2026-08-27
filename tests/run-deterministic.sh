#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

"$repo_root/tests/run-unit.sh"
"$repo_root/tests/hub/run-smoke.sh"
"$repo_root/tests/interop/run-smoke.sh"
"$repo_root/tests/real-api/run-mock-smoke.sh"

if [[ ${AFH_RUN_POSTGRES_TESTS:-0} == 1 ]]; then
  "$repo_root/tests/postgres/run-integration.sh"
fi

if [[ ${AFH_RUN_MINIO_TESTS:-0} == 1 ]]; then
  "$repo_root/tests/minio/run-integration.sh"
fi
