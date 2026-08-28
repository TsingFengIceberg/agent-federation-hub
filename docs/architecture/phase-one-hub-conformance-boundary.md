# Phase-One Hub and Conformance Boundary

> **Status**: implemented initial slice / production gaps explicit<br>
> **Updated**: 2026-08-28<br>
> **Evidence**: repository-owned tests, pinned upstream source, and previously recorded local interoperability runs

## Implemented Slice

The new `cmd/federation-hub` process exposes a tenant-scoped HTTP control plane
around a provider-opaque federation service:

```text
POST /v1/agents
GET  /v1/agents
POST /v1/tasks
GET  /v1/tasks/{id}
GET  /v1/tasks/{id}/events
POST /v1/tasks/{id}/cancel
POST /v1/tasks/{id}/messages
POST /v1/tasks/{id}/reconcile
POST /v1/tasks/{id}/push
```

Registration resolves an Agent Card and accepts only the selected A2A `1.0`
JSON-RPC interface. Calls use SecretProvider references whose first provider is
an operator-allowlisted environment backend. Task
submission consumes A2A SSE observations, persists normalized events and
Artifacts, and follows the recovery rules in [ADR 0002](../adr/0002-durable-federation-task-reconciliation.md).

The event endpoint returns JSON, finite SSE replay, or a continuous committed
Event stream with `follow=true`. `after` and `Last-Event-ID` resume after a
durable cursor. The first fan-out implementation polls the Store and has no
explicit backpressure policy; it remains independent of the process that
accepted the original Task.

When a remote Task is `INPUT_REQUIRED`, callers continue it with
`POST /v1/tasks/{id}/messages` and a JSON body containing non-empty `text`.
The Hub requires the local Task to retain both the provider `taskId` and
`contextId`, reuses those identifiers for the A2A Message, and returns the
updated local snapshot. Continuation is tenant-scoped and rejected with `409`
unless the Task is currently `INPUT_REQUIRED`; it never creates a replacement
Task or exposes provider-private workflow state.

## Boundary Matrix

| Area | Current executable evidence | Not yet claimed |
|---|---|---|
| A2A profile | Machine-checked exact `1.0` JSON-RPC+SSE selection, SDK `v2.5.0`, explicit opt-in HTTP+JSON adapter path, repository-owned deterministic TCK SUT | HTTP+JSON accepted-profile evidence, gRPC, extensions, signed/extended Card policy |
| Durability | Journal append/`fsync`/replay plus PostgreSQL Task/Event/outbox transactions, revisions, schema checksum ledger, and real two-pool lease tests | Managed database qualification, HA, backup/restore, compaction/retention, outbox dead-letter operations |
| Recovery | Known-ID disconnect uses `GetTask`; unknown-ID send becomes ambiguous | Automated ambiguous-operation resolution or exactly-once execution |
| Authentication | Dynamic OIDC/JWKS, JWT validation/revocation, SPIFFE mTLS mapping, external HTTPS policy, RFC 8693 exchange, Principal/scope policy, SecretProvider, and audit | Real partner trust-service integration, automated rollover, consent, centralized retention, and outage qualification |
| Tenancy | Authenticated Principal supplies tenant for every management lookup; forged JWT-mode tenant header is ignored | ABAC policy administration, quotas, tenant encryption keys, cross-organization trust federation |
| Push receiver | Per-Task Bearer hash, constant-time check, task/tenant/size controls, durable idempotent inbox, leased retry/ack, HTTPS/DNS/IP policy, authenticated rate limiting and audit | Replay timestamp/signature, dead-letter policy, HA ingress load test |
| Artifact | Text, raw bytes, URI, and arbitrary JSON data; append/replace semantics; filesystem/S3 object storage, MIME/size/quota controls, ClamAV quarantine, authenticated retrieval, expiry leases | Encryption policy, DLP/legal hold, backup/restore, production throughput |
| Errors | Sanitized transport/auth/authz/validation/resource/state/protocol categories | Complete binding-specific status equivalence and tenant policy details |
| Registry | Durable built-in Agent Card registration by URL | Nacos/ARD adapter, Card signature verification, refresh and health policy |
| Background work | PostgreSQL/journal leases, heartbeats, expiry takeover, bounded retry; immediate A2A acceptance | Priority, preemption, operator pause/drain, dead-letter administration |
| Gateway | Direct A2A calls behind the federation Adapter interface | Managed agentgateway route, policy routing, rate limiting, egress controls |
| AAMP | AAMP 1.1 lifecycle/result/attachment mapper into the common model | SMTP/JMAP client, discovery, sender policy, pairing, mailbox credentials |

