# Federation Task, Event, and Artifact Contract

> **Status**: implemented initial contract / verified by local tests<br>
> **Updated**: 2026-08-27<br>
> **Scope**: Hub-owned observable federation state, not provider-private workflow state

## Contract Boundary

The Hub records a local federation operation and the authorized facts observed
through a protocol adapter. It does not own the remote Agent's execution,
checkpoint, prompts, tools, memory, or internal workflow graph.

The executable model is in [`internal/core/model.go`](../../internal/core/model.go),
with persistence semantics in [`internal/core/store.go`](../../internal/core/store.go).
A2A and AAMP map into this model through separate adapters under
[`internal/federation/`](../../internal/federation/).

## Task

A Task contains:

- a Hub-generated `id`, stable `messageId`, tenant and registered Agent identity;
- an input digest, without treating it as a replay or exactly-once guarantee;
- remote `taskId` and `contextId` only after the provider exposes them;
- separately recorded observed state, delivery evidence, and cancellation intent;
- the last remote observation timestamp, sanitized problem, Artifacts, revision,
  and ordered event cursor;
- a one-way Push credential hash when Push is enabled, never the callback secret.

The state vocabulary is `SUBMITTED`, `WORKING`, `INPUT_REQUIRED`,
`AUTH_REQUIRED`, `COMPLETED`, `FAILED`, `CANCELED`, and `REJECTED`, plus
`UNKNOWN` for an unmapped observation. The final four are terminal. A terminal
observation is not overwritten by a different late state.

Delivery is independent of Task state:

| Delivery | Meaning |
|---|---|
| `PENDING` | No outbound attempt is active, or a non-ambiguous preflight rejection occurred. |
| `ACKNOWLEDGED` | A provider Message, Task, status, or Artifact was observed. |
| `AMBIGUOUS` | An outbound attempt started but no remote Task ID is known; execution may or may not have started. |

The Hub persists `AMBIGUOUS` before entering the network call so a process crash
cannot leave an attempted send looking safely retryable. An ambiguous send is not
automatically repeated. A stable A2A `messageId` aids correlation but does not
establish exactly-once execution.

## Event

Every accepted mutation appends one immutable Event with a tenant, local Task
ID, monotonically increasing sequence, source, type, local creation time, and
optional remote timestamp, state, Artifact, or sanitized Problem.

The store assigns sequence numbers. Callers may resume reads after a cursor;
the HTTP API maps this to `?after=<sequence>` or SSE `Last-Event-ID`. Adapter
observations carry a stable deduplication key. Replaying the same observation
does not increment the Task revision or event sequence.

The JSON journal appends and `fsync`s each Agent, Task, lease, inbox, or outbox
mutation and replays it on process restart. It remains a single-process
development backend. The PostgreSQL backend commits a Task revision, its Event,
and the corresponding outbox publication record in one serializable
transaction, enforces unique observation keys, and coordinates recoverable work
across instances with expiring leases. Outbox delivery is at-least-once:
publishers must deduplicate by `dedupKey` and tolerate a crash between publish
and acknowledgement. PostgreSQL migrations are checksum-recorded and fail
closed if an applied migration is changed. This is not yet a claim of
operational HA, backup, compaction, dead-letter administration, or
encryption-at-rest verification.

## Artifact and Part

Artifacts are grouped by provider Artifact ID and retain name, description,
completion status, and ordered Parts. A Part has one of three kinds:

- `text`, containing UTF-8 text;
- `file`, containing base64 bytes or a URI plus media type and filename;
- `data`, containing any valid JSON value, including objects, arrays, strings,
  numbers, booleans, or null.

An Artifact update replaces the current Artifact unless the adapter marks it as
append. Append adds Parts while preserving later completion metadata. Artifact
content remains untrusted input. Before a raw or URL file Part enters a Task or
Event, the Hub streams it through a size limit, MIME detection/allowlist, tenant
quota reservation, and malware scanner into a replaceable filesystem or
S3-compatible object store. Text and structured data remain inline. File Parts
retain only `objectId`, `sizeBytes`, `sha256`, detected media type, and filename;
base64 bytes and provider URLs are cleared.

