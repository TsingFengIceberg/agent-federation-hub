# Live-provider A2A smoke test

> **Evidence status**: verified local execution on 2026-08-27 against the
> user-configured external Responses endpoint; not a conformance result

This optional test starts a Python A2A Agent whose private implementation calls
an OpenAI Responses API endpoint. The existing Go Hub probe
discovers and invokes it through A2A v1 JSON-RPC and SSE. The Hub receives no
provider API key and has no provider-specific code.

## Local inputs

The Agent reads model API credentials from the ignored root `.env` and
non-secret model API settings from the ignored root `model_config.yaml`. Their
committed, development-wide templates are
[`.env.example`](../../.env.example) and
[`model_config.example.yaml`](../../model_config.example.yaml). They intentionally contain
no Agent or test-runner settings.

Required local values are:

- `MODEL_API_KEY` in `.env`;
- `model_api.base_url` and `model_api.model` in `model_config.yaml`;
- a streaming OpenAI Responses-compatible `/responses` endpoint.

`model_api.api_key_env` may name a different environment variable. Literal
authorization or API-key headers in YAML are rejected so credentials cannot be
accidentally promoted into the committed example.

Agent addresses, ports, provider timeouts, temperature, and smoke-test prompts
are runtime concerns. They are selected through the Agent CLI or the `AFH_*`
variables in [`run-smoke.sh`](run-smoke.sh), not through the root configuration.

## Run

From the repository root:

```bash
GO_BIN=/path/to/go tests/real-api/run-smoke.sh
```

Optional alternate local files can be selected without changing the repository:

```bash
AFH_ENV_FILE=/secure/path/provider.env \
AFH_MODEL_CONFIG_FILE=/secure/path/provider.yaml \
GO_BIN=/path/to/go \
tests/real-api/run-smoke.sh
```

The script performs two paid/external calls:

1. a non-streaming A2A `SendMessage` whose aggregated Task must complete with a
   non-empty text Artifact;
2. an A2A `SendStreamingMessage` whose SSE stream must contain Artifact updates
   and a final `COMPLETED` status.

The assertions intentionally avoid exact model wording. Provider availability,
quota, latency, and output variability make this an integration signal rather
than a deterministic conformance test.

## ca-agent and Coquo workflow smoke

[`run-ca-agent-coquo-hub-smoke.sh`](run-ca-agent-coquo-hub-smoke.sh) is an
opt-in multi-Provider workflow check. It starts the separately maintained
ca-agent A2A server, Coquo's independently deployed A2A server, and this Hub,
then runs the `sequential-pipeline` template. The Hub must discover and
register both AgentCards, preserve remote Task/Context IDs, and pass the
observed ca-agent Artifact to Coquo as public A2A content.

The ca-agent leg can call its configured external model/API route, so the
explicit authorization flag is mandatory:

```bash
AFH_ALLOW_LIVE_CA_AGENT=1 \
GO_BIN=/path/to/go \
tests/real-api/run-ca-agent-coquo-hub-smoke.sh
```

Coquo defaults to its labelled deterministic fixture Provider. That validates
the process, protocol, and Artifact path, but is not evidence of a
model-backed Coquo run. To use a preconfigured Coquo model Profile instead,
also set `AFH_COQUO_PROFILE`; `AFH_COQUO_MODEL` may select/override the local
route. The script inherits `XDG_CONFIG_HOME` for this mode, so it can read an
existing Coquo profile. `AFH_COQUO_CONFIG_HOME` explicitly selects a different
existing configuration directory. Fixture mode uses a fresh temporary
configuration directory and never reads user profiles:

```bash
AFH_ALLOW_LIVE_CA_AGENT=1 \
AFH_COQUO_PROFILE=local-work \
GO_BIN=/path/to/go \
tests/real-api/run-ca-agent-coquo-hub-smoke.sh
```

The script does not print credentials. It uses temporary runtime state and
removes it on exit. A passing run is local integration evidence, not a claim
of production trust qualification or general Agent compatibility.

## Externally deployed Provider smoke

[`run-external-providers-smoke.sh`](run-external-providers-smoke.sh) is the
provider-agnostic path for two independently deployed A2A Providers. It does
not start, import, or inspect either Provider's runtime. Set their public Card
URLs and run a disposable Hub against the advertised A2A 1.0 JSON-RPC/SSE
contract:

```bash
AFH_PROVIDER_A_CARD_URL=https://provider-a.example/.well-known/agent-card.json \
AFH_PROVIDER_B_CARD_URL=https://provider-b.example/.well-known/agent-card.json \
GO_BIN=/path/to/go \
tests/real-api/run-external-providers-smoke.sh
```

For local loopback Providers only, add `AFH_ALLOW_PRIVATE_AGENT_URLS=1`.
`AFH_EXTERNAL_PROVIDER_REPORT_ROOT=/secure/report/dir` optionally retains a
sanitized JSON evidence report. The script checks Card discovery, independent
registration, remote Task/Context correlation, terminal Artifacts, and tenant
isolation. It intentionally runs the Hub in development authentication mode;
use the trust and Gateway checks separately for production identity evidence.

## Deterministic adapter check

[`run-mock-smoke.sh`](run-mock-smoke.sh) runs the same Agent and Hub path against
a local OpenAI Responses SSE fixture. It verifies request authentication,
provider chunk parsing, A2A Artifact updates, and terminal status without using
an external API. Passing the mock check does not establish compatibility with a
particular provider deployment.
