# Google-Derived A2A Platform Outline

> **Source basis**: five verified Google publications from 2026, linked below<br>
> **Evidence status**: source statements are `verified`; the combined platform outline and product implications are `draft`<br>
> **Ownership**: product-specific research input for Agent Federation Hub, not a Google reference architecture or a committed project architecture

## Source Basis

- [`google-a2a-collaborative-agents.md`](google-a2a-collaborative-agents.md): opaque delegation to independently managed Agent workloads.
- [`google-cross-language-adk-a2a.md`](google-cross-language-adk-a2a.md): A2A interoperability alongside ADK orchestration across Python and Go services.
- [`google-agent-protocols-guide.md`](google-agent-protocols-guide.md): separate protocol responsibilities for Agent peers, tools, commerce, payment, and UI.
- [`google-agentic-resource-discovery.md`](google-agentic-resource-discovery.md): organization-owned catalogs, verifiable metadata, and federated registry indexing.
- [`google-long-running-adk-agents.md`](google-long-running-adk-agents.md): provider-side durable workflows, checkpoints, event-driven waiting, and human approval.

These publications were released separately. The architecture below is this project's synthesis of their compatible boundaries; Google has not published it as one complete Hub design.

## Derived System Boundary

```text
Organization A                                      Organization B
+---------------------------+                      +---------------------------+
| Domain Agent System       |                      | Domain Agent System       |
| ADK / LangGraph / custom  |                      | ADK / custom / service    |
| workflow, tools, memory,  |                      | private state, checkpoints|
| checkpoints, subagents    |                      | and proprietary logic     |
+-------------+-------------+                      +-------------+-------------+
              | A2A client                                      | A2A server
              +-------------------+     +------------------------+
                                  |     |
                       +----------v-----v----------+
                       | Federation Data Plane     |
                       | auth, policy, routing,    |
                       | version negotiation,      |
                       | streaming/push telemetry  |
                       +------------+--------------+
                                    |
                       +------------v--------------+
                       | Federation Control Plane  |
                       | catalog index, discovery, |
                       | trust metadata, tenant    |
                       | policy, audit, conformance|
                       +------------+--------------+
                                    |
                +-------------------v-------------------+
                | Organization-owned ARD catalogs and  |
                | protocol-native Agent Cards          |
                +---------------------------------------+
```

The Hub connects domain-level Agent systems. It does not import their prompts, tools, memory, workflow graphs, or execution checkpoints.

## Derived Design Principles

1. **Keep the remote Agent opaque.** A provider owns its execution plan, sensitive data, dependencies, internal state, and result quality. The federation contract exposes capability metadata, Messages, Tasks, status, and Artifacts.
2. **Separate local orchestration from federation.** ADK, LangGraph, and equivalent runtimes keep workflow sequencing, subagents, local tool access, checkpoints, and human approval. A2A connects independently deployed systems.
3. **Separate discovery from invocation.** ARD-like catalogs and registries identify resources and native protocols. A2A carries Agent interactions. A registry does not become a Task proxy merely by indexing an Agent.
4. **Keep catalog ownership with the publisher.** An organization publishes metadata under its domain. Registries index and verify it without becoming the canonical owner of the underlying Agent.
5. **Use Agent Cards as protocol-native connection contracts.** A client selects a compatible interface from declared capabilities, versions, endpoints, and security requirements instead of relying on per-Agent glue code.
6. **Do not stretch A2A across unrelated responsibilities.** MCP remains a tool/data protocol. Commerce, payment, and UI protocols stay separate and are added only for validated product scenarios.
7. **Treat a remote interaction as a Task lifecycle, not only an RPC.** A provider can reject work, request input, continue asynchronously, stream updates, or return multiple Artifacts.
8. **Separate provider workflow state from Hub reconciliation state.** Provider checkpoints resume private execution. The Hub stores observable remote references, delivery evidence, and its own reconciliation decisions.
9. **Support polling, streaming, and push as delivery modes over one logical Task.** A stream disconnect or duplicate callback does not determine the Task's final state; the Hub must reconcile with the Task owner.
10. **Make heterogeneous implementations a baseline test.** The first meaningful integration should cross at least two languages, SDKs, frameworks, or deployment owners.
11. **Layer identity and authorization.** Organization provenance, Agent metadata identity, service authentication, caller identity, delegated user identity, network reachability, and tenant authorization are separate checks.
12. **Correlate evidence without requiring shared internals.** Hub routing traces, caller traces, provider traces, Task identifiers, Artifact provenance, push attempts, and approval records need a common correlation strategy.
13. **Negotiate protocol versions and bindings explicitly.** Tutorials and samples can lag the current specification. The selected profile must come from the current A2A specification and be validated with contract tests and Inspector/TCK tooling.

## Draft Hub Capabilities

### 1. Federated Discovery

