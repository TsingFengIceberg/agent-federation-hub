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

The current production-shaped authenticator validates signed JWTs with required
issuer, audience, expiry, issued-at time, subject, tenant claim, allowed signing
algorithms, and key ID. Its key lookup is replaceable. The command currently
loads one RSA, ECDSA, or Ed25519 public key from PEM; dynamic OIDC discovery and
JWKS refresh are not yet implemented.

An explicit development authenticator continues to accept the tenant header and
grants wildcard scope. The server logs a warning when this mode is selected. It
is not a production security boundary. JWT is the command default, so missing
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

## Consequences

- A forged tenant header cannot override the tenant in a valid JWT.
- Invalid signature, expiry, issuer, audience, subject, or tenant claims fail as
  authentication errors; missing scopes fail as authorization errors.
- The same Principal and Authorizer interfaces can support mTLS, workload
  identity, external policy engines, or another standards-based identity system.
- Delegation is represented as identity context only. The Hub does not yet
  implement OAuth Token Exchange, delegated token forwarding, revocation, or
  consent workflows.
- Dynamic JWKS rotation, rate limiting, mTLS, policy administration, and durable
  audit storage remain production gates.

## Evidence

Tests cover forged tenant headers, invalid signature, expired tokens,
issuer/audience mismatch, insufficient scope, audit redaction, and secret
reference isolation. These tests establish the implemented boundary; they do
not certify an external identity provider integration.

## Related Material

- [Access control contract](../specifications/access-control-contract.md)
- [A2A authentication and authorization research](../research/a2a-study/authentication-and-authorization.md)
- [A2A Push security research](../research/a2a-study/push-notification-security.md)
