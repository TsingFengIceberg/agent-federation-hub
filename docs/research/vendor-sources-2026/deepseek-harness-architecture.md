# DeepSeek: Harness Architecture

> **Source**: [DeepSeek Harness architecture at `70a3bf4`](https://github.com/deepseek-ai/deepseek-harness/blob/70a3bf45542252c6aee96dedd242d0a5458d14a4/docs/architecture.md)<br>
> **Architecture document first added**: 2026-07-30; **reviewed version**: 2026-08-19<br>
> **Evidence status**: `verified` official vendor developer-preview architecture; experimental Agent Teams behavior remains `to-verify`

## Relevant Content

DeepSeek Harness is a plugin-composed Agent runtime. Model adapters, tools, persistence, sandbox and approval policy, credentials, telemetry, the Agent loop, and session handling are replaceable providers in a shared context. An append-only session event log is the durable source for model history, replay, fork, resume, transcripts, and telemetry.

The runtime exposes live Agent and subagent interfaces behind local capability seams. Its architecture describes Agent Teams as a private, opt-in experimental seam with a durable roster, task board, and mailbox layered over continuable subagents.

## What This Supports

This source supports a modular internal runtime boundary: Agent loops, tool execution, checkpoints, local subagents, approvals, and durable event replay can evolve independently behind stable interfaces. It also shows that mailbox and team abstractions can exist inside one runtime without becoming public interoperability protocols.

## What This Does Not Prove

- DeepSeek Harness is in developer preview and warns of compatibility-breaking changes.
- Its live Agent registry is an in-process runtime service, not a cross-organization capability registry.
- Experimental Agent Teams are private and do not define network discovery, A2A wire compatibility, federated identity, or trust negotiation.
- A replaceable plugin provider does not make separately operated remote Agents mutually interoperable.

## Project Implications

- Treat DeepSeek Harness, LangGraph, ADK, AgentScope, and similar systems as possible provider-side runtimes.
- Avoid duplicating their local Agent loop, tool pipeline, session log, or subagent orchestration in the Hub.
- Define an adapter boundary that exposes a domain system as an A2A Agent while leaving its internal plugin and event model private.
- Include a provider restart and resume scenario where the provider runtime owns its checkpoint and the Hub only reconciles observable A2A Task state.
