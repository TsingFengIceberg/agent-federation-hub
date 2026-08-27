#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
tck_dir=${A2A_TCK_DIR:-}
port=${A2A_TCK_PORT:-4999}
report_dir=${A2A_TCK_REPORT_DIR:-"$repo_root/var/tck"}

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
"$go_bin" build -o "$sut_bin" ./cmd/a2a-tck-sut
"$sut_bin" --listen "127.0.0.1:$port" --public-url "http://127.0.0.1:$port" >"$run_dir/sut.log" 2>&1 &
sut_pid=$!

for _ in $(seq 1 80); do
  if curl --fail --silent "http://127.0.0.1:$port/.well-known/agent-card.json" >/dev/null; then
    break
  fi
  sleep 0.1
done
curl --fail --silent "http://127.0.0.1:$port/.well-known/agent-card.json" >/dev/null

mkdir -p "$report_dir"
set +e
(cd "$tck_dir" && python_bin=${A2A_TCK_PYTHON:-.venv/bin/python}; if [[ ! -x "$python_bin" ]]; then python_bin=python3; fi; "$python_bin" run_tck.py --sut-host "http://127.0.0.1:$port" --transport jsonrpc --level must)
tck_status=$?
set -e

report_file="$report_dir/owned-sut-result.json"
cat >"$report_file" <<EOF
{
  "tckCommit": "5996b79f9cefa6fc390980e383e358a66fb9e49e",
  "selectedProtocolCommit": "16ba52690519bf55b9388e34d4db356efa88aa51",
  "tckProtocolCommit": "173695755607e884aa9acf8ce4feed90e32727a1",
  "sut": "cmd/a2a-tck-sut",
  "transport": "jsonrpc",
  "requestedLevel": "must",
  "exitCode": $tck_status,
  "waiverFile": "tests/conformance/tck-waivers.json",
  "interpretedAs": "evidence-with-waivers"
}
EOF

printf 'Owned A2A TCK run exit code: %d\n' "$tck_status"
printf 'Machine-readable result: %s\n' "$report_file"
exit "$tck_status"
