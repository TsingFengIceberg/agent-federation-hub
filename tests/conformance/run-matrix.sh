#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
: "${A2A_TCK_DIR:?A2A_TCK_DIR must point to a checked-out a2a-tck repository}"
go_bin=${GO_BIN:-go}
report_root=${A2A_TCK_REPORT_ROOT:-"$repo_root/var/tck"}

"$repo_root/tests/conformance/check-pins.sh"
for profile in jsonrpc http_json; do
  case "$profile" in
    jsonrpc) transport=jsonrpc ;;
    http_json) transport=http_json ;;
  esac
  A2A_TCK_TRANSPORT="$transport" \
  A2A_TCK_BINDING="$profile" \
  A2A_TCK_REPORT_DIR="$report_root/$profile" \
  GO_BIN="$go_bin" \
    "$repo_root/tests/conformance/run-tck.sh"
done
