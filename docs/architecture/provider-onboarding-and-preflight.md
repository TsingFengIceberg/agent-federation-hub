# Provider Onboarding and Local Preflight

> **Status**: implemented local tooling<br>
> **Evidence**: repository unit tests and command-level compilation; remote
> Card checks are runtime observations, not production qualification

This document defines the code-owned preparation path for an independently
deployed A2A Provider. The Provider remains a black box: onboarding consumes
only its public AgentCard and the operator's local admission policy. The Hub
does not inspect prompts, models, tools, memory, workflow graphs, or private
checkpoints.

## Onboarding command

Build or run `cmd/agent-onboard` to check a Card URL:

```bash
go run ./cmd/agent-onboard \
  --card-url https://provider.example/.well-known/agent-card.json \
  --profiles JSONRPC \
  --required-skills research \
  --require-streaming \
  --output json
```

The command can load the policy and Card URL from one registration in
`agent_config.yaml`:

```bash
go run ./cmd/agent-onboard \
  --agent-config agent_config.yaml \
  --agent-id competitive-analysis-agent \
  --output json
```

Checks cover the selected A2A Profile, required and allowed Skills, streaming,
Push, declared security schemes, and optionally a trusted AgentCard signature.
The report contains no credential values or credential references. A successful
report means that this Card satisfied the selected local policy at discovery
time; it does not prove provider business correctness, external trust
qualification, availability, or complete TCK conformance.

## Configuration runtime

The Hub accepts `--agent-config-reload-interval` for opt-in polling. Each new
file is parsed with strict YAML fields and validated before any remote Card is
checked. All enabled registrations are preflighted first; only then are local
records reconciled. A parse, discovery, policy, or application error retains
the last accepted configuration snapshot.

Entries removed or disabled by a later accepted snapshot are retained as
tenant-scoped Agent records with `healthStatus: STALE`. This avoids deleting
historical Task associations. They are excluded from normal routing until a
later accepted configuration re-enables and reconciles them.

The runtime callback is deliberately separate from the file watcher so a
future Registry-backed controller can reuse the same atomic snapshot and error
retention semantics.

## Preflight command

`cmd/hub-preflight` performs local checks before startup:

```bash
go run ./cmd/hub-preflight \
  --agent-config agent_config.yaml \
  --trust-bundle trust_bundle.json \
  --access-policy access_policy.json \
  --auth-mode oidc \
  --profile-matrix tests/conformance/profile-matrix.json \
  --output json
```

It validates local Agent configuration, Trust Bundle schema and time bounds,
access policy actions and scopes, TLS certificate/key pairing, and the
repository-owned A2A Profile matrix. Missing optional files are reported as
`skipped`; non-development authentication without a TLS pair fails closed.
No network call is made, and a passing preflight must not be described as
managed HA/DR, real partner trust, or production conformance.
