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
| A2A interoperability | [`interop/run-smoke.sh`](interop/run-smoke.sh) | no | Go Hub probe against independent Go and Python A2A Agents |
| Provider-adapter mock | [`real-api/run-mock-smoke.sh`](real-api/run-mock-smoke.sh) | no | Full A2A-to-provider path against a local compatible SSE endpoint |
| Live provider | [`real-api/run-smoke.sh`](real-api/run-smoke.sh) | yes | End-to-end A2A Task and SSE behavior around a real model call |
| Conformance pins | [`conformance/`](conformance/) | no | Machine-checked protocol/SDK/TCK revision and evidence status |
| Inspector/TCK | unresolved revision alignment | no | Future aligned protocol conformance and explained waivers |

The deterministic layers are the regression baseline and must not depend on
network availability, provider quotas, model output wording, or paid APIs. The
live-provider layer is opt-in. It proves integration behavior but is not a stable
conformance oracle.

Run all deterministic repository-owned tests with:

```bash
GO_BIN=/path/to/go tests/run-deterministic.sh
```

The PostgreSQL layer is opt-in within the aggregate script because it requires
Docker and the pinned `postgres:17-alpine` image:

```bash
AFH_RUN_POSTGRES_TESTS=1 GO_BIN=/path/to/go tests/run-deterministic.sh
```

The MinIO layer is independently opt-in and uses a pinned disposable image:

```bash
GO_BIN=/path/to/go tests/minio/run-integration.sh
```

## Configuration boundary

- [`.env.example`](../.env.example) declares secret variable names with empty
  values. The ignored root `.env` contains actual model API credentials.
- [`config.example.yaml`](../config.example.yaml) documents non-secret model API
  settings. The ignored root `config.yaml` contains the active API endpoint,
  protocol adapter, model, and non-secret compatibility headers.
- These root files are shared development configuration, not test-specific
  configuration. Agent addresses, ports, prompts, timeouts, and test behavior
  remain in command-line flags or test-script environment variables.
- API keys must not be placed in YAML configuration, A2A Messages, Artifacts,
  logs, or committed test output.
- `model_api.headers` is for non-secret routing or compatibility headers only.
  Secret headers require a future environment-variable mapping rather than a
  literal committed value.

The first live adapter targets the OpenAI Responses API wire shape documented
in the [official API reference](https://developers.openai.com/api/reference/resources/responses/methods/create/).
This is a replaceable test adapter, not a decision that the Hub depends on
OpenAI or on that provider protocol.
