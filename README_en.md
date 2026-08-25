<h1 align="center">Agent Federation Hub</h1>

<p align="center"><a href="./README_en.md">English</a> | <a href="./README.md">中文</a></p>

Agent Federation Hub is a research-oriented open-source project about cross-domain and cross-organization collaboration between AI agents. It does not try to orchestrate every agent inside one business. Instead, it explores how independently deployed agent systems, built with different frameworks and governed by different trust boundaries, can discover one another and collaborate through protocols that are observable, authenticated, and recoverable.

> Current status: the A2A protocol and open-source ecosystem research baseline has been imported in full. Platform implementation, A2A Inspector/TCK conformance validation, and concrete technology choices remain pending. Imported research findings are not implemented product capabilities.

## Direction

```text
A2A Protocol
  -> Registry / Discovery
  -> Gateway / Data Plane
  -> Agent Runtime / Domain Workflow
  -> Event and Async Adapters
  -> Governance, Evaluation, and Product Surface
```

The initial boundary is to use A2A as the primary cross-agent protocol and AAMP as a mailbox-style asynchronous adapter, while LangGraph and similar frameworks handle multi-agent orchestration inside a domain. Nacos, agentgateway, Agent Stack, Solace Agent Mesh, ShrimpCrab, and other projects are research references for different layers, not a predetermined stack to assemble blindly.

## Start Here

1. Read `.handoff/current.md` locally first: context, goals, open questions, and next steps.
2. Read [`docs/research/a2a-study/README.md`](docs/research/a2a-study/README.md), the entry point for the imported A2A protocol and cross-project research.
3. Then read the relevant architecture decisions in `.handoff/decisions/`. The local `.handoff/` material tracks continuity and does not replace formal documentation.

## Research Foundation

| Entry | Purpose |
|---|---|
| [`docs/README.md`](docs/README.md) | Formal documentation index and ownership boundaries |
| [`docs/research/a2a-study/`](docs/research/a2a-study/) | Complete A2A research snapshot imported from a pinned `agent-systems-study` commit |
| [`submodules/`](submodules/) | Pinned source revisions for A2A, AAMP, registry, gateway, runtime, and sample projects |

General A2A protocol and cross-project research remains canonical in `agent-systems-study`. This repository keeps a traceable full snapshot synchronized one way from a recorded source commit; product architecture, ADRs, specifications, implementation, and tests evolve only here.

## Planned Validation Scenarios

Werewolf validates private information, turn-based state, and adversarial collaboration. Software development, procurement, research, incident response, content production, personal assistance, IoT, AIOps, and agent marketplaces validate orthogonal requirements such as long-running tasks, approval, asynchronous events, multimodal artifacts, identity, billing, and untrusted agents. These scenarios test generality; they are not fixed product verticals.

## Project Phases

- **Phase 0: Protocol baseline and conformance**: the research notes are imported; next, select the initial A2A version and binding profile, then validate the implementation model with Inspector/TCK and minimal contract tests.
- **Phase 1: Minimal interoperability**: an A2A client, server, registry, and observable task flow.
- **Phase 2: Async and governance**: an AAMP adapter, delegated identity, tenancy, audit, retry, and human approval.
- **Phase 3: Scenario validation**: validate reuse of the same core across orthogonal scenarios.

## License and Implementation Commitment

The license, technology choices, and production deployment plan are not finalized. Implementation should be driven by protocol evidence and repeatable interoperability tests rather than promoting one demo's API into a platform contract.
