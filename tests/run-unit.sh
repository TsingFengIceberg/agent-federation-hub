#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
go_bin=${GO_BIN:-go}
python_project="$repo_root/tests/interop/python-agent"
python_bin="$python_project/.venv/bin/python"

cd "$repo_root"
uv sync --locked --project "$python_project"
"$go_bin" test ./...
"$python_bin" -m unittest discover \
  -s tests/real-api -p 'test_*.py' -v
