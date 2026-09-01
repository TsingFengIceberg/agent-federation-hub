# Agent Configuration

> **Status**: implemented loader, startup registration, and opt-in atomic reload<br>
> **Evidence**: local unit tests and `go test ./...`<br>
> **Scope**: operator-owned remote Agent registration; provider behavior remains defined by A2A AgentCard

## File Boundary

Model provider settings and remote Agent registrations are separate:

- [`model_config.example.yaml`](../../model_config.example.yaml) documents the
  local model API settings used by the live-provider fixture. The ignored
  `model_config.yaml` contains the operator's local values.
- [`agent_config.example.yaml`](../../agent_config.example.yaml) documents
  remote Agent registrations. The ignored `agent_config.yaml` contains
  environment-specific endpoints and credential references.
- Secrets are resolved through the operator's `SecretProvider`; YAML contains
  only environment-variable references, never credential values.

The Hub loads `agent_config.yaml` at startup through
[`internal/agentconfig`](../../internal/agentconfig/). Missing configuration is
allowed for development and starts the Hub with no configured Agents. An
invalid existing file fails startup. Only entries with `enabled: true` are
registered; registration discovers the remote AgentCard before persisting the
Agent.

## Discovery and Authority

Static configuration expresses local constraints: tenant ownership, accepted
A2A binding profiles, required capabilities, credential references, limits,
timeouts, and routing metadata. The remote AgentCard remains authoritative for
the discovered Agent name, version, endpoint, protocol interfaces, security
schemes, capabilities, and skills. A registration is rejected when the Card
does not satisfy the configured protocol, capability, or skill requirements.

The initial accepted profile is A2A `1.0` JSON-RPC with SSE streaming. Additional
profiles may be listed in order after their adapter and tests are implemented;
the configuration does not silently widen the adapter's supported profile.

## Lifecycle

```text
agent_config.yaml
    -> schema and policy validation
    -> AgentCard discovery
    -> capability/profile/credential checks
    -> tenant-scoped Agent Registry record
    -> A2A Task submission and reconciliation
```

The configuration file loads during process startup. The optional
`--agent-config-reload-interval` enables atomic polling-based reconciliation;
an invalid or unapplied candidate retains the last accepted snapshot. The
authenticated `POST /v1/agents/{id}/refresh` operation provides explicit runtime
Card refresh and health marking without changing the operator-owned credential
references.
Task callers may use `skill` instead of `agentId`; the Hub chooses a healthy
tenant-local registration whose Card declares that skill. A future scheduled
controller may add richer Registry reconciliation, but must preserve the same
AgentCard authority and tenant/credential boundaries. Removed or disabled
entries are retained as `STALE` records rather than deleted, preserving
historical Task associations.

## Local Setup

1. Copy `model_config.example.yaml` to `model_config.yaml` and fill in the
   model endpoint and environment-variable name.
2. Copy `agent_config.example.yaml` to `agent_config.yaml`.
3. Set the provider `card_url`, tenant, required skill, and credential
   reference. Keep `enabled: false` until the provider passes the A2A smoke
   test; then enable it and restart the Hub, or use the opt-in reload flag.
4. Add the referenced secret name to the operator credential allowlist and put
   its value in the local environment.
5. Start the Hub with `--agent-config agent_config.yaml` (the default path) and
   inspect the startup log for AgentCard discovery and registration failures.

The configuration layer is generic and does not contain `ca-agent` internals.
The `ca-agent` project must expose a standards-compatible A2A Provider; the Hub
uses only its public AgentCard and A2A Message/Task/Artifact contract.
