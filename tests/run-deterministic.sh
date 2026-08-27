#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

"$repo_root/tests/run-unit.sh"
"$repo_root/tests/interop/run-smoke.sh"
"$repo_root/tests/real-api/run-mock-smoke.sh"
