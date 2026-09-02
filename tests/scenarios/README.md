# Generality Scenario Matrix

> **Evidence status**: the matrix schema and deterministic runner are
> repository-owned. `verified` is reserved for scenarios with recorded local
> runner evidence; the JSON file is not a claim that every listed business
> scenario is implemented.

[`scenarios.json`](scenarios.json) separates business-specific Provider
behavior from generic Hub invariants. A scenario may change its domain
adapter, schemas, workflow, tools, policy, evaluator, or runtime without
changing the Hub's registry, routing, Task, Event, Artifact, identity, audit,
or recovery contracts.

Validate the matrix and run the deterministic two-Provider workflow smoke:

```bash
GO_BIN=/path/to/go tests/scenarios/run-matrix.sh
```

The default runner also executes `domain-provider-matrix`, which starts three
independently deployed, domain-labelled A2A fixtures for travel research,
procurement/finance, and incident response. It verifies skill routing,
Artifacts, input-required continuation, and tenant isolation. These fixtures
are deterministic evidence of Hub-generic behavior, not production business
logic or independent vendor implementations.

It also executes `multi-runtime-provider`, a Go SDK and Python SDK fixture
pair. This is the repository's deterministic evidence that the A2A boundary is
runtime-neutral; it does not imply conformance of every external SDK version.

List all scenarios without running a Provider:

```bash
tests/scenarios/run-matrix.sh --list
```

Run the real ca-agent cross-domain scenario explicitly:

```bash
AFH_RUN_EXTERNAL_SCENARIOS=1 \
GO_BIN=/path/to/go tests/scenarios/run-matrix.sh
```

The runner intentionally reports scenarios marked `external` without
inventing fake business logic. Those entries become runnable only when a
standards-compatible independent Provider and its adapter are supplied.

Workflow management API coverage is included in the Hub HTTP contract tests:
creation, tenant-scoped listing/reading, reconciliation, continuation,
compensation, and operator pause/resume/cancel controls. Task priorities are
covered by Journal lease-order tests; PostgreSQL uses the same persisted JSON
contract and ordering rule.
