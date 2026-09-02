# Test system

> **Evidence status**: deterministic, PostgreSQL 17, and MinIO integration layers verified locally; live-provider
> layer verified once on 2026-08-27 with local user-supplied configuration

The repository separates tests by the evidence they provide:

| Layer | Entry point | External API | Purpose |
|---|---|---|---|
| Unit | [`run-unit.sh`](run-unit.sh) | no | Go invariants plus live-adapter configuration and parsing tests |
| Hub contract | `go test ./internal/...` | no | Durable state, federated trust, Artifact policy/scanning, recovery, leases, tenancy, Push, HTTP, and adapters |
| PostgreSQL integration | [`postgres/run-integration.sh`](postgres/run-integration.sh) | local Docker | Real transactions, rollback, two-pool Task/Artifact leases, quota reservation, revocation, and inbox exclusion |
| MinIO integration | [`minio/run-integration.sh`](minio/run-integration.sh) | local Docker | Actual S3-compatible Artifact Put, Stat, Get, and Delete operations |
| Hub service smoke | [`hub/run-smoke.sh`](hub/run-smoke.sh) | no | Real Hub HTTP registration, A2A Task/Artifact exchange, and SSE replay against the Go fixture |
| Registry/Gateway control plane | [`hub/run-registry-gateway-smoke.sh`](hub/run-registry-gateway-smoke.sh) | local TLS loopback | HTTPS reference Registry/Gateway, Bearer propagation, publication/import, Send/Get/Subscribe routing, and stale local cache during Registry outage |
| Multi-Provider workflow | [`hub/run-federation-workflow-smoke.sh`](hub/run-federation-workflow-smoke.sh) | no | Concurrent fan-out, remote ID preservation, human approval continuation, fan-in, Artifact provenance, partial failure, and tenant isolation |
| Domain Provider matrix | [`hub/run-domain-provider-matrix-smoke.sh`](hub/run-domain-provider-matrix-smoke.sh) | no | Three independent domain-labelled Providers, skill routing, Artifact delivery, HITL continuation, and tenant isolation |
| Multi-runtime Provider | [`hub/run-multi-runtime-provider-smoke.sh`](hub/run-multi-runtime-provider-smoke.sh) | no | Independent Go and Python SDK Providers, cross-runtime A2A, Artifact/HITL continuation, remote correlation, and failure isolation |
| Durable outbox worker | `go test ./internal/core ./internal/worker` | no | Transaction-linked Event outbox, lease exclusion, publish-before-ack, retry, and Journal restart replay |
| A2A Push | [`hub/run-push-smoke.sh`](hub/run-push-smoke.sh) | local loopback | Pinned Go SDK HTTP Push sender to authenticated Hub receiver, including status and Artifact delivery |
| CloudEvents collector | [`hub/run-cloudevents-smoke.sh`](hub/run-cloudevents-smoke.sh) | local TLS loopback | CloudEvents 1.0 structured delivery, tenant identity, stable idempotency, and real HTTPS collector response |
| Federated trust integration | `AFH_RUN_TRUST_TESTS=1 go test ./internal/hub -run TestRealTrustBundleWithOIDCMTLSPDPAndOperations` | local TLS loopback | OIDC discovery/JWKS rotation, token revocation, HTTPS PDP, authenticated rate limiting, fsync audit, and CA-verified SPIFFE mTLS |
| Partner trust script | [`trust/run-partner-integration.sh`](trust/run-partner-integration.sh) | local TLS loopback | Repeatable partner-style IdP/PDP/central-audit success, key rotation, revocation, outage retry, and mTLS checks |
| A2A interoperability | [`interop/run-smoke.sh`](interop/run-smoke.sh) | no | Go Hub probe against independent Go and Python A2A Agents |
| Provider-adapter mock | [`real-api/run-mock-smoke.sh`](real-api/run-mock-smoke.sh) | no | Full A2A-to-provider path against a local compatible SSE endpoint |
| Live provider | [`real-api/run-smoke.sh`](real-api/run-smoke.sh) | yes | End-to-end A2A Task and SSE behavior around a real model call |
| Conformance pins | [`conformance/`](conformance/) | no | Machine-checked protocol/SDK/TCK revision and evidence status |
| Inspector/TCK | [`conformance/run-tck.sh`](conformance/run-tck.sh) | local TCK checkout | Repository-owned JSON-RPC/SSE SUT evidence with machine-readable waivers and explicit skipped bindings; not full multi-binding conformance |
| Generality scenario matrix | [`scenarios/run-matrix.sh`](scenarios/run-matrix.sh) | selected | Machine-readable domain scenarios mapped to provider-opaque Hub invariants; external business scenarios remain explicitly unimplemented |
| Foundation phase gates | [`phase-gates/run-foundation.sh`](phase-gates/run-foundation.sh) | selected | One repeatable entry point for baseline, trust, HA/DR, and A2A compatibility evidence; writes an ignored manifest and preserves external qualification skips |

The deterministic layers are the regression baseline and must not depend on
network availability, provider quotas, model output wording, or paid APIs. The
live-provider layer is opt-in. It proves integration behavior but is not a stable
conformance oracle.

Run the first four readiness gates and retain the generated evidence locally:

```bash
GO_BIN=/path/to/go tests/phase-gates/run-foundation.sh
```

Set `A2A_TCK_DIR`, `AFH_RUN_POSTGRES_TESTS=1`, or
`AFH_RUN_EXTERNAL_TRUST_TESTS=1` to enable the corresponding optional layers.
The resulting `var/phase-gates/manifest.json` always records whether a check
was passed, failed, or skipped and keeps `productionQualified` false.

