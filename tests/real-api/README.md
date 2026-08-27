# Live-provider A2A smoke test

> **Evidence status**: verified local execution on 2026-08-27 against the
> user-configured external Responses endpoint; not a conformance result

This optional test starts a Python A2A Agent whose private implementation calls
an OpenAI Responses API endpoint. The existing Go Hub probe
discovers and invokes it through A2A v1 JSON-RPC and SSE. The Hub receives no
provider API key and has no provider-specific code.

## Local inputs

The Agent reads model API credentials from the ignored root `.env` and
non-secret model API settings from the ignored root `config.yaml`. Their
committed, development-wide templates are
[`.env.example`](../../.env.example) and
[`config.example.yaml`](../../config.example.yaml). They intentionally contain
no Agent or test-runner settings.

Required local values are:

- `MODEL_API_KEY` in `.env`;
- `model_api.base_url` and `model_api.model` in `config.yaml`;
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
AFH_CONFIG_FILE=/secure/path/provider.yaml \
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

## Deterministic adapter check

[`run-mock-smoke.sh`](run-mock-smoke.sh) runs the same Agent and Hub path against
a local OpenAI Responses SSE fixture. It verifies request authentication,
provider chunk parsing, A2A Artifact updates, and terminal status without using
an external API. Passing the mock check does not establish compatibility with a
particular provider deployment.
