# ADR 0010: Agent Federation Hub v1 Product Contract

> **Status**: accepted for the next implementation phase<br>
> **Date**: 2026-09-01<br>
> **Evidence**: repository-owned contract tests and existing local A2A/control-plane tests

## Context

The repository had separate A2A, Task/Event/Artifact, access-control, and
opaque-Agent documents. Those documents correctly described individual
boundaries, but did not provide one product contract against which a new
Provider, Binding, or security deployment could be reviewed. Without that
contract, a local fixture could accidentally become a product requirement or
the Hub could drift toward owning provider-private orchestration.

## Decision

1. Adopt the versioned Hub v1 product contract in
   [`docs/specifications/federation-hub-v1-contract.md`](../specifications/federation-hub-v1-contract.md).
2. Keep A2A `1.0` JSON-RPC+SSE as the accepted initial external profile.
   HTTP+JSON+SSE and gRPC server-streaming remain explicit opt-in profiles.
3. Define the Hub as a multi-tenant discovery, trust, routing, and observable
   Task reconciliation layer. A conformant remote Provider remains opaque.
4. Treat Registry, Gateway, identity, policy, credential, storage, and event
   services as replaceable integration edges. No vendor or runtime becomes a
   core requirement without a separate evidence-backed ADR.
5. Require machine-readable contract metadata and tests to change together
   with the normative product document.

## Consequences

- Provider onboarding is evaluated by its public AgentCard and A2A behavior,
  not by its framework or internal graph.
- The Hub may retain observable state, correlation, audit, and recovery data,
  but must not mirror private Provider checkpoints or hidden reasoning.
- New Binding, trust, or storage support must be added as an explicit profile
  and tested independently; SDK defaults cannot silently expand the contract.
- Local and partner-style fixtures remain useful evidence but cannot be
  promoted to production claims without the qualification gates in the
  product contract.

## Non-goals

- replacing LangGraph or another in-domain runtime;
- standardizing a Provider's domain schema, tools, model, memory, or workflow;
- promising exactly-once cross-organization side effects;
- requiring a central Gateway for every A2A call;
- implementing a universal marketplace, payment, or semantic planner in v1.

## Related

- [ADR 0001: A2A v1 JSON-RPC and SSE](0001-a2a-v1-jsonrpc-sse-profile.md)
- [ADR 0005: Federated Workload Trust](0005-federated-workload-trust.md)
- [Opaque Agent Federation Review](../architecture/opaque-agent-federation-review.md)
- [Hub v1 machine contract](../../tests/conformance/hub-contract.json)
