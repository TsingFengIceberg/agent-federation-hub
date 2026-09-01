# Documentation

## Current entry points

| Directory | Purpose | Status |
|---|---|---|
| [`research/a2a-study/`](research/a2a-study/) | Complete imported A2A protocol and federation research snapshot | draft / imported |
| [`research/vendor-sources-2026/`](research/vendor-sources-2026/) | Product-specific notes on dated 2026 vendor publications relevant to federation boundaries | verified sources / draft implications |
| [`architecture/a2a-v1-go-python-decision-gate.md`](architecture/a2a-v1-go-python-decision-gate.md) | Product architecture evidence and pending language decision gates | verified test evidence / decision pending |
| [`architecture/opaque-agent-federation-review.md`](architecture/opaque-agent-federation-review.md) | Evidence review and product contract for opaque remote Agent federation | verified source review / draft architecture |
| [`architecture/phase-one-hub-conformance-boundary.md`](architecture/phase-one-hub-conformance-boundary.md) | Executable Hub slice and explicit production/conformance gaps | implemented initial slice / gaps explicit |
| [`architecture/agent-configuration.md`](architecture/agent-configuration.md) | Versioned remote Agent registration configuration, discovery constraints, and startup/reload loading | implemented loader and atomic runtime reconciliation |
| [`architecture/provider-onboarding-and-preflight.md`](architecture/provider-onboarding-and-preflight.md) | Provider-agnostic AgentCard admission checks, configuration runtime semantics, and local startup preflight | implemented local tooling / external qualification open |
| [`../cmd/a2a-compat-report`](../cmd/a2a-compat-report) | Machine-readable summary of A2A Profile matrix gaps and explicit waivers | implemented evidence reporter |
| [`adr/0001-a2a-v1-jsonrpc-sse-profile.md`](adr/0001-a2a-v1-jsonrpc-sse-profile.md) | Initial A2A v1 external wire profile and explicit deferrals | accepted / implementation coverage partial |
| [`adr/0002-durable-federation-task-reconciliation.md`](adr/0002-durable-federation-task-reconciliation.md) | Durable observable-state and ambiguous-delivery recovery rules | accepted for initial slice |
| [`adr/0003-authenticated-principal-and-policy-boundary.md`](adr/0003-authenticated-principal-and-policy-boundary.md) | Authenticated Principal, policy, audit, and SecretProvider boundary | accepted / implemented initial boundary |
| [`adr/0004-postgresql-leased-background-execution.md`](adr/0004-postgresql-leased-background-execution.md) | PostgreSQL transactions, multi-instance leases, and durable Push inbox | accepted / PostgreSQL 17 integration-tested |
| [`adr/0005-federated-workload-trust.md`](adr/0005-federated-workload-trust.md) | Dynamic OIDC/JWKS, SPIFFE mTLS, external policy, token exchange, and revocation | accepted / deterministic tests |
| [`adr/0006-artifact-object-data-plane.md`](adr/0006-artifact-object-data-plane.md) | External object storage, content policy, scanning, quota, and lifecycle boundary | accepted / PostgreSQL and MinIO integration-tested |
| [`specifications/task-event-artifact-contract.md`](specifications/task-event-artifact-contract.md) | Implemented normalized Task, Event, Artifact, tenancy, and recovery contract | verified local implementation |
| [`specifications/access-control-contract.md`](specifications/access-control-contract.md) | Principal fields and management API Action-to-scope mapping | implemented initial contract |
| [`specifications/trust-bundle-contract.md`](specifications/trust-bundle-contract.md) | Versioned OIDC/mTLS trust snapshot, reload, rotation, and legacy migration boundary | implemented initial contract / partner qualification open |

The imported research snapshot records its source repository and commit. General protocol corrections belong in the source study repository first; Agent Federation Hub product decisions and implementation contracts belong here.
