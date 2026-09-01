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
POST /v1/agents/{id}/refresh
GET  /v1/outbox
POST /v1/outbox/{id}/replay
POST /v1/outbox/purge
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

Task submission may select an Agent explicitly with `agentId` or request a
declared capability with `skill`. Skill routing is tenant-scoped, deterministic
by registry order, and excludes registrations marked `UNHEALTHY`. The refresh
endpoint re-resolves the public AgentCard, updates endpoint/capability data,
and records the last health result; a failed discovery marks the registration
unhealthy until a later refresh succeeds.

## Boundary Matrix

| Area | Current executable evidence | Not yet claimed |
|---|---|---|
| A2A profile | Machine-checked exact `1.0` JSON-RPC+SSE selection, opt-in HTTP+JSON and gRPC adapter paths, gRPC Bearer metadata regression, signed-card round-trip, SDK `v2.5.0`, repository-owned three-Binding TCK SUT, and provider-SDK Push sender/Hub receiver smoke | extensions, signed/extended Card policy, production authentication and complete TCK Push coverage |
| Durability | Journal append/`fsync`/replay plus PostgreSQL Task/Event/outbox transactions, revisions, schema checksum ledger, two-pool lease tests, Outbox admin replay/retention | Managed database qualification, HA, encrypted backup/PITR, and cross-region replication |
| Recovery | Known-ID disconnect uses `GetTask`; unknown-ID send becomes ambiguous | Automated ambiguous-operation resolution or exactly-once execution |
| Authentication | Dynamic OIDC/JWKS, JWT validation/revocation, SPIFFE mTLS mapping, versioned Trust Bundle reload/rollback checks, external HTTPS policy, RFC 8693 exchange, Principal/scope policy, SecretProvider, local and central audit retry/outage tests | Production partner IdP/CA/PDP rollout, protected trust-bundle distribution, automated key management, consent, and operational SLO qualification |
| Tenancy | Authenticated Principal supplies tenant for every management lookup; forged JWT-mode tenant header is ignored | ABAC policy administration, quotas, tenant encryption keys, cross-organization trust federation |
| Push receiver | Per-Task Bearer hash, constant-time check, task/tenant/size controls, durable idempotent inbox, leased retry/ack, HTTPS/DNS/IP policy, authenticated rate limiting and audit; real Go SDK Push sender smoke covers status and Artifact delivery | Replay timestamp/signature, dead-letter policy, HA ingress load test, and production sender qualification |
| Artifact | Text, raw bytes, URI, and arbitrary JSON data; append/replace semantics; filesystem/S3 object storage, MIME/size/quota controls, ClamAV quarantine, authenticated retrieval, expiry leases | Encryption policy, DLP/legal hold, backup/restore, production throughput |
| Errors | Sanitized transport/auth/authz/validation/resource/state/protocol categories | Complete binding-specific status equivalence and tenant policy details |
| Registry | Durable built-in Agent Card registration by URL, skill selection, explicit Card refresh and health status; HTTPS external Registry publication/import, stale-cache marking, bounded reads, and best-effort periodic sync are covered by a local reference smoke | Nacos/ARD production adapter, Card signature trust distribution, conflict policy, and managed Registry health/SLO qualification |
| Background work | PostgreSQL/journal leases, heartbeats, expiry takeover, bounded retry; immediate A2A acceptance; durable Outbox worker with CloudEvents sink and admin replay/retention | Priority, preemption, operator pause/drain, broker partitioning and managed event-bus operations |
| Gateway | Direct A2A calls behind the federation Adapter interface; HTTPS external Gateway proxy with CA, optional mTLS client credentials, bounded safe retries, circuit breaking, Bearer configuration, Send/Get/Cancel/Subscribe forwarding, and explicit managed selection are covered by a local reference smoke | Managed agentgateway route, policy authoring, rate limiting, egress controls, and production Gateway SLO qualification |
| AAMP | AAMP 1.1 lifecycle/result/attachment mapper into the common model | SMTP/JMAP client, discovery, sender policy, pairing, mailbox credentials |

Remote Agent Card and selected endpoint URLs default to HTTPS and reject literal
private, loopback, link-local, multicast, and unspecified addresses plus common
private host suffixes. Local development can explicitly enable private Agent
URLs. The operator-configured Push callback has the same public HTTPS checks.
Production deployment still needs DNS resolution and revalidation on every
connection and redirect to close DNS-rebinding and hostname-based SSRF paths.
The Hub is the Push receiver in this slice; the remote Agent performs outbound
delivery. [`tests/hub/run-push-smoke.sh`](../../tests/hub/run-push-smoke.sh)
uses the pinned Go SDK's `HTTPPushSender` against the Hub's authenticated
callback. It is local executable evidence, not a production webhook or HA
ingress qualification.

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

