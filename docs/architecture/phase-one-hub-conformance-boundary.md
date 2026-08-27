# Phase-One Hub and Conformance Boundary

> **Status**: implemented initial slice / production gaps explicit<br>
> **Updated**: 2026-08-27<br>
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
POST /v1/tasks/{id}/reconcile
POST /v1/tasks/{id}/push
```

Registration resolves an Agent Card and accepts only the selected A2A `1.0`
JSON-RPC interface. Calls use environment-variable credential references. Task
submission consumes A2A SSE observations, persists normalized events and
Artifacts, and follows the recovery rules in [ADR 0002](../adr/0002-durable-federation-task-reconciliation.md).

The event endpoint returns JSON or a finite SSE replay. `after` and
`Last-Event-ID` resume after a durable cursor. Continuous client fan-out and
backpressure are not implemented yet; periodic provider reconciliation remains
independent of an HTTP client connection.

## Boundary Matrix

| Area | Current executable evidence | Not yet claimed |
|---|---|---|
| A2A profile | Exact `1.0` JSON-RPC selection, SDK `v2.5.0`, SSE mapping | HTTP+JSON, gRPC, extensions, signed/extended Card policy |
| Durability | Append, `fsync`, replay, restart reconciliation | Multi-process safety, database transactionality, HA, backup, compaction |
| Recovery | Known-ID disconnect uses `GetTask`; unknown-ID send becomes ambiguous | Automated ambiguous-operation resolution or exactly-once execution |
| Authentication | Card-declared single-scheme credential loaded from an operator-allowlisted environment reference | OAuth refresh, mTLS, compound AND requirements, delegated identity |
| Tenancy | Tenant-scoped Agent/Task/Event/cancel/reconcile/Push with masked lookup | Authenticated tenant principal, RBAC/ABAC, quotas, encryption keys |
| Push receiver | Per-Task random Bearer secret hash, constant-time check, task/tenant match, size limit, dedup | Rate limiting, replay timestamp/signature, DNS-rebinding defense, queue/dead letter, HA ingress |
| Artifact | Text, raw bytes, URI, and arbitrary JSON data; append/replace semantics | URI retrieval, malware scanning, object storage, retention and content policy |
| Errors | Sanitized transport/auth/authz/validation/resource/state/protocol categories | Complete binding-specific status equivalence and tenant policy details |
| Registry | Durable built-in Agent Card registration by URL | Nacos/ARD adapter, Card signature verification, refresh and health policy |
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
  -journal var/hub.journal \
  -credential-env-allowlist REMOTE_AGENT_TOKEN
```

Every control-plane request requires `X-AFH-Tenant-ID`. The header is only a
tenant-scoping input in this slice; an authenticated ingress must establish and
overwrite it before production use. `-allow-private-agent-urls` exists only for
local fixtures. Push additionally requires a public HTTPS base URL.

## TCK Alignment

[`tests/conformance/a2a-profile.json`](../../tests/conformance/a2a-profile.json)
records the selected protocol source, SDK, Binding, and the exact TCK revision
previously evaluated. A deterministic Go test verifies that these pins and their
evidence status do not drift silently.

The external TCK remains `unresolved-revision-skew`, not passed. Its evaluated
revision pins an older A2A protocol source, and the available Go SUT lacks
Message-only, Artifact, `INPUT_REQUIRED`, ListTasks principal, and HTTP+JSON
behavior required by its scenarios. Repository-owned tests cover many of these
behaviors for the selected JSON-RPC/SSE slice, but cannot be counted as a TCK
waiver. Full conformance requires selecting an aligned TCK revision, running the
owned server as its SUT, and explaining every remaining skip or failure.

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
