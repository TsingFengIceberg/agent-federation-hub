# ADR 0004: PostgreSQL Leased Background Execution

> **Status**: accepted as the first multi-instance backend | **Evidence**: implemented and tested against PostgreSQL 17 | **Date**: 2026-08-28

## Context

The append-only journal provides durable restart replay for one process, but its
in-memory lock cannot coordinate multiple Hub instances. Reconciliation and Push
processing also need ownership that survives worker failure without tying a
remote Task to one HTTP request or process.

## Decision

Keep the journal as a local-development backend and add PostgreSQL as the first
multi-instance Store implementation. PostgreSQL commits each Task mutation and
its Event in one serializable transaction. Task revisions support optimistic
concurrency, and Event deduplication keys provide idempotent observation insert.

Recoverable Tasks are claimed with time-bounded leases using row locks and
`SKIP LOCKED`. Workers renew leases while a provider call is active. An expired
lease can be claimed by another instance; the old owner cannot renew or release
it. Failures are rescheduled with bounded exponential backoff.

A2A Push callbacks are authenticated and validated, then idempotently committed
to a durable inbox before the Hub returns success. A background processor
applies the normalized observation and acknowledges the inbox item only after
the Task/Event transaction succeeds. Redelivery after a crash is safe because
Task observation keys are idempotent.

The A2A send requests immediate acceptance. Once a remote Task ID is known,
leased reconciliation, Push, and polling own the longer remote lifecycle.
Continuous SSE uses committed Event cursors as its source, so it does not depend
on the process that accepted the original request.

## Consequences

- PostgreSQL instances coordinate Task and inbox ownership without an in-memory
  leader or process-local queue.
- The journal implements the same interfaces for deterministic development tests
  but remains explicitly single-process.
- PostgreSQL is a replaceable backend, not a requirement for remote Agents or a
  commitment to a specific deployment platform.
- Schema migrations are currently embedded and forward-only. Migration history,
  online rollout policy, and downgrade procedures remain open.
- Database backup/restore drills, encryption-at-rest verification, retention,
  compaction, metrics, alerting, and multi-node HA acceptance tests remain open.
- Artifact bytes still use the initial Task model. A replaceable object store and
  large-object policy must be implemented before accepting production-scale
  binary Artifacts.

## Evidence

Repository tests run against a disposable PostgreSQL 17 container and verify
transaction rollback, event atomicity, two independent connection pools
competing for one Task, lease expiry takeover, stale-owner rejection, durable
inbox deduplication, cross-pool exclusion, and acknowledgement. Journal tests
cover the same lease/backoff behavior and worker heartbeat renewal.

These tests demonstrate the implemented coordination semantics, not operational
HA or a managed PostgreSQL service certification.

## Related Material

- [Task/Event/Artifact contract](../specifications/task-event-artifact-contract.md)
- [Durable Task reconciliation ADR](0002-durable-federation-task-reconciliation.md)
- [Task recovery research](../research/a2a-study/task-delivery-and-recovery.md)