The TCK is now aligned to the selected normative A2A `v1.0.0` source commit
`173695755607e884aa9acf8ce4feed90e32727a1` and is checked by
`tests/conformance/check-pins.sh`. At that pinned checkout, the repository-owned
SUT exits successfully for JSON-RPC (`81 passed, 154 skipped, 30 deselected`),
HTTP+JSON (`73 passed, 162 skipped, 30 deselected`), and gRPC (`62 passed, 173
skipped, 30 deselected`). This is still not a complete multi-binding claim:
authentication, Push sender, signed-card, and other requirements remain
explicit waivers. The
newer A2A mainline commit remains recorded as `latestProtocolCandidateCommit`
for a separately gated upgrade.

## Registry and Gateway Replaceability

The current `Store` provides a minimal local registry, while the federation
`Adapter` provides the data-plane boundary. Neither implies that the Hub must be
the canonical catalog or mandatory traffic proxy. Future Nacos or ARD discovery
can populate logical Agents, and an agentgateway route can implement the same
outbound contract. Direct A2A remains valid when tenant policy and trust permit
it; managed Gateway routing remains valid when policy requires central control.

The executable control-plane boundary is intentionally small. `cmd/reference-
registry` and `cmd/reference-gateway` provide local HTTPS contract fixtures;
they hold no durable production state. `--registry-import-tenants` imports
Agent Cards through the same discovery validation used for local configuration,
while `--registry-sync-interval` performs best-effort refresh and keeps the
last validated local cache when the Registry is unavailable. Gateway routing is
selected explicitly at Hub startup, and the direct A2A adapter remains the
default when no Gateway is configured. See
[`tests/hub/run-registry-gateway-smoke.sh`](../../tests/hub/run-registry-gateway-smoke.sh).

## Multi-Provider and Generality Validation

[`tests/hub/run-federation-workflow-smoke.sh`](../../tests/hub/run-federation-workflow-smoke.sh)
exercises two independently deployed Providers with concurrent fan-out,
remote Task/Context correlation, `INPUT_REQUIRED` continuation, Artifact
delivery, fan-in, partial Provider failure, and tenant isolation. It keeps
Provider runtime state opaque and does not implement a business workflow.

The machine-readable [`tests/scenarios/scenarios.json`](../../tests/scenarios/scenarios.json)
maps software, finance, research, AIOps, multimedia, asynchronous, robotics,
and marketplace scenarios to generic Hub invariants. The scenario runner only
executes scenarios with a repository-owned adapter; entries marked `planned`
or `external` are reported without fake business logic. Passing the runner is
evidence for the selected scenario, not a blanket generality or production
claim.

Workflow-level persistence and compensation are defined in
[`durable-federation-workflows.md`](durable-federation-workflows.md). The
current evidence covers Journal/PostgreSQL aggregate durability and explicit
provider-owned compensation Tasks; it does not claim rollback of arbitrary
external side effects.

## AAMP Boundary

The AAMP mapper is deliberately not an A2A emulation. AAMP `taskId` becomes the
remote Task correlation; `task.ack`, `task.help_needed`, and `task.result` map to
observable working, input-required, and terminal states; body, structured result,
and attachments become Artifact Parts. The AAMP mailbox thread remains its
authoritative asynchronous control plane. It does not become an in-domain Agent
orchestration runtime, and no SMTP/JMAP compatibility claim is made yet.

## Event Publication and Trust Evidence

The durable Outbox can publish to a configured HTTPS collector, a CloudEvents
1.0 structured-mode collector, or a local 0600 JSONL sink. All use stable
tenant/deduplication keys and at-least-once retry semantics. `GET /v1/outbox`,
the replay route, and the bounded purge route provide tenant-scoped operations;
the CloudEvents smoke verifies a real HTTPS collector. Broker partitioning,
consumer offsets, and managed event-bus SLOs remain deployment qualification.

The opt-in trust integration test exercises OIDC discovery and JWKS rotation,
JWT revocation, HTTPS policy decisions, rate limiting, durable local and
central audit, bounded exporter retries/outage recovery, and SPIFFE-mapped mTLS
using generated local certificates. It is repeatable partner-style evidence;
production IdP/CA/PDP deployment and key-management operations remain external
qualification work.

The preferred unified trust path is `--trust-bundle-file`, documented in
[`trust-bundle-contract.md`](../specifications/trust-bundle-contract.md). It
atomically binds OIDC issuers and SPIFFE workloads to tenants and policy
inputs, rejects generation rollback, and can be watched with
`--trust-bundle-reload-interval`. The bundle does not contain CA material;
verified mTLS still requires `--tls-client-ca-file`. The legacy trust files
remain migration-compatible but are mutually exclusive with the unified path.

## External Provider Readiness Note

The 2026-08-28 real ca-agent smoke completed A2A discovery, immediate
acknowledgement, SSE reconciliation, and Artifact delivery, but its provider
log also emitted a SQLite thread-affinity warning from the provider heartbeat
worker (`sqlite3.ProgrammingError: SQLite objects created in a thread can only
be used in that same thread`). This did not prevent that run from completing,
but it is an external Provider readiness issue and is not a Hub fix or a
production qualification. The ca-agent project must correct and retest its
database connection ownership before being treated as production-ready.
