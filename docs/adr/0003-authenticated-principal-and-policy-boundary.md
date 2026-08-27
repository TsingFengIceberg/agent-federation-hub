# ADR 0003: Authenticated Principal and Policy Boundary

> **Status**: accepted for the current implementation | **Evidence**: implemented and locally tested | **Date**: 2026-08-28

## Context

The initial Hub accepted `X-AFH-Tenant-ID` as a scoping input. A caller-controlled
header cannot establish a production tenant boundary. Inbound caller identity,
outbound credentials used to call a remote Agent, delegated end-user identity,
and A2A Push callback credentials also have different issuers, audiences, and
trust directions.

## Decision

The Hub establishes a protocol-neutral Principal before every management API
operation. A Principal contains subject, tenant, issuer, authentication method,
scopes, roles, and an optional delegated actor chain. Route handlers obtain the
tenant only from that Principal.

The production-shaped authenticator validates signed JWTs with required issuer,
audience, expiry, issued-at time, subject, tenant claim, allowed signing
algorithms, and key ID. OIDC mode discovers issuer metadata and caches RSA,
ECDSA, or Ed25519 JWKS keys with bounded TTLs. An unknown key ID triggers a
rate-limited refresh so normal key rotation does not require a Hub restart.
Static PEM JWT validation remains available as an explicit compatibility mode.

An explicit development authenticator continues to accept the tenant header and
grants wildcard scope. The server logs a warning when this mode is selected. It
is not a production security boundary. OIDC is the command default, so missing
production identity configuration fails startup rather than silently selecting
development authentication.

Every route passes an Action and resource identifier to an Authorizer before a
resource lookup. The first policy implementation maps Actions to scopes. A
structured audit sink records authentication and authorization decisions but
does not record bearer tokens, request bodies, outbound credentials, or Task
payloads.

Remote Agent credentials are resolved through a SecretProvider after an
operator allowlist check. A2A Push continues to use its task-specific callback
token because the callback trust direction differs from management API JWTs.
Verified client certificates can map one SPIFFE URI SAN to a replaceable
workload identity resolver. A hybrid mode accepts OIDC or mTLS but never falls
back to mTLS after a caller supplied an invalid Bearer credential. An optional
HTTPS policy decision point runs after local scope enforcement and fails closed.

## Consequences

- A forged tenant header cannot override the tenant in a valid JWT.
- Invalid signature, expiry, issuer, audience, subject, or tenant claims fail as
  authentication errors; missing scopes fail as authorization errors.
- OAuth RFC 8693 Token Exchange profiles bind subject and optional actor tokens
  to audience, resource, requested scopes, and a short-lived cache. Profiles and
  secrets are operator-controlled references, not tenant-supplied credentials.
- Token IDs can be revoked per issuer and tenant in the journal or PostgreSQL;
  OIDC requires `jti` and fails authentication if revocation state is unavailable.
- Rate limiting, automated certificate issuance/rotation, consent workflows,
  policy authoring/distribution, and durable audit export remain production gates.

## Evidence

Tests cover forged tenant headers, invalid signature, expired tokens,
issuer/audience mismatch, OIDC issuer pinning and JWKS rotation, verified SPIFFE
mapping, hybrid downgrade prevention, insufficient scope, external policy
failure, token-exchange binding, revocation, audit redaction, and secret
reference isolation. These tests establish the implemented contract; they do
not certify a particular external identity provider, CA, or policy service.

## Related Material

- [Access control contract](../specifications/access-control-contract.md)
- [A2A authentication and authorization research](../research/a2a-study/authentication-and-authorization.md)
- [A2A Push security research](../research/a2a-study/push-notification-security.md)
