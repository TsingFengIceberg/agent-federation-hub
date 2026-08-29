#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
issuer=${AFH_TRUST_ISSUER:-}
audience=${AFH_TRUST_AUDIENCE:-}
token_file=${AFH_TRUST_TOKEN_FILE:-}

log() { printf '[trust-external] %s\n' "$*"; }
fail() { log "ERROR: $*" >&2; exit 2; }
[[ -n "$issuer" ]] || fail 'AFH_TRUST_ISSUER is required'
[[ -n "$audience" ]] || fail 'AFH_TRUST_AUDIENCE is required'
[[ -n "$token_file" ]] || fail 'AFH_TRUST_TOKEN_FILE is required'
[[ -f "$token_file" ]] || fail "token file does not exist: $token_file"
[[ "$issuer" == https://* ]] || fail 'AFH_TRUST_ISSUER must use HTTPS'

args=(--issuer "$issuer" --audience "$audience" --token-file "$token_file")
[[ -n "${AFH_TRUST_PDP_URL:-}" ]] && args+=(--pdp-url "$AFH_TRUST_PDP_URL")
[[ -n "${AFH_TRUST_AUDIT_URL:-}" ]] && args+=(--audit-url "$AFH_TRUST_AUDIT_URL")
[[ -n "${AFH_TRUST_AUDIT_TOKEN_ENV:-}" ]] && args+=(--audit-token-env "$AFH_TRUST_AUDIT_TOKEN_ENV")
if [[ -n "${AFH_TRUST_MTLS_URL:-}" ]]; then
  args+=(--mtls-url "$AFH_TRUST_MTLS_URL" --mtls-ca-file "${AFH_TRUST_MTLS_CA_FILE:?AFH_TRUST_MTLS_CA_FILE is required}" --mtls-cert-file "${AFH_TRUST_MTLS_CERT_FILE:?AFH_TRUST_MTLS_CERT_FILE is required}" --mtls-key-file "${AFH_TRUST_MTLS_KEY_FILE:?AFH_TRUST_MTLS_KEY_FILE is required}")
fi

cd "$repo_root"
log 'Running OIDC/JWKS, optional PDP, audit, and mTLS checks against configured partner endpoints'
"$go_bin" run ./cmd/trust-probe "${args[@]}"
log 'external trust profile passed; repeat with rotated keys and during outage drills'
