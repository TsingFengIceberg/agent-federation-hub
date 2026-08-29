# Opaque Agent Federation Architecture Review

> **Review date**: 2026-08-26<br>
> **Evidence status**: verified source review / draft product architecture<br>
> **Scope**: Google 2026 publications, the pinned A2A protocol source, and representative runnable open-source systems; this is not an accepted ADR

## Review Questions

1. Does the current Agent Federation Hub architecture align with the reviewed Google publications, the A2A protocol, and representative runnable A2A systems?
2. Should a natively connected remote Agent remain opaque to the Hub?

## Conclusion

The current architecture is directionally aligned. Its core responsibilities -- discovery, trust, routing, A2A protocol handling, observable Task reconciliation, Artifact handling, policy, audit, and conformance -- are supported by the reviewed sources.

Opaque Agent integration must be a first-class product invariant:

> A conformant remote A2A Agent can join through an Agent Card URL, a reachable declared interface, and any required credential reference without exposing its source code, prompts, model, tools, memory, workflow graph, subagents, or private checkpoints, and without requiring a change to the Hub core.

Two architecture corrections are required:

1. The Hub owns **Task observation, identifier mapping, delivery evidence, and reconciliation**, not the provider's private execution or checkpoints.
2. The Hub supports both a policy-enforced managed gateway path and a verified direct A2A path. Registration or discovery does not require every data-plane call to traverse a central proxy.

These conclusions remain `draft` until they are promoted through an ADR and verified by repository-owned contract tests.

## Evidence Boundary

### Google 2026 Publications

