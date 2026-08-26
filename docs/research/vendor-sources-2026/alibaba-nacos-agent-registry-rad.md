# Alibaba Nacos: Protocol-Neutral Agent Registry and RAD

> **Sources**: [AI Registry specs at `2979345`](https://github.com/alibaba/nacos/tree/2979345f22a7883c7ebef98f4401c4b21622cee8/specs/en/ai), including [Agent Management](https://github.com/alibaba/nacos/blob/2979345f22a7883c7ebef98f4401c4b21622cee8/specs/en/ai/agent-management-spec.md), [A2A binding](https://github.com/alibaba/nacos/blob/2979345f22a7883c7ebef98f4401c4b21622cee8/specs/en/ai/a2a-agent-spec.md), and [RAD](https://github.com/alibaba/nacos/blob/2979345f22a7883c7ebef98f4401c4b21622cee8/specs/en/ai/rad-protocol-spec.md)<br>
> **Published in source history**: Agent model added 2026-07-21; ARD support added 2026-07-29; reviewed through 2026-08-24<br>
> **Evidence status**: `verified` official vendor specifications and source at the pinned commit; several contracts identify themselves as experimental and runtime conformance remains `to-verify`

## Relevant Content

Nacos defines a protocol-neutral Agent identity and version model. Each Agent version may expose one or more native call interfaces, with A2A as the first adapter rather than the generic model itself. The complete Agent Card stays in an A2A-native descriptor instead of being flattened into a union of protocol-specific fields.

Its Remote Agent Discovery (RAD) protocol separates five control-plane operations: Search, Discover, Watch, Register, and Deregister. RAD returns calling descriptors and currently available endpoints but explicitly does not proxy Agent messages, Tasks, sessions, streams, retries, or credentials. Runtime endpoints have a different lifecycle and publisher from catalog metadata and versioned definitions.

The A2A compatibility specification maps historical AgentCard APIs onto the canonical Agent model and states an A2A 1.0 descriptor baseline plus 0.x compatibility fields. The source also includes adapter work for Google's Agentic Resource Discovery proposal.

## What This Supports

The specifications provide direct evidence for separating a registry control plane from the A2A data plane. They also support keeping stable Agent identity, version governance, protocol-native descriptors, and live runtime endpoints as related but distinct facts.

## What This Does Not Prove

- Nacos does not become an A2A gateway or Task proxy by registering Agents.
- The RAD 0.1.0 and A2A compatibility contracts are marked experimental; interoperability outside Nacos has not been established here.
- Namespace visibility is not a complete cross-company trust, delegated authorization, or tenant-policy model.
- Source specifications and tests must still be checked against a running pinned Nacos build before promoting behavior to `verified` runtime evidence.

## Project Implications

- Use the Nacos model as a registry candidate and comparison baseline, not as an assumed platform dependency.
- Keep catalog identity, protocol version, native Agent Card, declared endpoint, and live endpoint as separate internal facts.
- Require a registry adapter to return connection metadata without silently taking ownership of A2A Task traffic.
- Compare RAD, ARD, direct Agent Card discovery, and any Hub-local index using one explicit discovery and revocation test matrix.
