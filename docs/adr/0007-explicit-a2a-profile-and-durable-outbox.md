# ADR 0007: Explicit A2A Profiles and Transactional Event Outbox

> **Status**: accepted for the next implementation slice<br>
> **Decision date**: 2026-08-28<br>
> **Evidence status**: profile and Journal behavior verified locally; PostgreSQL behavior requires the opt-in integration test

## Context

The Hub had an explicit JSON-RPC/SSE decision, but the adapter's transport
choice and the SDK's fallback behavior were not represented as one executable
contract. At the same time, Task and Event state was durable while downstream
publication (audit export, indexing, notifications, and future event-bus
delivery) had no transactionally linked queue. A process crash could therefore
leave committed state without a recoverable publication record.

## Decisions

### 1. A2A profile selection is explicit

The accepted first profile remains A2A interface version `1.0`, JSON-RPC over
HTTP, and SSE for streaming. The machine-readable profile is
[`tests/conformance/a2a-profile.json`](../../tests/conformance/a2a-profile.json).
Agent Card selection must match a configured `(protocolVersion, binding)` pair
exactly. The default adapter accepts only that profile.

The Go adapter exposes an opt-in ordered profile constructor and supports the
A2A SDK's JSON-RPC, HTTP+JSON, and gRPC transports. gRPC is intentionally
opt-in with an explicit endpoint/TLS dial configuration; the default remains
JSON-RPC+SSE. The repository-owned fixture and aligned TCK matrix exercise all
three transports, while authentication, Push sender, and signed-card behavior
remain separate capability gates.
No SDK fallback may silently expand the advertised support matrix.

### 2. Task events produce an outbox record in the same commit

Every newly committed Task Event creates one durable `OutboxItem` with the
event JSON as its payload, event type as its topic, and the event deduplication
key as its idempotency identity. Both the Journal and PostgreSQL stores expose
the same `OutboxStore` contract.

`OutboxProcessor` claims records with leases, publishes them through a
replaceable idempotent publisher, and acknowledges only after publication.
Failures are rescheduled with bounded backoff. The delivery guarantee is
at-least-once; exactly-once external side effects are not claimed.

The executable Hub accepts an HTTPS collector (`--outbox-url`), a
CloudEvents 1.0 structured-mode collector (`--outbox-cloudevents-url`), or a
0600 append-only JSONL development sink (`--outbox-file`). The CloudEvents
publisher carries the stable tenant/deduplication identity and preserves the
Task Event payload as `data`; a managed event bus remains an operator
integration rather than a built-in dependency.

Operators can inspect records through tenant-scoped `GET /v1/outbox`, replay a
non-purged dead letter with `POST /v1/outbox/{id}/replay`, and apply bounded
retention with `POST /v1/outbox/purge?before=<RFC3339>&limit=<n>`. These routes
require `outbox:read` or `outbox:write` and never cross tenant boundaries.

PostgreSQL schema migration `003_outbox.sql` adds the queue and its pending
index. The migration runner records SHA-256 checksums in
`afh_schema_migrations`, serializes concurrent migration attempts with a
transaction advisory lock, and fails if an applied migration is modified.

## Consequences

- Event publication can recover after process restart and can be drained by
  multiple Hub instances without duplicate lease ownership.
- Consumers must deduplicate by the outbox key and tolerate redelivery after a
  publish/acknowledgement crash window.
- The Journal remains useful for local development, but PostgreSQL plus an
  external object store remains the production deployment direction.
- The local/HTTPS/CloudEvents sinks do not provide a broker's partitioning,
  consumer offsets, or cross-region replication; managed event-bus operations
  remain deployment work.

## Verification

- `internal/core/store_test.go` verifies atomic Journal event/outbox creation,
  lease exclusion, acknowledgement, and restart replay.
- `internal/worker/outbox_test.go` verifies publish-before-ack and retry delay.
- `internal/worker/http_outbox_test.go` verifies CloudEvents 1.0 structured
  payloads, stable identity headers, and HTTPS enforcement.
- `internal/core/store_test.go` and `internal/hub/http_test.go` verify
  tenant-scoped dead-letter listing, replay, and retention operations.
- [`run-cloudevents-smoke.sh`](../../tests/hub/run-cloudevents-smoke.sh) verifies
  an actual HTTPS collector receiving structured events from a running Hub.
- `internal/core/postgres_integration_test.go` verifies PostgreSQL transactional
  outbox creation and multi-instance lease exclusion when PostgreSQL is enabled.
- `tests/conformance/profile_test.go` verifies that protocol pins and accepted
  profile coverage remain explicit.

## Related

- [ADR 0001: A2A v1 JSON-RPC and SSE initial profile](0001-a2a-v1-jsonrpc-sse-profile.md)
- [ADR 0002: durable federation Task reconciliation](0002-durable-federation-task-reconciliation.md)
- [ADR 0004: PostgreSQL leased background execution](0004-postgresql-leased-background-execution.md)
- [Phase-one Hub and conformance boundary](../architecture/phase-one-hub-conformance-boundary.md)
