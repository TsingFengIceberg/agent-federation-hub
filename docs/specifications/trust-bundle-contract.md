# Trust Bundle Contract

> **Status**: implemented initial contract<br>
> **Evidence**: repository-owned parser, reload, OIDC, mTLS, and integration tests<br>
> **Qualification**: real partner IdP/CA/PDP and production trust distribution remain open

## Purpose and Boundary

`TrustBundle` is a versioned authorization-trust snapshot consumed by the
Hub. It is delivered through an operator-controlled configuration channel and
is not an identity provider, certificate authority, key store, or policy
decision service. The bundle binds already-authenticated identities to tenants
and policy inputs; TLS chain verification and OIDC signature verification
remain separate runtime responsibilities.

The Hub supports the bundle as the unified path for OIDC issuer trust and
SPIFFE workload-to-Principal mapping. The older `tenant_trust.json` and
`workload_identities.json` files remain compatible migration inputs, but they
cannot be combined with `--trust-bundle-file` in one process.

## Versioned Shape

```json
{
  "version": 1,
  "generation": 12,
  "notBefore": "2026-09-01T00:00:00Z",
  "expiresAt": "2026-10-01T00:00:00Z",
  "issuers": {
    "https://idp.example.com": {
      "tenants": ["partner-a"],
      "requiredScopes": ["hub:invoke"]
    },
    "spiffe://partner.example": {
      "tenants": ["partner-a"]
    }
  },
  "workloads": {
    "spiffe://partner.example/ns/prod/sa/agent": {
      "subject": "agent",
      "tenantId": "partner-a",
      "issuer": "spiffe://partner.example",
      "scopes": ["tasks:submit"],
      "roles": ["provider"]
    }
  }
}
```

`version` is the schema version and is currently `1`. `generation` is a
strictly increasing operator snapshot number. `notBefore` is inclusive and
`expiresAt` is exclusive; the active snapshot must satisfy
`notBefore <= now < expiresAt`. Issuer keys must be absolute HTTPS or SPIFFE
URIs without user information, query strings, or fragments. Every issuer has
an explicit, non-empty tenant allowlist. Scope and role lists are unique.

Each workload key is a SPIFFE URI with a host and path. Its subject and tenant
are required. If `issuer` is omitted, the issuer is derived as
`spiffe://<workload-host>`. The resulting issuer must exist in `issuers`, and
the workload tenant must be listed by that issuer. This prevents a valid mTLS
certificate from becoming an arbitrary tenant assertion.

Unknown JSON fields, malformed timestamps, duplicate values, trailing JSON,
and files larger than 1 MiB are rejected.

## Runtime Semantics

- The Hub loads and validates the initial snapshot before starting the HTTP
  server.
- `TrustBundleManager` atomically swaps only schema-valid snapshots.
- A reload with a generation that is not greater than the active generation is
  rejected, preventing accidental rollback.
- A failed reload retains the last valid snapshot and emits an operational
  error when watch mode is enabled.
- `--trust-bundle-reload-interval` enables bounded polling; zero disables it.
- `/readyz` reports the Hub as unavailable when the active snapshot is absent,
  not yet active, or expired.
- OIDC and static JWT authentication require the configured issuer to appear
  in the active bundle. mTLS requires at least one configured workload.
- Expiry is checked at authentication time, so an expired snapshot fails
  closed even if no readiness probe is running.

The bundle does not contain CA certificates. For mTLS, the server still needs
`--tls-client-ca-file` (or an equivalent externally managed TLS trust source),
and the verified certificate must contain exactly one SPIFFE URI SAN.

## Deployment and Rotation

The committed [`trust_bundle.example.json`](../../trust_bundle.example.json)
is a template only. A deployment should write a complete replacement file to
a private location, set restrictive permissions, and atomically rename it into
place. Operators should publish overlapping validity windows and increment
`generation`; the Hub will retain the old snapshot if the replacement is
invalid or stale. Key, certificate, IdP, CA, PDP, and configuration-channel
rotation are separate operational procedures and require partner qualification.

No production claim follows from a local bundle test. Production acceptance
still requires real partner trust services, protected distribution and
rollback procedures, centralized audit retention, outage drills, and measured
tenant-isolation behavior.
