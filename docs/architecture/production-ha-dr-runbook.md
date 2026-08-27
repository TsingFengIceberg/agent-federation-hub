# Production HA and Disaster-Recovery Runbook

> **Status**: draft operational contract | **Evidence**: local restart and
> multi-instance tests are verified; managed HA and disaster recovery remain
> to-verify

This runbook defines the failure boundaries that must be qualified before the
Hub is described as production highly available. It separates behavior already
implemented in the repository from deployment guarantees that require a real
PostgreSQL/object-storage environment.

## Durable state and readiness

- PostgreSQL is the authoritative multi-instance store. `OpenPostgres` pings
  the database before migrations, and `GET /readyz` repeats a bounded health
  check so an instance can be removed from service when its store is unavailable.
- The Journal backend is a single-process development fallback. Its backup is
  fsynced, atomically installed, and replay-validated before restore.
- `GET /healthz` is a liveness probe and does not assert database availability;
  `GET /readyz` is the traffic admission probe and returns `503` on a failed
  dependency check.

## Failure and recovery tests

| Boundary | Repository evidence | Remaining qualification |
|---|---|---|
| Two Hub workers claim one Task | PostgreSQL integration test | node/process failure injection |
| Lease expiry takeover | PostgreSQL integration test | clock skew and long outage behavior |
| Database process restart | [`run-restart-recovery.sh`](../../tests/postgres/run-restart-recovery.sh) | primary/standby failover |
| Journal restore | `TestJournalBackupAndRestoreAreReplayable` | encrypted off-host retention |
| PostgreSQL archive restore | [`run-backup-restore.sh`](../../tests/postgres/run-backup-restore.sh) | PITR and cross-region drill |
| Artifact metadata/object lifecycle | PostgreSQL and MinIO integration | replication, key rotation, restore |

## Deployment acceptance gates

1. Run at least two Hub instances against a managed PostgreSQL HA endpoint and
   verify that a terminated worker's leases are taken over without duplicate
   state mutations.
2. Prove that readiness removes an instance before traffic reaches a failed
   database or object-storage dependency, while liveness remains available for
   diagnosis.
3. Restore an encrypted backup into an isolated environment, verify migration
   checksums and Task/Event/Artifact invariants, and record recovery-point and
   recovery-time measurements.
4. Exercise rolling upgrades, schema-forward compatibility, outbox replay,
   dead-letter retention, and object deletion recovery.
5. Connect metrics and audit signals to alerts with bounded labels; local
   process counters alone are not an HA monitoring solution.

These gates are deployment tests, not claims implied by the local Docker
smokes. The Hub currently provides the application contracts and test hooks;
the managed database, object store, backup encryption, KMS, and orchestration
system remain replaceable deployment choices.