| Source | Relevant evidence | Limitation |
|---|---|---|
| [How A2A Is Building a World of Collaborative Agents](https://developers.googleblog.com/how-a2a-is-building-a-world-of-collaborative-agents/) | Describes a secure "black box" handoff in which a provider retains its sensitive data, proprietary process, dependencies, and internal state while returning useful output | Vendor direction, not independent proof of a complete Hub architecture |
| [Build Cross-Language Multi-Agent Team with ADK and A2A](https://developers.googleblog.com/build-cross-language-multi-agent-team-with-google-agent-development-kit-and-a2a/) | Connects a Python Agent and a deterministic Go service through A2A while ADK retains local orchestration; neither side imports the other's implementation | Demonstrator, not a cross-company trust deployment |
| [Developer's Guide to AI Agent Protocols](https://developers.googleblog.com/developers-guide-to-ai-agent-protocols/) | Uses Agent Cards for runtime discovery and says a new remote Agent can be added by URL without per-Agent code changes or redeployment | Does not require every protocol discussed by the article |
| [Agentic Resource Discovery](https://developers.googleblog.com/announcing-the-agentic-resource-discovery-specification/) | Separates organization-owned catalogs, federated registry indexing, trust verification, and a subsequent direct connection over the resource's native protocol | ARD maturity and interoperability remain to verify |
| [Long-Running Agents with ADK](https://developers.googleblog.com/build-long-running-ai-agents-that-pause-resume-and-never-lose-context-with-adk/) | Keeps durable workflow state, checkpoints, pause/resume, and internal delegation inside the provider runtime | Provider checkpoints do not solve cross-system ambiguous delivery |

The product-specific notes and earlier synthesis are indexed in [`../research/vendor-sources-2026/README.md`](../research/vendor-sources-2026/README.md) and [`google-derived-a2a-platform-outline.md`](../research/vendor-sources-2026/google-derived-a2a-platform-outline.md).

### A2A Protocol

The selected protocol baseline is [`a2aproject/A2A@1736957`](../../submodules/a2a/),
the pinned A2A v1.0.0 source used by the current TCK profile. The newer
`16ba526` mainline candidate is tracked separately. The baseline defines A2A as
interoperability between independent, potentially opaque systems and explicitly
states that peers do not need access to one another's internal state, memory, or
tools.

The public interoperability surface is intentionally narrower than an Agent implementation:

- an Agent Card declares identity, provider, version, interfaces, capabilities, Skills, media types, security requirements, and optional signatures;
- Messages and Parts carry exchanged input and conversational context;
- a provider may return a direct Message or own a stateful Task;
- Tasks expose an authorized view of status, history, and Artifacts;
- polling, streaming, subscriptions, Push, cancellation, and errors describe observable interaction behavior;
- authenticated clients only receive Tasks and extended metadata they are authorized to access.

The protocol does not standardize or require disclosure of prompts, models, tools, internal Agent graphs, runtime checkpoints, private memory, or chain-of-thought.

Relevant pinned source entry points:

- [`docs/specification.md`](../../submodules/a2a/docs/specification.md)
- [`docs/topics/what-is-a2a.md`](../../submodules/a2a/docs/topics/what-is-a2a.md)
- [`docs/topics/key-concepts.md`](../../submodules/a2a/docs/topics/key-concepts.md)
- [`docs/topics/enterprise-ready.md`](../../submodules/a2a/docs/topics/enterprise-ready.md)
- [`docs/topics/agent-discovery.md`](../../submodules/a2a/docs/topics/agent-discovery.md)
- [`specification/a2a.proto`](../../submodules/a2a/specification/a2a.proto)

### Representative Open-Source Systems

The systems below were rechecked at their pinned submodule revisions. This review verified documentation and source behavior; it did not relaunch their complete deployment stacks.

| System | Pinned revision | Verified role | Boundary |
|---|---|---|---|
| [Solace Agent Mesh](../../submodules/solace-agent-mesh/) | `2b4ef6ab54e796bc77f12d5edb84dbb656e36610` | An external A2A proxy accepts URL, authentication, timeout, and refresh configuration; fetches Agent Cards; publishes discovery; forwards requests and cancellation; and transforms Artifacts | Strongest reviewed evidence for black-box Remote A2A Import; its mesh transport and product model are not project requirements |
| [Agent Stack](../../submodules/agent-stack/) | `79c786049d39684841d77fef9abfd8457a58b0bf` | Wraps LangGraph, CrewAI, or custom implementations as A2A-compatible services and can self-register an Agent Card in development mode | Provider-side L1 wrapper, not arbitrary remote Agent Card URL import |
| [Routa](../../submodules/routa/) | `e48861ab81e2b30378fd32f05204a3ab424c4fec` | A workflow step takes `agentCardUrl`, `skillId`, and `authConfigId`, sends A2A requests, stores external Task/Context identifiers, polls, and reconciles terminal state | Demonstrates a black-box workflow consumer, not a complete Registry or protocol implementation |

[ShrimpCrab](../../submodules/shrimpcrab/) was retained as a negative comparison. It is a runnable product-level multi-Agent platform, but its current import and execution paths center on platform manifests, workspaces, and known CLI runtimes. It is not evidence for arbitrary remote A2A Agent Card import.

## Architecture Conformance Review

| Current capability | Evidence alignment | Review result |
|---|---|---|
| Agent Card ingestion, validation, caching, and version snapshots | A2A discovery, Google protocol guide, Solace proxy | Keep |
| Direct URL, external Registry, and future ARD discovery adapters | A2A discovery strategies and Google ARD | Keep, but do not make the Hub the canonical owner of provider metadata |
| Identity, trust, tenant policy, credentials, and authorization | A2A security model and Google's secure-boundary rationale | Keep; cross-issuer delegation remains open |
| A2A Client/Server and managed Gateway | A2A Bindings, Solace proxy, and gateway research | Keep, but make central proxying policy-dependent |
| Message, Task, Artifact, Streaming, Push, cancellation, and subscription | A2A normative interaction surface | Keep |
| Durable Task engine | Provider runtime evidence conflicts with central ownership | Rename and narrow to Federation Task Reconciler |
| Artifact storage, provenance, scanning, and retention | A2A Artifact model and Solace Artifact bridge | Keep as policy-controlled platform behavior |
| Provider and consumer adapters | Google ADK example, Agent Stack, and Routa | Keep |
| AAMP | No reviewed Google or A2A requirement | Keep optional and separate as an asynchronous mailbox adapter |
| Marketplace, payment, reputation, and universal semantic routing | Not established as first-phase requirements | Defer |
| Exactly-once cross-system side effects | Not provided by A2A or the reviewed systems | Explicit non-goal |

## Revised Architecture Boundary

```text
Consumer domain system
ADK / LangGraph / custom runtime
          |
          v
Hub client adapter
          |
          v
Discovery and trust control plane
Card / catalog / registry / policy
          |
          v
Route decision
  |                         |
  | managed path            | verified direct path
  v                         |
Gateway / A2A data plane    |
  |                         |
  +-------------+-----------+
                v
       Provider A2A boundary
       Card / Message / Task / Artifact
                |
                v
       Opaque provider runtime
       prompts / tools / memory / workflow /
       models / subagents / checkpoints

Observable responses and delivery evidence
                |
                v
Federation Task Reconciler
external ID mapping / last observed state /
stream-push-poll convergence / cancel / recovery
```

The provider remains the authoritative owner of its Task execution. The Hub may retain a local federation operation, an external `taskId` and `contextId`, the last authorized state observed, delivery attempts, and reconciliation decisions. It must not claim that this state is the provider's private checkpoint.

## Opaque Agent Integration Contract

### Minimum Native A2A Inputs

A native remote Agent onboarding request may require:

- Agent Card URL or an authorized catalog/Registry reference;
- a credential reference, never an embedded long-lived secret;
- tenant, network, data-classification, quota, and admission policy configuration;
- an optional expected provider identity, Card digest, signature, or version constraint;
- optional routing preferences such as mandatory Gateway traversal.

Onboarding then performs:

```text
fetch Card
  -> validate schema, interface, version, security, and extensions
  -> verify source and policy
  -> retain an immutable snapshot and digest
  -> index declared Skills and media types
  -> verify reachability and authentication separately
  -> assign admission and routing state
  -> refresh, disable, revoke, or roll back without Hub source changes
```

### Information the Hub May Observe

- public and authorized extended Agent Card fields;
- selected protocol version, Binding, endpoint, and extension declarations;
- exchanged A2A Messages and Parts when the selected data path permits it;
- authorized Task status, visible history, and external identifiers;
- returned Artifacts and their protocol metadata;
- stream, callback, polling, cancellation, error, and timing evidence;
- policy decisions, correlation identifiers, and operational telemetry.

### Information the Hub Must Not Require

- source code, implementation language, or internal framework;
- system prompts, hidden reasoning, or chain-of-thought;
- model provider, model selection, or private evaluation strategy;
- internal tools, MCP servers, tool credentials, or direct resource access;
- private memory, internal conversation state, or domain databases;
- workflow graphs, internal Task decomposition, subagent topology, or checkpoints;
- proprietary business logic or data not returned through the authorized contract.

Security admission may require attestations, provenance, deployment constraints, or independently produced evaluation results. These are boundary evidence, not permission for the Hub to take ownership of private execution internals.

### What Opaque Does Not Mean

- It does not mean zero configuration: URL, credentials, trust, network, tenancy, and policy still exist.
- It does not mean zero contract: the Agent must conform to the selected A2A version and Binding Profile.
- It does not mean zero observability: boundary events, errors, latency, state, and authorized content remain observable.
- It does not guarantee content confidentiality from a terminating Gateway. Direct connections or an additional encryption design are required where intermediaries must not see Message or Artifact content.
- It does not imply business correctness: protocol conformance, reachability, authorization, output quality, and production readiness are separate gates.

## Integration Modes

### Remote A2A Import

For an already conformant external server, the Hub consumes its public contract without modifying the provider:

```text
Agent Card URL + credential reference
  -> admission
  -> discoverable and callable logical Agent
```

This is the primary cross-organization path and the strongest test of opaque integration.

### Provider-Side Adapter

For a legacy LangGraph, CrewAI, deterministic service, or custom runtime, a reusable adapter is deployed in the provider's trust boundary:

```text
private implementation
  -> provider-owned A2A adapter
  -> standard Agent Card and A2A endpoint
  -> Hub Remote Import
```

The Hub still treats the resulting endpoint as opaque. Framework-specific code does not enter the federation core.

### Platform-Managed Hosting

An organization may voluntarily deploy an Agent through a Hub-associated runtime or SDK. This can expose additional operational configuration to the hosting layer, but it must not change the generic federation contract seen by other participants.

## First-Phase Functional Baseline

The reviewed evidence supports this minimum product slice:

1. Pin one A2A v1 revision and an initial JSON-RPC plus SSE Binding Profile.
2. Import a remote Agent from an Agent Card URL and credential reference without Hub code changes.
3. Validate and snapshot Agent Cards, index Skills, and separate declaration from verified reachability.
4. Support direct Message and provider-owned Task responses, including `INPUT_REQUIRED`.
5. Preserve text, file, URL, structured-data, and streamed Artifact semantics.
6. Map local federation operations to external Task and Context identifiers.
7. Reconcile polling, streaming, later Push delivery, cancellation, disconnect, and provider restart behavior.
8. Apply identity, tenant, policy, timeout, size, content, and audit controls at the boundary.
9. Test an independently deployed Go/Python pair and a local orchestrator that sees the remote endpoint only as an A2A Agent.
10. Validate observable behavior through repository-owned contracts, ITK, Inspector, and a protocol-aligned TCK.

## Acceptance Criteria for the Black-Box Invariant

The first implementation should not claim opaque plug-and-play integration until all of the following are demonstrated:

- a conformant Agent can be added by configuration or API without recompiling or redeploying the Hub core;
- the Agent runs in a separate process and may use a different language and framework;
- the Hub never reads its source, prompts, tools, memory, workflow, or checkpoint store;
- the Hub discovers only declared and authorized capabilities;
- Message-or-Task branching, Artifact delivery, `INPUT_REQUIRED`, errors, streaming, cancellation, and resubscription work through the public contract;
- provider restart recovery uses externally observable A2A state rather than provider-private checkpoints;
- changing the provider implementation without changing its compatible public contract does not require a Hub change;
- disabling or revoking the Agent prevents new routing without modifying the provider implementation.

## Open Questions

- Which calls must traverse the managed Gateway, and which policies permit verified direct connections?
- How should ARD, direct Agent Card URLs, and external registries map to one logical identity without creating a proprietary canonical registry?
- What token represents attenuated user delegation across organizations and multiple Agent hops?
- Which Message and Artifact content may be retained, inspected, scanned, or encrypted by tenant policy?
- What minimum local state is required for ambiguous delivery and cancellation reconciliation without mirroring provider workflow state?
- Which Card changes require automatic refresh, manual re-admission, rollback, or immediate revocation?

## Related Documents

- [`google-derived-a2a-platform-outline.md`](../research/vendor-sources-2026/google-derived-a2a-platform-outline.md)
- [`plug-and-play-federation.md`](../research/a2a-study/plug-and-play-federation.md)
- [`agent-card.md`](../research/a2a-study/agent-card.md)
- [`message-task-artifact.md`](../research/a2a-study/message-task-artifact.md)
- [`task-delivery-and-recovery.md`](../research/a2a-study/task-delivery-and-recovery.md)
- [`a2a-v1-go-python-decision-gate.md`](a2a-v1-go-python-decision-gate.md)
