# Provider-Opaque Content and Workflow Templates

> **Status**: implemented product contract, with targeted deterministic Hub
> tests verified locally; the real `ca-agent + Coquo` run is opt-in and remains
> a qualification step<br>
> **Scope**: A2A-facing input Parts, observed Artifact dataflow, and reusable
> Hub workflow topology. This is not a Provider-runtime workflow language.

This document extends the Hub v1 boundary in
[`federation-hub-v1-contract.md`](../specifications/federation-hub-v1-contract.md).
The Provider still owns prompts, model selection, tools, memory, private
workflow graphs, checkpoints, and execution. The Hub coordinates only public
A2A Messages, Tasks, Artifacts, and aggregate dependencies.

## Content Input Contract

`POST /v1/tasks`, Task continuation, Workflow creation, Workflow continuation,
and template runs accept the following compatible shape:

```json
{
  "text": "Backward-compatible instruction shorthand",
  "parts": [
    {"kind": "data", "mediaType": "application/json", "data": {"priority": "high"}},
    {"kind": "file", "objectId": "tenant-owned-hub-artifact"}
  ]
}
```

`text` remains valid by itself. When present, the Hub inserts it as the first
canonical `text` Part so existing Providers retain their expected first
instruction. `parts` supports only:

| Kind | Required value | Hub behavior |
|---|---|---|
| `text` | nonblank `text` | forwarded as an A2A Text Part |
| `data` | JSON-compatible `data` | forwarded as an A2A Data Part |
| `file` | exactly one `bytesBase64`, HTTPS `uri`, or `objectId` | forwarded as Raw bytes, URL reference, or a tenant-authorized Hub Artifact |

`objectId` is not an arbitrary object-store key. Before the A2A adapter sees
it, the Artifact service verifies tenant ownership, availability, scan result,
integrity, encryption access, and outbound size. The selected content is then
materialized as a Raw A2A Part. A user-provided HTTPS URL is passed as a URL
reference and is not fetched merely to relay input. In contrast, a *returned*
remote A2A URL Artifact follows the separate Artifact ingestion policy before
the Hub persists it.

The Task input digest covers the normalized Parts, enabled extensions, and
extension metadata. The public Task/Event record retains the usual observable
Task information; durable Workflow input is held in the encrypted input vault,
not copied into the Workflow aggregate.

The implementation lives in [`internal/hub/parts.go`](../../internal/hub/parts.go)
and the selected A2A Binding mapping is in
[`internal/federation/a2a/adapter.go`](../../internal/federation/a2a/adapter.go).

## Observed Artifact Dataflow

A `WorkflowStep` may declare `artifactInputs`:

```json
{
  "fromStepId": "research",
  "artifactId": "report",
  "partIndex": 0
}
```

The source step must be a direct declared dependency and must have reached
`COMPLETED`. The Hub selects only its locally observed A2A Artifact Parts. It
does not open a Provider database, inspect a private checkpoint, infer a
missing report, or transfer a private internal context. A missing requested
Artifact is a workflow submission error rather than a silently incomplete
downstream prompt.

This enables an explicit edge such as:

```text
Provider A Task -> observed A2A Artifact -> Hub policy boundary -> Provider B Message Part
```

It is intentionally narrower than an internal Agent runtime's shared-memory
or prompt-graph mechanism. The enforcing coordinator is
[`internal/orchestration/workflow.go`](../../internal/orchestration/workflow.go).

## Template Catalog

The Hub exposes a small versioned catalog:

| Template | Topology |
|---|---|
| `single-agent` | one opaque Provider Task |
| `sequential-pipeline` | ordered stages; each later stage receives observed Artifacts from its predecessor |
| `parallel-fanout` | independent Tasks receive the same caller input concurrently |
| `review-revision` | draft Provider -> reviewer Provider -> original draft Provider with observed review Artifact |
| `human-approval` | one Provider Task; waits only when that Provider emits `INPUT_REQUIRED` |

Management endpoints are:

```text
GET  /v1/workflow-templates
POST /v1/workflow-templates/{templateID}/runs
```

They use the existing `workflows:read` and `workflows:write` actions. A
template compiles to the durable generic `WorkflowDefinition`; it does not add
a second orchestrator or invent a Provider-side approval state. The compiler
is [`internal/orchestration/templates.go`](../../internal/orchestration/templates.go).

## Evidence and Qualification

The repository's targeted Hub tests cover mixed text/data/file input,
tenant-bound Artifact object references, invalid Part rejection, Artifact
projection, encrypted Workflow input compatibility, and template compilation.
These establish repository-owned behavior only.

[`run-ca-agent-coquo-hub-smoke.sh`](../../tests/real-api/run-ca-agent-coquo-hub-smoke.sh)
is the opt-in process-level qualification path. It starts a real external
`ca-agent`, an independently deployed Coquo A2A Provider, and the Hub; it
checks AgentCard discovery, two registrations, a `sequential-pipeline`, remote
Task/Context IDs, both Artifacts, and the observed Artifact transfer.

The script requires `AFH_ALLOW_LIVE_CA_AGENT=1` because ca-agent may call its
configured model/API. By default Coquo uses its explicitly labelled
deterministic fixture Provider, proving A2A and Hub behavior but **not**
model-backed Coquo quality. `AFH_COQUO_PROFILE` or `AFH_COQUO_MODEL` selects a
separately authorized model-backed Coquo run. Neither mode establishes general
production compatibility, multi-organization trust, or domain correctness.

## Non-Goals

- No Provider source, prompt, tool, memory, Session ID, model route, approval
  transcript, or private checkpoint is admitted into the Hub contract.
- No arbitrary file-system or object-store reference is accepted as `objectId`.
- No implicit download of caller URL Parts is performed.
- No automatic human approval, exactly-once Provider side effect, or generic
  cross-runtime shared context is claimed.
