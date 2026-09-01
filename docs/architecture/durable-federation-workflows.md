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

`ReconcileWorkflow` refreshes child Tasks by their preserved remote Task and
Context IDs. It may be called after process restart and never resubmits a child
merely because a stream was interrupted.

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

The current implementation does not yet provide a workflow definition
language, operator pause/drain, priority scheduling, a managed workflow
store, or domain-specific side-effect compensation guarantees. Those require
separate ADRs and production drills before a production SLO claim.
