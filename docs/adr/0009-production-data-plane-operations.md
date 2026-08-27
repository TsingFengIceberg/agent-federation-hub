# ADR 0009: Production Data-Plane Operations

> **Status**: initial operational contract; production qualification remains open
> **Evidence**: Journal backup/restore unit test and PostgreSQL transaction/lease integration tests are verified locally; managed-service controls are planned

## Decision

The Hub data plane must make failure and recovery behavior explicit at three
boundaries:

1. **Durable state**: Task, Event, Inbox, Outbox, revocation, and Artifact
   metadata are committed transactionally in PostgreSQL. Schema migrations use
   a checksum ledger and fail closed if an applied migration is changed.
2. **Delivery workers**: reconciliation, Push inbox, Artifact lifecycle, and
   optional Outbox workers use leases. Outbox delivery is at-least-once with a
   configurable maximum attempt count and a durable dead-letter state. A
   publisher must use the stable tenant/task/dedup identity for idempotency.
3. **Operations**: the process cancels workers on shutdown and waits up to
   `--worker-drain-timeout` for them to stop. File-backed Journal snapshots are
   fsynced and atomically renamed; `tests/postgres/run-backup-restore.sh` proves
   a PostgreSQL custom-format archive can be restored in the reference image.

## Boundaries that are not claimed yet

- PostgreSQL failover, managed backup encryption, point-in-time recovery, and
  cross-region disaster recovery are not qualified by the local tests.
- Object storage replication, encryption keys, legal holds, and restore drills
  remain deployment responsibilities until a concrete storage profile is chosen.
- The Outbox dead-letter record is durable, but an operator API, retention
  policy, replay authorization, and metrics/alert integration are still needed.
- A single Hub process can drain gracefully; rolling upgrades and multi-node
  readiness require deployment-level health checks and failure injection.

## References

- [PostgreSQL leased execution ADR](0004-postgresql-leased-background-execution.md)
- [Artifact object data plane ADR](0006-artifact-object-data-plane.md)
- [Outbox and profile ADR](0007-explicit-a2a-profile-and-durable-outbox.md)
- [PostgreSQL backup/restore smoke](../../tests/postgres/run-backup-restore.sh)
