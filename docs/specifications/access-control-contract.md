# Hub Access Control Contract

> **Status**: implemented initial contract | **Evidence**: local unit and HTTP tests | **Date**: 2026-08-28

## Principal

Authenticated management requests carry an internal Principal with:

- `subject` and `tenantId` as required identity fields;
- `issuer` and `authMethod` as trust provenance;
- `scopes` and `roles` as policy inputs;
- an optional delegated actor chain that is not itself an authorization grant.

The tenant used for Store queries comes from this Principal. In JWT mode,
`X-AFH-Tenant-ID` is ignored as an identity assertion. Development mode accepts
the header only to support deterministic local tests and is explicitly unsafe
for production.

## Action-to-Scope Mapping

| Hub Action | Required scope |
|---|---|
| Register Agent | `agents:write` |
| List Agents | `agents:read` |
| Submit Task | `tasks:submit` |
| Read Task | `tasks:read` |
| Read/follow Task Events | `tasks:read` |
| Cancel Task | `tasks:cancel` |
| Reconcile Task | `tasks:reconcile` |
| Enable Push while submitting | `push:configure` in addition to `tasks:submit` |
| Read Artifact metadata/content | `artifacts:read` |
| Revoke a token ID for the caller tenant | `security:revoke` |

The built-in scope policy accepts `*` only for the explicit development
principal. A replaceable Authorizer may add roles, resource attributes, external
policy evaluation, or denial masking without changing HTTP handlers.

## Trust Directions

```text
management caller -- OIDC JWT / SPIFFE mTLS --> Hub Principal
Hub -- SecretProvider credential --> remote A2A Agent
remote A2A Agent -- task callback token --> Hub Push inbox
delegated subject/actor -- RFC 8693 token exchange --> target service
```

No credential is automatically reused across these directions. Audit records
contain decision metadata but exclude credentials and payloads.

## Implemented Trust Edges

- OIDC discovery and bounded JWKS caching support RSA, ECDSA, and Ed25519 keys;
  an unknown `kid` triggers a rate-limited refresh for key rotation.
- Verified mTLS maps exactly one SPIFFE URI SAN through a replaceable workload
  resolver. Hybrid mode never downgrades an invalid Bearer credential to mTLS.
- The local scope map can be chained with an HTTPS external policy endpoint;
  denial, transport failure, or malformed decisions fail closed.
- RFC 8693 Token Exchange profiles bind subject/actor credentials, audience,
  resource, and scopes while exposing only SecretProvider references to adapters.
- OIDC requires `jti`; tenant/issuer/token revocations persist in the journal or
  PostgreSQL and authentication fails if revocation state is unavailable.
- An optional process-local token bucket is keyed by tenant, subject, and action;
  rejected requests return `429` with `Retry-After`. Deployments needing a
  shared budget must provide a distributed implementation of the same interface.
- Audit records can be written to an append-only `0600` JSONL file with an
  `fsync` per record. This is a local durability boundary, not a central SIEM.

## Current Gates

Production acceptance still requires real partner IdP, CA, and policy-service
interoperability; automated key/certificate rollover; distributed rate limits;
policy authoring and distribution; centralized durable audit export and
retention; consent workflows; and trust-service outage drills. Deterministic
and local TLS tests establish the local contract only.