- ingest organization-hosted catalogs and direct Agent Card URLs;
- verify source domain, signatures where supported, freshness, and revocation state;
- index capability, provider, protocol, version, media, and policy metadata;
- resolve a logical Agent to compatible declared and live endpoints;
- cache metadata without silently becoming its canonical owner;
- compare ARD, direct discovery, and external registry adapters behind one interface.

### 2. Trust, Identity, And Policy

- authenticate publishing organizations and callable services;
- represent caller Agent, tenant, and delegated user identity separately;
- select credentials by target, tenant, audience, and requested operation;
- apply local organization policy and Hub policy with an auditable decision order;
- enforce allow/deny, rate, quota, data-residency, and approval requirements;
- retain decision evidence without logging credentials or unnecessary prompt content.

Google's reviewed publications establish the need for these boundaries but do not supply a complete cross-organization delegated-authorization design. Token exchange, multi-issuer trust, and authority attenuation remain open work.

### 3. A2A Federation Data Plane

- select A2A version, binding, endpoint, and declared extension per request;
- forward or directly coordinate Messages, Tasks, status operations, cancellation, and push configuration;
- support JSON-RPC, HTTP+JSON, or gRPC only where the selected compatibility profile requires them;
- apply authentication, authorization, routing, timeout, size, and content policies;
- propagate safe correlation metadata and emit protocol-aware traces;
- avoid automatic retries when a timed-out operation may already have produced side effects.

The deployment should support both direct provider connections and policy-enforced gateway traversal. The Hub should not require every data-plane call to pass through a central proxy when policy permits a verified direct connection.

### 4. Task Reconciliation And Delivery

- map a local federation operation to the provider-owned A2A Task and Context identifiers;
- record the last observed state and evidence source rather than claiming ownership of provider execution;
- consume streaming events, push callbacks, and polling results through one normalized event path;
- deduplicate delivery attempts and preserve ordering evidence without promising global exactly-once delivery;
- reconcile ambiguous timeout, cancellation, restart, and terminal-state races;
- expose human-action requirements without implementing the provider's private approval workflow.

### 5. Artifact And Content Handling

- preserve protocol-native text, file, structured-data, and media Parts;
- record Artifact identity, media type, size, digest, source Agent, Task, tenant, and retention policy;
- choose inline, object-store, or provider-reference delivery by policy and size;
- scan untrusted content and prevent Agent Card, Message, and Artifact fields from becoming unchecked prompt input;
- keep business-specific schemas outside the generic federation core.

### 6. Provider And Consumer Integration

- offer a small server adapter for ADK, LangGraph, custom runtimes, and deterministic services;
- offer a client adapter that can represent a remote A2A Agent inside a local orchestrator;
- generate or validate Agent Cards without exposing runtime internals;
- provide local test fixtures for Task, streaming, push, cancellation, and Artifact behavior;
- let the domain runtime retain workflow state, memory, tools, evaluation, and checkpoints.

### 7. Operations And Conformance

- provide tenant administration, policy management, credential references, quotas, and audit search;
- expose discovery, routing, Task observation, callback attempts, and Artifact provenance;
- run compatibility tests for every supported A2A version and binding;
- test with independently implemented clients and servers rather than only one SDK;
- distinguish protocol conformance, platform reachability, business correctness, and production readiness.

## Explicit Non-Goals

- replacing ADK, LangGraph, AgentScope, or another in-domain runtime;
- centralizing the private state or workflow graph of every connected Agent;
- treating every remote Agent as a deterministic REST tool;
- using one generic protocol for Agent peers, tools, payments, commerce, and UI;
- claiming that registry visibility proves reachability, authorization, or successful Task execution;
- claiming exactly-once side effects from transport retries or duplicate suppression alone.

## Suggested Validation Sequence

1. Select the current A2A version and one initial binding; implement a minimal conformant client/server pair.
2. Add a second language or SDK and validate Agent Card discovery, Message-or-Task response, Artifact return, and error mapping.
3. Add streaming, push, reconnect, cancellation, and provider-restart scenarios with an explicit Task reconciler.
4. Add one local orchestrator that treats a remote A2A service as a subagent while retaining its own checkpoints.
5. Compare direct Agent Card discovery, ARD, and an external registry adapter without coupling Task traffic to the registry.
6. Add organization, service, caller, delegated-user, and tenant-policy checks as distinct test assertions.
7. Run Inspector/TCK where applicable and retain wire-level evidence before promoting compatibility claims to `verified`.

## Open Questions Not Settled By The Google Sources

- How should different organizational identity providers establish and revoke trust?
- What authorization token represents an attenuated user delegation across multiple Agent hops?
- Which calls must traverse the Hub gateway, and which may use verified direct connections?
- What is the minimum durable Hub state for ambiguous delivery, cancellation, and compensation?
- How are billing, pricing, quotas, liability, and dispute evidence represented across organizations?
- How mature and interoperable is ARD, and how should it coexist with existing registries and Agent Cards?
