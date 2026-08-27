# ADR 0002: Durable Federation Task Reconciliation

> **Status**: accepted for the initial implementation slice<br>
> **Date**: 2026-08-27<br>
> **Evidence**: repository-owned deterministic tests and pinned A2A/AAMP source review

## Context

A2A Tasks remain owned by the remote provider. Streaming and Push are observation
channels that can disconnect, duplicate, reorder, or miss updates. A send timeout
before the provider returns a Task ID is ambiguous: blindly resending could
execute external work twice. The Hub therefore needs durable correlation and
reconciliation without copying provider-private runtime checkpoints.

Selecting PostgreSQL, Nacos, an event broker, or a distributed workflow runtime
now would combine this semantic decision with a premature infrastructure choice.

## Decision

The Hub will use a protocol-neutral Task/Event/Artifact model and a storage
interface. The initial store is an append-only, versioned JSON journal that
`fsync`s accepted mutations and replays them after restart.

The initial recovery rules are:

1. Persist a local Task and stable Message ID before sending.
2. Persist a remote Task ID as soon as it is observed.
3. Treat a stream disconnect as an observation failure, not Task failure or cancellation.
4. If the remote ID is known, call `GetTask`, merge the snapshot, and subscribe only if requested and still non-terminal.
5. If the remote ID is unknown, mark delivery `AMBIGUOUS` and do not automatically resend.
6. Record cancellation intent separately from the remote state observed afterward.
7. Converge polling, streams, Push, and mailbox events through stable deduplication keys and the same merge path.

The built-in Agent registry and direct A2A route are initial implementations of
interfaces, not requirements that every deployment use an embedded registry or
bypass an external Gateway.

## Consequences

- A process restart can recover acknowledged non-terminal Tasks without reading
  provider internals.
- The model can later be backed by a transactional database or event log without
  changing protocol semantics.
- The journal is intentionally single-process. It has no compaction, replication,
  cross-record transaction, multi-writer locking, backup, or tenant encryption.
- Exactly-once external side effects remain a non-goal. Ambiguous Tasks require
  operator or provider-specific idempotency evidence before resubmission.
- Provider history may be incomplete; current Task state and complete Artifacts
  are the reconciliation facts.

## Alternatives Considered

**In-memory only** was rejected because it cannot validate restart recovery.

**Database first** was deferred because persistence semantics and deployment
requirements are not yet stable enough to select one database responsibly.

**Treat the Hub as the workflow owner** was rejected because it violates the
opaque-provider boundary and duplicates domain-runtime checkpoint ownership.

**Retry every send timeout** was rejected because A2A Message IDs do not provide
a universal exactly-once guarantee.

## Related Material

- [Task/Event/Artifact contract](../specifications/task-event-artifact-contract.md)
- [ADR 0001](0001-a2a-v1-jsonrpc-sse-profile.md)
- [Task delivery and recovery study](../research/a2a-study/task-delivery-and-recovery.md)
- [Reliability and cancellation study](../research/a2a-study/reliability-errors-and-cancellation.md)
