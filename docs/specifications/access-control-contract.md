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

The built-in scope policy accepts `*` only for the explicit development
principal. A replaceable Authorizer may add roles, resource attributes, external
policy evaluation, or denial masking without changing HTTP handlers.

## Trust Directions

```text
management caller -- JWT/mTLS --> Hub Principal
Hub -- SecretProvider credential --> remote A2A Agent
remote A2A Agent -- task callback token --> Hub Push inbox
delegated end user -- future token exchange --> target service
```

No credential is automatically reused across these directions. Audit records
contain decision metadata but exclude credentials and payloads.

## Current Gates

Dynamic OIDC discovery/JWKS refresh, key rotation tests, mTLS authentication,
external policy administration, rate limits, durable audit export, OAuth Token
Exchange, and revocation remain unimplemented.
