# Anthropic: Trustworthy Agents in Practice

> **Source**: [Anthropic Research](https://www.anthropic.com/research/trustworthy-agents)<br>
> **Published**: 2026-04-09<br>
> **Evidence status**: `verified` official vendor publication; project implications are `draft`

## Relevant Content

Anthropic argues that Agent safety and reliability cannot be achieved by one company acting alone. It calls for shared work by industry, standards bodies, and governments, including common benchmarks, evidence sharing, and open standards.

Anthropic uses MCP as its example of an open protocol. Its rationale is that security properties can be designed once into shared infrastructure rather than patched independently into each deployment, and that competition should focus on Agent quality and safety rather than control of integrations.

## What This Supports

The article supports open, community-governed protocol surfaces and shared security infrastructure across an ecosystem.

## What This Does Not Prove

- Anthropic discusses MCP, not A2A, and does not endorse this project's protocol choice.
- An open protocol does not remove the need for implementation-specific controls or product security work.

## Project Implications

- Avoid proprietary integration lock-in in the generic core.
- Treat conformance, common security tests, and portable evidence as product requirements to validate.
- Keep protocol support modular enough to evolve with community standards.
