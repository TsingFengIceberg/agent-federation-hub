# Durable Federation Workflows

> Evidence status: `verified` for the repository-owned Journal implementation,
> deterministic tests, and local PostgreSQL schema; production workflow
> qualification is still `planned`.

The Hub-owned Workflow aggregate coordinates several opaque A2A Provider
Tasks. It is intentionally separate from provider-internal orchestration such
as LangGraph. A workflow stores only the Provider identity, the remote Task
correlation held by the Hub's child Task, observable state, and explicit
compensation intent.

## Aggregate

`core.Workflow` contains a tenant-scoped ID, ordered `WorkflowStep` records,
an aggregate state, a monotonic revision, and an append-only event sequence.
Each step stores a local Task ID and may declare a provider-owned
`compensationText`. No prompt graph, tool list, memory, or checkpoint is copied
from a Provider.

The Journal Store persists the aggregate and event in one append operation and
replays it after restart. PostgreSQL persists the workflow row and event in a
serializable transaction with optimistic revision checks and a unique
tenant/workflow/dedup key. A Store that does not implement `core.WorkflowStore`
is rejected instead of silently falling back to volatile state.

## State semantics

- `PENDING` is the state before child submission begins.
- `RUNNING` means at least one child is not terminal.
- `WAITING_INPUT` means one or more child Tasks require human input.
- `COMPLETED` means every child completed successfully.
- `FAILED` means every child failed or no successful branch exists.
- `PARTIALLY_FAILED` preserves completed branches alongside failed branches.
- `COMPENSATING` records explicit compensating child Tasks in progress.
- `COMPENSATED` means all requested compensations completed.
- `PAUSED` means Hub reconciliation and continuation are paused by an
  operator; Provider-owned execution is not forcibly frozen.

`ReconcileWorkflow` refreshes child Tasks by their preserved remote Task and
Context IDs. It may be called after process restart and never resubmits a child
merely because a stream was interrupted.

## Input Parts and Templates

Each durable input vault record contains the caller's `text` shorthand and
ordered protocol-level Parts. The aggregate retains only an encrypted input
reference and digest. A Step can declare Artifact inputs only from completed,
direct dependencies; the coordinator projects the locally observed A2A
Artifact Parts into the downstream child Message without inspecting Provider
internals.

The Hub also offers a small versioned template catalog for single-Agent,
sequential, parallel fan-out, review/revision, and provider-emitted human
approval topologies. Templates compile into this same `WorkflowDefinition`
and durable state machine. They are Hub-level topology recipes, not a DSL for
Provider prompts, memory, tools, or internal workflows. See
[`provider-opaque-content-and-templates.md`](provider-opaque-content-and-templates.md).

## Compensation

Compensation is opt-in per step. `CompensateWorkflow` is allowed only for a
failed or partially failed aggregate, submits one new A2A Task per completed
step with a declared compensation text, and records each compensation Task ID
and state. Submission and status updates are deduplicated. A failed
compensation remains visible as `FAILED`; the Hub does not claim rollback or
exactly-once side effects.

The deterministic coverage is in
[`internal/orchestration/workflow_test.go`](../../internal/orchestration/workflow_test.go).
The multi-Provider HTTP smoke continues to validate remote correlation,
partial failure, and tenant isolation in
[`tests/hub/run-federation-workflow-smoke.sh`](../../tests/hub/run-federation-workflow-smoke.sh).

## Remaining qualification

The current implementation does not provide a general Provider-runtime
workflow language, managed cross-region workflow storage, or domain-specific
side-effect compensation guarantees. Hub Workflow definitions, a bounded
template catalog, operator pause/resume/cancel controls, and bounded priority
scheduling are repository-owned APIs, but still require production drills
before a production SLO claim.
