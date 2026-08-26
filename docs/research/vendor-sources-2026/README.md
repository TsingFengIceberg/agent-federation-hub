# 2026 Vendor Source Notes

> **Scope**: official vendor publications first published in 2026<br>
> **Checked**: 2026-08-26<br>
> **Evidence status**: source metadata and cited statements verified against the linked official pages; project implications remain `draft`

This directory records product-specific research inputs for Agent Federation Hub. It is separate from the pinned general A2A research snapshot in [`../a2a-study/`](../a2a-study/). A vendor publication is evidence of that vendor's architecture, product direction, or engineering experience; it is not independent proof that the proposed Hub architecture or a particular protocol is correct.

The inclusion rule is strict: the page itself must identify 2026 as its original publication year. Sitemap modification dates do not qualify. For example, Google's A2A Extensions article and Anthropic's original MCP announcement were excluded because their pages identify them as 2025 and 2024 publications respectively.

## Source Notes

### Google

- [`google-a2a-collaborative-agents.md`](google-a2a-collaborative-agents.md): secure black-box delegation and independently managed Agent workloads.
- [`google-cross-language-adk-a2a.md`](google-cross-language-adk-a2a.md): explicit coexistence of A2A interoperability and ADK orchestration.
- [`google-agent-protocols-guide.md`](google-agent-protocols-guide.md): protocol responsibility boundaries across MCP, A2A, commerce, payment, and UI.
- [`google-agentic-resource-discovery.md`](google-agentic-resource-discovery.md): cross-organization discovery, trust metadata, and federated registries.
- [`google-long-running-adk-agents.md`](google-long-running-adk-agents.md): durable in-domain runtime state, pause/resume, and event-driven execution.

### Synthesis Notes

- [`google-derived-a2a-platform-outline.md`](google-derived-a2a-platform-outline.md): this project's evidence-bounded synthesis of the five Google sources into draft federation boundaries, capabilities, non-goals, and validation steps; it is not a Google reference architecture.

### Anthropic

- [`anthropic-trustworthy-agents.md`](anthropic-trustworthy-agents.md): shared infrastructure, open standards, and ecosystem-level security.
- [`anthropic-emerging-multiagent-systems.md`](anthropic-emerging-multiagent-systems.md): tool-like subagents versus long-lived peers without a clear hierarchy.
- [`anthropic-managed-agents.md`](anthropic-managed-agents.md): stable interfaces around harnesses, sandboxes, credentials, and durable sessions.

### OpenAI

- [`openai-private-mcp-tunnel.md`](openai-private-mcp-tunnel.md): preserving customer network and identity boundaries while using a standard protocol path.
- [`openai-agents-sdk-skills.md`](openai-agents-sdk-skills.md): an in-application multi-agent runtime and its repository-level operating controls.
- [`openai-governed-agents-cookbook.md`](openai-governed-agents-cookbook.md): policy-as-code, guardrails, tracing, and audit; this is partner-authored Cookbook content.

### ByteDance

- [`bytedance-agentkit-hybrid-cloud-a2a.md`](bytedance-agentkit-hybrid-cloud-a2a.md): an official cross-Runtime A2A sample covering discovery, capability validation, authentication, Artifacts, and correlated traces.

### Tencent

- [`tencent-agently-mail.md`](tencent-agently-mail.md): an Agent-specific asynchronous mailbox with OAuth, attachments, and confirmation controls; it is not an A2A or AAMP claim.
- [`tencent-loopforge.md`](tencent-loopforge.md): durable, resumable internal multi-Agent workflow orchestration for software delivery.

### Alibaba

- [`alibaba-agent-operation-authorization-draft.md`](alibaba-agent-operation-authorization-draft.md): a 2026 IETF Internet-Draft for fine-grained user-to-Agent authorization and attenuated Agent-to-Agent delegation.
- [`alibaba-open-agent-auth.md`](alibaba-open-agent-auth.md): a public-beta implementation of operation authorization; Agent-to-Agent authorization remains roadmap work.
- [`alibaba-nacos-agent-registry-rad.md`](alibaba-nacos-agent-registry-rad.md): a protocol-neutral Agent registry, A2A binding, and discovery control plane that explicitly excludes Task proxying.
- [`alibaba-agentscope-business-travel.md`](alibaba-agentscope-business-travel.md): an internal multi-Agent business workflow and a useful boundary contrast with federation.

### DeepSeek

- [`deepseek-harness-architecture.md`](deepseek-harness-architecture.md): a plugin-based internal Agent runtime with durable sessions and private experimental Agent Teams.

## Current Cross-Source Reading

The strongest directly observed layering is:

```text
In-domain runtime (ADK, Agents SDK, LangGraph, custom)
  -> workflow, subagents, durable execution, local tools, checkpoints

Inter-agent protocol (A2A)
  -> framework-neutral discovery metadata, messages, tasks, artifacts

Federation and governance infrastructure
  -> cross-organization discovery, trust, identity, policy, audit, routing
```

Only Google explicitly endorses A2A in these sources. Anthropic supports open protocols and shared security infrastructure but discusses MCP rather than A2A. The reviewed OpenAI 2026 developer publications contain no direct A2A endorsement.

Among the reviewed Chinese vendors, ByteDance provides the most direct 2026 A2A execution example, while Alibaba provides the strongest registry/discovery and delegated-authorization specifications. Tencent's Agently Mail is relevant to an asynchronous mailbox adapter, but it does not claim AAMP compatibility. DeepSeek Harness, Tencent LoopForge, and Alibaba AgentScope are evidence for retaining capable in-domain runtimes, not substitutes for a cross-organization protocol.

The Alibaba-authored Agent Operation Authorization draft includes Agent-to-Agent delegation, but Alibaba Open Agent Auth still lists that implementation as future work. The distinction between a published draft, an experimental specification, a sample, and verified runtime interoperability must remain visible in later architecture decisions.

Google's Agentic Resource Discovery proposal is a new item to evaluate before assigning canonical discovery ownership to the Hub. Its existence supports the need for cross-organization discovery while also arguing against inventing an isolated proprietary registry.

## Checked But Not Included As Direct Federation Evidence

- ByteDance Seed's 2026 research and blog index contains model, multimodal, simulation, and environment-learning publications, but no directly relevant A2A or federation article was found in the reviewed official index.
- Tencent TeamAI and BrowserSkill address shared harness configuration and tool access; they do not define remote Agent interoperability. LoopForge is retained only as an explicit internal-orchestration contrast.
- Alibaba ANOLISA addresses Agent execution, security, observability, and recovery at the operating-system/runtime layer; it does not define cross-organization Agent communication.
- DeepSeek's 2026 API announcements and integration index focus on models and harness integrations. DeepSeek Harness is retained as an internal-runtime boundary source; no 2026 DeepSeek A2A endorsement was found.