OTLP trace export is disabled unless `--otlp-endpoint` is configured. HTTPS is
required by default; `--otlp-allow-http` is an explicit local-development opt-in
for a non-TLS collector and must not be used as a production security control.

The local control-plane smoke uses `cmd/reference-registry` and
`cmd/reference-gateway`. Both are intentionally in-memory HTTPS reference
servers for contract testing, not production Registry or Gateway deployments.
The Hub accepts `--registry-ca-file` and `--gateway-ca-file` for operator-owned
CA bundles. `--registry-import-tenants` enables startup import from the
external Registry, and `--registry-sync-interval` enables best-effort periodic
refresh; an outage retains the already validated local cache.

Control-plane clients also accept optional `--*-client-cert-file`,
`--*-client-key-file`, and `--*-server-name` settings for operator-managed
mTLS. Registry reads and Gateway `get`/`cancel`/`subscribe` use bounded retries;
Gateway `send` is deliberately never replayed. A local circuit breaker opens
after repeated dependency failures. These controls are exercised by transport
unit tests and the Registry/Gateway smoke, but managed service qualification
remains outside this repository.

The two-Provider workflow contract can be run directly:

```bash
GO_BIN=/path/to/go tests/hub/run-federation-workflow-smoke.sh
```

The durable Workflow aggregate and explicit compensation contract are covered
by `go test ./internal/orchestration`; the three-domain fixture can be run with:

```bash
GO_BIN=/path/to/go tests/hub/run-domain-provider-matrix-smoke.sh
```

Validate and run the generality matrix:

```bash
GO_BIN=/path/to/go tests/scenarios/run-matrix.sh
tests/scenarios/run-matrix.sh --list
```

Run all deterministic repository-owned tests with:

```bash
GO_BIN=/path/to/go tests/run-deterministic.sh
```

The PostgreSQL layer is opt-in within the aggregate script because it requires
Docker and the pinned `postgres:17-alpine` image:

```bash
AFH_RUN_POSTGRES_TESTS=1 GO_BIN=/path/to/go tests/run-deterministic.sh
```

The operator backup contract can be exercised independently with the same
PostgreSQL image. It creates a temporary custom-format archive, recreates the
database schema, restores it, and verifies a sentinel row:

```bash
tests/postgres/run-backup-restore.sh
```

This is a restore-path smoke test, not a substitute for encrypted managed
backups, retention policy, point-in-time recovery, or disaster-recovery drills.

The PostgreSQL restart recovery smoke keeps a sentinel row across a database
process restart:

```bash
tests/postgres/run-restart-recovery.sh
```

It validates persistence and reconnect readiness in the reference image; it is
not a primary/standby failover or managed-service qualification.

The MinIO layer is independently opt-in and uses a pinned disposable image:

```bash
GO_BIN=/path/to/go tests/minio/run-integration.sh
```

The trust integration is opt-in because it binds local TLS listeners and uses
generated keys and certificates:

```bash
AFH_RUN_TRUST_TESTS=1 go test ./internal/hub -run TestRealTrustBundleWithOIDCMTLSPDPAndOperations
```

## Configuration boundary

- [`.env.example`](../.env.example) declares secret variable names with empty
  values. The ignored root `.env` contains actual model API credentials.
- [`model_config.example.yaml`](../model_config.example.yaml) documents non-secret model API
  settings. The ignored root `model_config.yaml` contains the active API endpoint,
  protocol adapter, model, and non-secret compatibility headers.
- These root files are shared development configuration, not test-specific
  configuration. Agent addresses, ports, prompts, timeouts, and test behavior
  remain in command-line flags or test-script environment variables.
- [`agent_config.example.yaml`](../agent_config.example.yaml) is the committed
  template for external Agent registrations. The local `agent_config.yaml` is
  ignored and is reserved for deployment-specific Agent endpoints and policy.
- [`access_policy.example.json`](../access_policy.example.json) and
  [`tenant_trust.example.json`](../tenant_trust.example.json) are committed
  non-secret templates for non-development authorization and issuer/tenant
  trust. Local copies are ignored.
- API keys must not be placed in YAML configuration, A2A Messages, Artifacts,
  logs, or committed test output.
- `model_api.headers` is for non-secret routing or compatibility headers only.
  Secret headers require a future environment-variable mapping rather than a
  literal committed value.

The first live adapter targets the OpenAI Responses API wire shape documented
in the [official API reference](https://developers.openai.com/api/reference/resources/responses/methods/create/).
This is a replaceable test adapter, not a decision that the Hub depends on
OpenAI or on that provider protocol.

Task Events are transactionally mirrored into the durable outbox by both the
Journal and PostgreSQL stores. The outbox is an at-least-once publication
boundary: downstream publishers must be idempotent and should use the stored
deduplication key. CloudEvents 1.0 delivery is selected with
`--outbox-cloudevents-url`; operator listing, replay, and purge require
`outbox:read`/`outbox:write`. See [ADR 0007](../docs/adr/0007-explicit-a2a-profile-and-durable-outbox.md).

Run the owned TCK fixture against an existing checkout without cloning or
modifying upstream sources:

```bash
A2A_TCK_DIR=/path/to/a2a-tck \
A2A_TCK_PYTHON=/path/to/a2a-tck/.venv/bin/python \
GO_BIN=/path/to/go \
tests/conformance/run-tck.sh
```

The command writes `var/tck/owned-sut-result.json` and preserves the TCK exit
code. Read [`tck-waivers.json`](conformance/tck-waivers.json) together with the
report; a zero exit code covers the selected JSON-RPC/SSE run only, while
skipped bindings and revision waivers remain outside a complete conformance
claim.
