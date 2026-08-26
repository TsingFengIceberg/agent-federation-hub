# ByteDance: AgentKit Hybrid-Cloud A2A Customer-Service Sample

> **Source**: [ByteDance AgentKit Samples at `0890278`](https://github.com/bytedance/agentkit-samples/tree/0890278a6571e4190c1ab8cacd9becc147f74d9c/python/02-use-cases/hybrid_cloud_customer_service)<br>
> **First added**: 2026-07-26 in [`d5e58a8`](https://github.com/bytedance/agentkit-samples/commit/d5e58a8f05928ba54730102a72e888713f11a144)<br>
> **Evidence status**: `verified` official vendor sample and documentation; successful deployment and platform behavior remain `to-verify` locally

## Relevant Content

The sample separates a customer-service Agent and a complaint-analysis Agent into two independent AgentKit Runtimes. Its documented path is:

```text
Agent Card discovery
  -> validate the selected Agent name and skills[].id
  -> A2A message/send
  -> receive a Task Artifact
  -> correlate traces on both Runtimes with one canary value
```

The provider Runtime owns its model, implementation, API key, Agent Card, and A2A endpoint. An A2A center registers multiple Agents and supplies a governance and discovery entry point. The caller keeps the selected peer URL, name, capability ID, timeout, and credential in its own Runtime configuration.

The sample also keeps A2A identity separate from inbound Runtime authentication. Its OAuth validation uses a sibling Runtime so that changing the authentication profile does not invalidate the previously tested A2A and component chain.

## What This Supports

This source provides a concrete 2026 vendor example of A2A across independently deployed Runtimes, including discovery, capability selection, authenticated invocation, Artifact return, and evidence on both sides of the boundary. It also demonstrates that A2A can coexist with a local Agent runtime, Knowledge, Memory, MCP, Skills, sandboxing, evaluation, and tracing.

## What This Does Not Prove

- The two Runtimes are demonstrated inside one AgentKit platform environment, not across unrelated companies or trust domains.
- Registration in the A2A center does not by itself prove network reachability, authorization, or successful invocation.
- The sample's API-key propagation is not a general delegated-user authorization design.
- The documentation and source have not yet been executed in this repository.

## Project Implications

- Reuse the sample's three-part conformance evidence: Card response, A2A response, and correlated caller/provider traces.
- Test registry visibility and data-plane reachability as separate conditions.
- Keep peer service credentials, caller identity, delegated user identity, and tenant policy as different concepts.
- Include at least two independently deployed Runtimes in the first integration scenario, even if both initially run under one test organization.