Object identity includes the tenant, local Task, provider Artifact, observation
deduplication key, Part position, and content digest. Replaying the same A2A
observation therefore returns the existing metadata without double charging
quota. PostgreSQL rows and the journal contain metadata only, never object bytes.

Remote URI retrieval permits HTTPS targets only and applies DNS/IP pinning,
private and reserved address rejection, redirect revalidation, timeout, and byte
limits. Query and fragment components are not retained. Scanner failure releases
the reservation; infected content is quarantined and never downloadable. Only
an authenticated tenant Principal with `artifacts:read` may read metadata or
`AVAILABLE` content. Leased lifecycle workers delete expired objects and release
quota across Hub instances.

## Reconciliation and Cancellation

When a stream fails after a remote Task ID was observed, the Hub does not fail
or resend the Task. It calls `GetTask`, merges the provider snapshot, and may
then subscribe if the Task remains non-terminal and the Agent declares
streaming. Background reconcilers claim non-terminal Tasks with remote IDs
through leases, renew ownership during provider calls, and refresh them through
the same path. Failures receive a bounded retry schedule, and expired leases can
be taken over by another instance.

Cancellation first records `cancelRequested=true`, then invokes the provider.
Only an observed provider state changes the Task to `CANCELED`; a transport error
leaves cancellation unconfirmed and records a sanitized Problem.

An `INPUT_REQUIRED` Task is resumed through the Hub continuation endpoint
`POST /v1/tasks/{taskID}/messages` with `{"text":"..."}`. The endpoint is
authorized and tenant-scoped like other Task operations, requires non-empty
text, and requires both persisted `remoteTaskId` and `remoteContextId`. It
sends a new A2A Message with those existing identifiers and
`ReturnImmediately=true`, then applies the provider observation to the same
local Task. A completed, failed, canceled, rejected, or otherwise non-input
required Task is not continued; callers receive a state conflict rather than
silently creating a new Task.

## Tenant and Credential Rules

All Agent, Task, Event, cancellation, reconciliation, and Push lookups are
tenant-scoped. Management routes take that tenant from an authenticated
Principal, not a caller-controlled header in JWT mode. Cross-tenant reads return
the same not-found result as absent resources. Registered Agents persist only
SecretProvider references, and the initial environment provider requires each
reference in an operator allowlist. Credential values are loaded for a call and
are not written to the Task, Event, Artifact, journal, database, or HTTP response.

Push callbacks are durably and idempotently enqueued before HTTP success. A
leased background processor applies the normalized observation and acknowledges
the inbox item only after the Task/Event mutation commits.

## Verified Scenarios

Local deterministic tests currently verify journal replay, ordered and followed cursors,
duplicate suppression, forced stream failure with and without a remote Task ID,
restart reconciliation, late-state protection, exact A2A version selection,
declared credential requirements, JWT and scope failures, audit redaction,
sanitized error categories, tenant isolation, Push token/payload/inbox controls,
leases and worker heartbeat, AAMP lifecycle mapping, Artifact externalization,
content policy, scanning, quarantine, quota, URI controls, and tenant-scoped
downloads, plus HTTP `INPUT_REQUIRED` continuation and a two-provider
tenant-isolation flow. PostgreSQL 17 integration tests add transaction rollback and real
two-connection-pool Task/Artifact lease exclusion; MinIO integration performs
actual S3-compatible object operations.

See [`internal/core/store_test.go`](../../internal/core/store_test.go),
[`internal/hub/service_test.go`](../../internal/hub/service_test.go),
[`internal/hub/http_test.go`](../../internal/hub/http_test.go), and
[`tests/postgres/run-integration.sh`](../../tests/postgres/run-integration.sh).
The two-provider tenant-isolation and continuation smoke is
[`tests/hub/run-multi-agent-smoke.sh`](../../tests/hub/run-multi-agent-smoke.sh).
