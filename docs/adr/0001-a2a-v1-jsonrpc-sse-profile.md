# ADR 0001: A2A v1 JSON-RPC and SSE initial profile

> **Status**: accepted for the first interoperability slice<br>
> **Decision date**: 2026-08-27<br>
> **Evidence status**: protocol source pinned; implementation coverage partial

## Context

The Hub needs one explicit wire contract before product services are designed. The
A2A specification supports several protocol bindings and a broader surface than a
first implementation can verify responsibly. Leaving the version and binding
implicit would make SDK defaults, conformance results, and recovery behavior
impossible to compare.

This decision concerns the external A2A boundary only. It does not select an
in-domain agent runtime, make Go the final Hub language, or define the Hub's
durable internal task model.

## Decision

The first interoperability slice targets:

- A2A interface version `1.0`, using the protocol source pinned at
  [`16ba52690519bf55b9388e34d4db356efa88aa51`](../../submodules/a2a/);
- JSON-RPC over HTTP as the initial request/response Binding;
- Server-Sent Events (SSE) as the streaming representation for JSON-RPC;
- Agent discovery through `/.well-known/agent-card.json`;
- opaque remote Agents: the Hub depends only on the Agent Card and observable
  A2A behavior, never on the Agent's framework, model, tools, or workflow graph.

Production deployments will use HTTPS. Plain HTTP is allowed only for local
interoperability fixtures.

### Initial contract surface

The profile includes these protocol operations and result branches:

- `SendMessage`, including both direct `Message` and stateful `Task` results;
- `SendStreamingMessage` over SSE;
- `GetTask`, `CancelTask`, and `SubscribeToTask`;
- `INPUT_REQUIRED` as a resumable non-terminal state;
- text, raw file, file URL, and structured-data Artifact parts;
- protocol error preservation rather than conversion to successful business
  payloads;
- the `A2A-Version: 1.0` service parameter on A2A calls.

Agent Cards must advertise a `JSONRPC` interface with protocol version `1.0`.
Streaming calls may be made only when the card advertises
`capabilities.streaming=true`.

Timestamps emitted by Hub-owned components must be RFC 3339 UTC values ending
in `Z`. Agent timestamps are retained as received and validated at the boundary;
the Hub must not silently rewrite remote evidence.

### Authentication boundary

Agent Card discovery may be public. Authentication for A2A calls follows the
card's declared security schemes and requirements. The Hub must keep transport
credentials out of A2A Messages, Artifacts, logs, and task metadata. Cross-
organization identity delegation and authorization policy are not decided by
this ADR and require a separate threat model and ADR.

### Cancellation and recovery boundary

Cancellation is a request, not proof that external work stopped. The Hub records
the remote terminal state it observes. Recovery uses the remote `taskId` plus
`GetTask` and, for non-terminal tasks that support it, `SubscribeToTask`. A local
disconnect is not treated as remote cancellation or failure.

The first fixture uses in-memory task stores. It proves wire behavior only, not
restart durability, exactly-once execution, or cross-instance recovery.

## Deferred

The following are intentionally outside this first profile:

- HTTP+JSON and gRPC Bindings;
- Push notification configuration and callback security;
- AAMP asynchronous mailbox adaptation;
- registry, ARD, routing policy, and semantic discovery;
- production OAuth/OIDC delegation and multi-tenant authorization;
- durable task, event, and Artifact storage;
- final selection of the Hub implementation language.

These are deferred, not rejected. They need independent evidence and acceptance
criteria.

## Verification policy

The repository-owned three-process fixture is the first executable evidence:
a Go client acting as a provisional Hub test entry, a Go A2A Agent, and a Python
A2A Agent. Both Agents expose the same black-box scenarios through the same A2A
profile.

The current TCK snapshot is pinned to an older specification revision and its Go
SUT does not implement all current scenarios. TCK output is therefore supporting
evidence, not the authority for this decision. Any skipped, waived, or revision-
skewed assertion must be recorded rather than counted as a pass.

## Consequences

- Product code can start with one unambiguous external protocol path.
- Go and Python can be compared using identical remote behavior without exposing
  either Agent's internal runtime.
- Push, durability, production identity, and other Bindings remain mandatory
  decision gates before a production federation claim can be made.
- This ADR must be superseded if the pinned protocol changes in a way that alters
  the selected wire contract.

## Related evidence

- [A2A v1 Go/Python decision gate](../architecture/a2a-v1-go-python-decision-gate.md)
- [Opaque Agent federation review](../architecture/opaque-agent-federation-review.md)
- [Protocol Bindings study](../research/a2a-study/protocol-bindings.md)
- [Task delivery and recovery study](../research/a2a-study/task-delivery-and-recovery.md)
- [Reliability, errors, and cancellation study](../research/a2a-study/reliability-errors-and-cancellation.md)
