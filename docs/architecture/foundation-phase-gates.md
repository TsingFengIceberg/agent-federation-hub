# Foundation Phase Gates

> **Status**: accepted execution contract for phases 1-4<br>
> **Evidence**: repository-owned checks are executable locally; external
> partner and managed-service qualification remain `planned` until run against
> operator infrastructure.

This document turns the first four implementation phases into repeatable gates.
The single entry point is [`tests/phase-gates/run-foundation.sh`](../../tests/phase-gates/run-foundation.sh).
It writes a machine-readable `manifest.json`, a human-readable `summary.md`,
and the preflight/conformance reports below `var/phase-gates/` (which is
ignored).

## Phase 1: Freeze the v1 baseline

The baseline is the accepted Hub product contract, the pinned A2A v1.0.0
protocol/Go SDK/TCK revisions, and the deterministic unit, race, shell, and
scenario suites. CI must run these checks on every push and pull request and
upload the generated reports. A TCK checkout is required in the CI TCK job;
without `A2A_TCK_DIR`, the local script records an evidence-boundary skip.

## Phase 2: Production trust and security

Repository code now supplies the replaceable boundaries for OIDC/JWKS,
SPIFFE/mTLS, policy decisions, token exchange, revocation, rate limiting,
SecretProvider references, signed AgentCard verification, encrypted Artifacts,
and chained local plus retrying central audit. The partner-style test proves
the boundary behavior with generated local services.

Production acceptance still requires all of the following outside this
repository:

- a real OIDC issuer with JWKS rotation and an issuer-to-tenant Trust Bundle;
- an operational CA/SPIRE (or equivalent) and workload certificate rotation;
- a fail-closed PDP with policy publication and outage behavior;
- KMS/HSM-backed implementations of the existing `artifact.KeyProvider` and
  workflow input key boundary, with key rotation and recovery evidence;
- protected, signed or otherwise integrity-authenticated Trust Bundle
  distribution and rollback procedures;
- consent/delegation policy and centralized audit retention.

The local `StaticKeyProvider` is test/development-only. No local fixture may
be promoted as evidence for any of these external requirements.

## Phase 3: Hosted infrastructure and recovery

PostgreSQL is the authoritative multi-instance store; object storage is the
Artifact data plane; leases, outbox retries, dead letters, readiness, and
graceful worker drain are Hub contracts. The local drills cover restart,
standby promotion, PITR, encrypted backup handling, and versioned MinIO
replication. `tests/dr/run-all.sh` is the repeatable entry point.

The remaining deployment gate is a managed environment with measured RPO/RTO,
cross-zone or cross-region failure injection, rolling schema upgrades, broker
partition/dead-letter drills, object restore, alerting, and documented SLOs.

## Phase 4: A2A compatibility convergence

The default external profile remains A2A `1.0` JSON-RPC over HTTP with SSE.
HTTP+JSON/SSE and gRPC server-streaming are explicit opt-in profiles. The
machine-readable matrix and waiver document are checked for exact protocol,
SDK, and TCK pins; no failed MUST requirement is accepted. Authentication and
signed-card trust distribution remain explicit waivers until the selected TCK
and partner providers exercise them.

Every profile upgrade must update the pins, rerun the three-Binding matrix,
review SDK behavior, and either remove a waiver with evidence or add a new
time-bounded waiver. A zero exit code from the pinned fixture is not a claim of
complete conformance.

## Running the gates

```bash
GO_BIN=/path/to/go tests/phase-gates/run-foundation.sh
```

Optional evidence layers are enabled explicitly:

```bash
A2A_TCK_DIR=/path/to/a2a-tck \
AFH_RUN_POSTGRES_TESTS=1 \
GO_BIN=/path/to/go tests/phase-gates/run-foundation.sh
```

The script exits non-zero for a failed executable check. Skipped external
checks remain visible in the manifest and never set `productionQualified` to
true.
