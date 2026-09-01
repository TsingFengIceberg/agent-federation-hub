# ADR 0005: Federated Workload Trust

> **Status**: accepted for the current implementation | **Evidence**: deterministic local tests | **Date**: 2026-08-28

## Context

Static public keys and a built-in scope map are insufficient when independently
operated organizations rotate credentials, identify workloads through different
trust domains, delegate a user or service identity, and administer policy
outside the Hub process. The Hub must preserve one internal Principal and Action
contract without assuming one identity vendor.

## Decision

Use dynamic OIDC discovery and JWKS as the default inbound Bearer trust path.
The configured issuer is exact-match pinned. Discovery and JWKS URLs must be
public HTTPS targets, documents are size bounded, cache TTLs are bounded, and an
unknown `kid` triggers a rate-limited refresh. OIDC tokens require issuer,
audience, subject, tenant, expiry, issued-at time, an allowed algorithm, and
`jti`; revocation lookup fails closed.

Support verified mTLS as a second inbound trust path. The leaf certificate must
come from a verified TLS chain and contain exactly one valid SPIFFE URI SAN. A
replaceable resolver maps that workload ID to the same Principal model. Hybrid
mode chooses Bearer whenever an Authorization header is present, preventing an
invalid Bearer token from downgrading to the certificate identity.

Run the local Action-to-scope authorizer before an optional external HTTPS policy
decision point. The external request contains Principal and resource metadata,
not credentials or payloads. Transport, malformed response, explicit denial,
or policy-service failure all deny the operation. Credential-bearing policy and
token endpoint requests do not follow redirects.

Use RFC 8693 Token Exchange as an outbound SecretProvider profile. Profiles bind
subject and optional actor credentials to audience, optional resource, and
requested scopes. Returned scopes must be a subset, token types must match, and
lifetime is bounded. Only secret references are configured; returned tokens are
short-lived memory cache entries. Revocations are durable in PostgreSQL and
restart-replayed in the development journal.

Use the versioned [`Trust Bundle`](../specifications/trust-bundle-contract.md)
as the preferred operator-distributed snapshot for issuer-to-tenant trust and
SPIFFE workload mappings. It is validated before startup, atomically reloaded
with a monotonic generation, checked for active validity at authentication
time, and cannot be combined with the legacy issuer/workload files. The
snapshot is configuration data, not an IdP, CA, PDP, or secret store.

## Consequences

- Remote Agents remain opaque. They receive a credential appropriate to their
  declared security scheme and do not expose prompts, tools, or internal graphs.
- OIDC, SPIFFE mapping, policy evaluation, and token exchange remain replaceable
  edges around a protocol-neutral Principal and Action model.
- Server TLS is mandatory outside development mode. mTLS additionally requires
  an operator-provided client CA and either the unified Trust Bundle workload
  mapping or the compatible legacy mapping file.
- The implementation does not operate an identity provider, CA, policy authoring
  system, consent service, or credential vault. Non-development startup also
  requires explicit versioned issuer-to-tenant trust and local access-policy
  documents; these are policy inputs, not an embedded identity provider or
  policy administration system.
- Local process-scoped and PostgreSQL-coordinated rate limiting, plus a `0600`
  JSONL audit sink and optional HTTPS central exporter with bounded retry, are
  implemented and exercised by unit, PostgreSQL, and opt-in TLS integration tests. Real partner
  IdP/CA/PDP interoperability, managed-database qualification, durable audit
  export/rotation, automated certificate rollover, and outage drills remain
  gates before a production trust claim.

## Evidence

Unit, HTTP, and opt-in local TLS integration tests exercise OIDC issuer mismatch, cache and key rotation,
RSA/ECDSA/Ed25519 parsing, SPIFFE mapping, invalid-Bearer downgrade prevention,
external PDP fail-closed behavior, credential redaction, RFC 8693 audience/
actor/scope binding, token response narrowing, durable token revocation,
authenticated rate limiting, and fsync-backed audit persistence.

## Related Material

- [Authenticated Principal boundary](0003-authenticated-principal-and-policy-boundary.md)
- [Access control contract](../specifications/access-control-contract.md)
- [A2A authentication research](../research/a2a-study/authentication-and-authorization.md)