Remote Agent Card and selected endpoint URLs default to HTTPS and reject literal
private, loopback, link-local, multicast, and unspecified addresses plus common
private host suffixes. Local development can explicitly enable private Agent
URLs. The operator-configured Push callback has the same public HTTPS checks.
Production deployment still needs DNS resolution and revalidation on every
connection and redirect to close DNS-rebinding and hostname-based SSRF paths.
The Hub is the Push receiver in this slice; the remote Agent performs outbound
delivery.

## Running the Initial Service

With the pinned Go dependencies available:

```bash
go run ./cmd/federation-hub \
  -listen 127.0.0.1:8080 \
  -storage journal \
  -journal var/hub.journal \
  -auth-mode development \
  -credential-env-allowlist REMOTE_AGENT_TOKEN
```

Development mode uses `X-AFH-Tenant-ID`, grants wildcard scope, and logs a
warning; it is only for local fixtures. JWT mode requires issuer, audience, key
ID, and a PEM public-key file, then derives tenant identity and scopes from the
validated token. PostgreSQL mode reads its DSN from the configured environment
variable. `-allow-private-agent-urls` exists only for local fixtures. Push
additionally requires a public HTTPS base URL.

## TCK Alignment

[`tests/conformance/a2a-profile.json`](../../tests/conformance/a2a-profile.json)
records the selected protocol source, SDK, Binding, and the exact TCK revision
previously evaluated. A deterministic Go test verifies that these pins and their
evidence status do not drift silently.

The external TCK remains `unresolved-revision-skew`, not passed. The
repository-owned JSON-RPC/SSE SUT run at the pinned TCK revision exited
successfully with 81 passed, 154 skipped, and 30 deselected pytest cases; its
compatibility registry records 67 PASS, 25 SKIPPED, and 37 NOT TESTED
requirements. The remaining untested bindings and
authentication/Push/revision waivers are machine-recorded; full conformance
requires an aligned TCK and closure or explanation of every remaining MUST
failure.

## Registry and Gateway Replaceability

The current `Store` provides a minimal local registry, while the federation
`Adapter` provides the data-plane boundary. Neither implies that the Hub must be
the canonical catalog or mandatory traffic proxy. Future Nacos or ARD discovery
can populate logical Agents, and an agentgateway route can implement the same
outbound contract. Direct A2A remains valid when tenant policy and trust permit
it; managed Gateway routing remains valid when policy requires central control.

## AAMP Boundary

The AAMP mapper is deliberately not an A2A emulation. AAMP `taskId` becomes the
remote Task correlation; `task.ack`, `task.help_needed`, and `task.result` map to
observable working, input-required, and terminal states; body, structured result,
and attachments become Artifact Parts. The AAMP mailbox thread remains its
authoritative asynchronous control plane. It does not become an in-domain Agent
orchestration runtime, and no SMTP/JMAP compatibility claim is made yet.

## External Provider Readiness Note

The 2026-08-28 real ca-agent smoke completed A2A discovery, immediate
acknowledgement, SSE reconciliation, and Artifact delivery, but its provider
log also emitted a SQLite thread-affinity warning from the provider heartbeat
worker (`sqlite3.ProgrammingError: SQLite objects created in a thread can only
be used in that same thread`). This did not prevent that run from completing,
but it is an external Provider readiness issue and is not a Hub fix or a
production qualification. The ca-agent project must correct and retest its
database connection ownership before being treated as production-ready.
