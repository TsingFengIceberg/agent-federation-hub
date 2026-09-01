# Production Control Plane and Trust Boundary

> Evidence status: `verified` for repository-owned interfaces, local TLS
> fixtures, deterministic retry/circuit tests, strict policy parsing, unified
> Trust Bundle parser/reload tests, and local trust integration. External
> Registry/Gateway, IdP, CA, PDP, and managed audit services remain `planned`
> qualification targets.

This document records the first production-shaped boundary for the Hub. It is
not a claim that a particular Registry, Gateway, identity provider, or policy
vendor has been qualified.

## Control plane

The Hub keeps a local tenant-scoped Agent cache and exposes replaceable edges:

```text
operator config -> Registry Client -> validated AgentCard -> local cache
                                  \
                                   -> direct A2A Adapter or Gateway Adapter
```

The `internal/registry.Client` and `internal/federation.Adapter` interfaces
are the stable seams. The built-in Journal/PostgreSQL registry and direct A2A
adapter are valid deployments, while Nacos, ARD, an agentgateway deployment,
or another service may implement the same edges.

External Registry behavior:

- startup publication is explicit and tenant-scoped;
- imports and periodic sync use a bounded response size and operator Bearer
  credential reference;
- HTTPS trust can use a custom CA and optional client certificate/key;
- reads use bounded retries and a per-process circuit breaker;
- a successful snapshot marks missing previously imported Agents `STALE`, so
  routing will not select them; an unavailable Registry retains the last
  validated cache;
- the remote Agent Card remains authoritative for endpoint, protocol,
  capabilities, and skills.

External Gateway behavior:

- routing is opt-in; direct A2A remains valid when policy permits it;
- Gateway calls carry tenant and Agent identity headers for downstream policy
  and audit correlation;
- `send` is never automatically replayed, because its outcome may be
  ambiguous; `get`, `cancel`, and `subscribe` may use bounded retries;
- HTTPS trust supports custom CA, optional mTLS client credentials, and a
  configured server name;
- response bodies are bounded before JSON decoding and a local circuit breaker
  suppresses repeated calls during an outage.

The local reference Registry and Gateway under `cmd/reference-*` are test
fixtures only. They intentionally do not represent managed-service HA,
multi-region routing, policy authoring, or production SLO evidence.

## Identity and authorization

Non-development startup requires all of the following:

- an inbound OIDC/JWT or verified SPIFFE mTLS mode;
- `trust_bundle.json` (or the compatible legacy trust files) binding each
  accepted issuer and SPIFFE workload to explicit tenants and optional trust
  scopes;
- `access_policy.json` (or an equivalent operator path) defining versioned
  role-to-scope and action requirements;
- PostgreSQL-backed request rate limiting;
- a `0600` local audit file; a central HTTPS exporter is optional but should be
  paired with the local sink.

`TrustBundle` is the preferred unified snapshot. It is not an IdP, CA, or PDP;
it binds identities after cryptographic authentication and is distributed by
an operator-controlled channel. `TenantTrustDocument` remains a compatible
legacy issuer-only input. Both prevent a valid token from a trusted issuer from
asserting an arbitrary tenant. `PolicyDocument` preserves the default action
contract while allowing operator-controlled role scopes and tenant-specific
action requirements. An external HTTPS PDP remains an additional fail-closed
decision layer, not a replacement for local authentication or scope checks.

Audit records written by `FileAuditSink` contain a sequence, previous hash,
and SHA-256 integrity hash. Reopening the file verifies the chain before new
records are accepted. This detects accidental truncation or tampering but is
not a substitute for append-only remote retention, key management, or a
forensic logging service.

## Operator examples

The committed templates are [`access_policy.example.json`](../../access_policy.example.json),
[`tenant_trust.example.json`](../../tenant_trust.example.json), and
[`trust_bundle.example.json`](../../trust_bundle.example.json). The unified
bundle format is specified in
[`trust-bundle-contract.md`](../specifications/trust-bundle-contract.md). A
non-development process must additionally configure the existing TLS, issuer,
audience, rate-limit, artifact scanner, and audit flags documented in the
[phase-one boundary](phase-one-hub-conformance-boundary.md).

Use `--trust-bundle-file` to select the unified path and optionally
`--trust-bundle-reload-interval` for bounded polling. It cannot be combined
with `--tenant-trust-file` or `--workload-identities-file`. For mTLS, the
bundle maps verified SPIFFE URI SANs, while `--tls-client-ca-file` still
controls certificate-chain verification.

Control-plane client credentials are always SecretProvider references. Literal
tokens must not appear in YAML, JSON, AgentCards, Tasks, Artifacts, or logs.

## Remaining gates

- qualify one managed Registry and one managed Gateway with mTLS, rotation,
  health, policy, and outage drills;
- replace static workload-identity JSON with an operational SPIFFE/SPIRE or
  equivalent trust distribution process;
- integrate a real IdP, CA, PDP, and centralized audit retention service;
- measure control-plane latency, circuit behavior, cache staleness, and
  cross-tenant denial under load;
- define consent, delegated-user policy, tenant encryption keys, and incident
  response before making a production trust claim.
