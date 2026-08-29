#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
if [[ "$go_bin" == */* ]]; then
  [[ -x "$go_bin" ]] || { printf 'Go binary is not executable: %s\n' "$go_bin" >&2; exit 2; }
else
  command -v "$go_bin" >/dev/null || { printf 'Go binary not found: %s\n' "$go_bin" >&2; exit 2; }
fi

printf '[trust] running local partner-style OIDC/JWKS rotation, JWT revocation, HTTPS PDP,\n'
printf '[trust] centralized audit success/outage/retry, rate limiting, and SPIFFE mTLS checks\n'
printf '[trust] generated keys/certificates stay in memory; no credentials are written\n'
cd "$repo_root"
AFH_RUN_TRUST_TESTS=1 "$go_bin" test ./internal/hub -run '^(TestRealTrustBundleWithOIDCMTLSPDPAndOperations|TestPartnerCARotation)$' -count=1 -v
printf '[trust] partner-style trust integration passed\n'
