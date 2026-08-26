# Anthropic: Scaling Managed Agents

> **Source**: [Anthropic Engineering](https://www.anthropic.com/engineering/managed-agents)<br>
> **Published**: 2026-04-08<br>
> **Evidence status**: `verified` official vendor engineering publication; project implications are `draft`

## Relevant Content

Anthropic describes a hosted long-horizon Agent architecture that separates:

- the model and harness as the "brain";
- sandboxes and tools as the "hands";
- an append-only event log as the durable session.

The interfaces are intended to survive changes in the underlying harness or execution environment. Failed harnesses can recover from the external session log. Credentials remain outside untrusted sandboxes, and MCP calls pass through a credential proxy backed by a vault.

## What This Supports

The article supports durable event state outside the model context window, replaceable execution components, explicit credential boundaries, and recovery through stable interfaces.

## What This Does Not Prove

- This is a single-provider hosted architecture, not cross-company federation.
- Its internal interfaces are not proposed as interoperability standards.

## Project Implications

- Keep Hub task evidence independent of model context and provider harness state.
- Store delegated credentials outside execution sandboxes.
- Design adapters around stable task, event, and artifact contracts rather than runtime internals.
