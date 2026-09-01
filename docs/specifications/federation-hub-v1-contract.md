# Agent Federation Hub v1 Product Contract

> **Status**: accepted product boundary for the next implementation phase<br>
> **Evidence**: repository-owned implementation and deterministic tests; partner
> deployment and complete A2A conformance remain qualification gates<br>
> **Version**: `1.0`

This document is the product-owned contract for the Hub. It narrows the
responsibility of a federation control plane without prescribing a provider's
language, framework, model, tools, memory, or internal workflow.

The machine-readable companion is
[`../../tests/conformance/hub-contract.json`](../../tests/conformance/hub-contract.json).
The A2A wire details remain pinned in
[`../../tests/conformance/a2a-profile.json`](../../tests/conformance/a2a-profile.json)
and [`../../tests/conformance/profile-matrix.json`](../../tests/conformance/profile-matrix.json).

## Product Boundary

The Hub is a multi-tenant federation control plane and observable Task
reconciler. It owns:

- Agent admission, Card snapshots, capability indexing, route selection, and
  health/admission state;
- authentication, tenant trust, authorization, credential references, audit,
  and policy enforcement at the Hub boundary;
- local federation Task identity, remote Task/Context correlation, delivery
  evidence, observable state, Event history, Artifact metadata, and recovery;
- optional Gateway mediation, Registry integration, Push reception, and
  durable event publication.

The Provider owns its execution. The Hub must not require or persist source
code, prompts, hidden reasoning, model selection, tools, private memory,
workflow graphs, subagent topology, or provider checkpoints.

Changing a business domain must normally change only a Provider adapter,
domain schema, workflow, policy, evaluator, or tool. It must not require a
change to the federation core for a Provider that still satisfies this
contract.

## Wire and Discovery Contract

The accepted initial wire profile is A2A protocol `1.0` with JSON-RPC over
HTTP and SSE streaming. HTTP+JSON/SSE and gRPC server-streaming are explicit
opt-in profiles, not silent SDK fallbacks. A2A AgentCard is authoritative for
the remote name, provider version, interface endpoint, capabilities, Skills,
media modes, and security declarations after admission checks.

An Agent can be admitted by an AgentCard URL or an external Registry adapter.
The Hub validates the Card and selected interface, records an immutable local
snapshot, indexes declared Skills, and verifies reachability separately. A
Card change, failed refresh, tenant mismatch, or revoked trust can remove an
Agent from routing without changing Provider code.

The Hub may route directly to the declared A2A endpoint or through an
operator-selected Gateway. Central Gateway traversal is a policy decision,
not a protocol requirement.

## Observable Task Contract

The normalized Task states are:

```text
SUBMITTED -> WORKING -> {INPUT_REQUIRED, COMPLETED, FAILED, CANCELED,
                         REJECTED, AUTH_REQUIRED}
INPUT_REQUIRED -> WORKING
```

`UNKNOWN` is reserved for an observation that cannot be mapped safely. The
terminal states are `COMPLETED`, `FAILED`, `CANCELED`, and `REJECTED`.
Terminal observations are not overwritten by late contradictory events.

The Hub exposes the following stable management operations:

| Operation | HTTP route | Required scope |
|---|---|---|
| register Agent | `POST /v1/agents` | `agents:write` |
| list Agents | `GET /v1/agents` | `agents:read` |
| refresh Agent Card | `POST /v1/agents/{agentID}/refresh` | `agents:write` |
| submit Message/Task | `POST /v1/tasks` | `tasks:submit` |
| continue `INPUT_REQUIRED` Task | `POST /v1/tasks/{taskID}/messages` | `tasks:continue` |
| read Task | `GET /v1/tasks/{taskID}` | `tasks:read` |
| read/follow Events | `GET /v1/tasks/{taskID}/events` | `tasks:read` |
| cancel Task | `POST /v1/tasks/{taskID}/cancel` | `tasks:cancel` |
| reconcile Task | `POST /v1/tasks/{taskID}/reconcile` | `tasks:reconcile` |
| receive Push | `POST /v1/tasks/{taskID}/push` | callback credential |
| read Artifact | `GET /v1/artifacts/{artifactID}[/{content}]` | `artifacts:read` |

The Hub returns immediate acceptance for a potentially long-running remote
Task. `remoteTaskId` and `remoteContextId` are preserved when observed.
Continuation reuses both identifiers and never silently creates a replacement
Task.

Delivery evidence is separate from Task state:

- `PENDING`: no accepted outbound observation exists;
- `ACKNOWLEDGED`: a Message, Task, status, or Artifact was observed;
- `AMBIGUOUS`: an outbound attempt began without a known remote Task ID and is
  not automatically replayed.

Polling, SSE, Push, and reconnect are convergence mechanisms. At-least-once
delivery and duplicate suppression are supported; exactly-once execution or
arbitrary external side-effect rollback is explicitly not promised.

## Artifact and Event Contract

Text, structured data, and file Parts are preserved as protocol-level
Artifacts. Untrusted file content is externalized through the configured
object store, MIME/size/quota checks, and malware policy before becoming
available. Tenant and Task ownership applies to metadata and content reads.

Every accepted observable mutation appends an ordered Event. The optional
Outbox is transactionally linked to that Event and delivers at least once with
stable tenant and deduplication identity. Consumers must be idempotent.

## Identity and Trust Contract

Management callers authenticate through one of the declared modes:

- OIDC/JWT with exact issuer, audience, time, tenant, algorithm, `jti`, and
  revocation checks;
- verified mTLS with exactly one SPIFFE URI SAN mapped to a Principal;
- hybrid mode, which never downgrades an invalid Bearer token to a client
  certificate identity;
- development headers only for local fixtures.

Production-shaped deployments require an explicit versioned trust bundle (or
the compatible legacy trust files), a versioned access policy, TLS, durable
rate limiting, and a local durable audit sink. External IdP, CA, PDP, token
exchange, and audit systems remain replaceable boundaries. Their production
availability and key-management operations must be qualified separately.

The unified bundle format and reload semantics are specified in
[`trust-bundle-contract.md`](trust-bundle-contract.md). The bundle is an
operator-distributed authorization snapshot; it is not an IdP, CA, or policy
decision service.

Credentials are SecretProvider references. Long-lived secrets must not appear
in AgentCards, Tasks, Artifacts, Events, logs, or committed configuration.

## Compatibility and Evidence Rules

The product profile is versioned independently from Provider implementation
versions. A profile change requires an explicit compatibility review and
machine-readable update. A passing local fixture is evidence for that fixture,
not a claim about every A2A implementation or a production deployment.

The contract is considered production-qualified only after all of the
following are separately evidenced: real partner identity/trust integration,
managed Registry/Gateway operation, complete selected A2A TCK coverage,
managed HA/DR with measured RPO/RTO, and independent multi-organization
Provider runs.
