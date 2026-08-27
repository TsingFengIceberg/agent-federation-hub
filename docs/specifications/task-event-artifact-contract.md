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

The current JSON journal appends and `fsync`s each Agent or Task mutation and
replays it on process restart. It is an initial single-process durability proof,
not a claim of concurrent multi-node storage, compaction, backup, encryption at
rest, or database transactions.

## Artifact and Part

Artifacts are grouped by provider Artifact ID and retain name, description,
completion status, and ordered Parts. A Part has one of three kinds:

- `text`, containing UTF-8 text;
- `file`, containing base64 bytes or a URI plus media type and filename;
- `data`, containing any valid JSON value, including objects, arrays, strings,
  numbers, booleans, or null.

An Artifact update replaces the current Artifact unless the adapter marks it as
append. Append adds Parts while preserving later completion metadata. Artifact
content remains untrusted input; malware scanning, external URI fetching,
retention, encryption, and tenant quotas are production gates not implemented by
this contract.

## Reconciliation and Cancellation

When a stream fails after a remote Task ID was observed, the Hub does not fail
or resend the Task. It calls `GetTask`, merges the provider snapshot, and may
then subscribe if the Task remains non-terminal and the Agent declares
streaming. On restart, the reconciler scans non-terminal Tasks with remote IDs
and refreshes them through the same path.

Cancellation first records `cancelRequested=true`, then invokes the provider.
Only an observed provider state changes the Task to `CANCELED`; a transport error
leaves cancellation unconfirmed and records a sanitized Problem.

## Tenant and Credential Rules

All Agent, Task, Event, cancellation, reconciliation, and Push lookups are
tenant-scoped. Cross-tenant reads return the same not-found result as absent
resources. Registered Agents persist security-scheme-to-environment-variable
references only, and the referenced variable name must be present in the
operator-configured Hub allowlist. Credential values are loaded for a call and
are not written to the Task, Event, Artifact, journal, or HTTP response.

## Verified Scenarios

Local deterministic tests currently verify journal replay, ordered cursors,
duplicate suppression, forced stream failure with and without a remote Task ID,
restart reconciliation, late-state protection, exact A2A version selection,
declared credential requirements, sanitized error categories, tenant isolation,
Push token and payload controls, and AAMP lifecycle mapping.

See [`internal/core/store_test.go`](../../internal/core/store_test.go),
[`internal/hub/service_test.go`](../../internal/hub/service_test.go), and
[`internal/hub/http_test.go`](../../internal/hub/http_test.go).
