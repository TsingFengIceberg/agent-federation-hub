# Google: Developer's Guide to AI Agent Protocols

> **Source**: [Google Developers Blog](https://developers.googleblog.com/developers-guide-to-ai-agent-protocols/)<br>
> **Published**: 2026-03-18<br>
> **Evidence status**: `verified` official vendor publication; project implications are `draft`

## Relevant Content

Google presents six protocols as solving different integration problems:

- MCP connects Agents to tools and data.
- A2A connects an Agent to remote Agents implemented by different teams, frameworks, and servers.
- UCP standardizes commerce operations.
- AP2 represents payment authorization.
- A2UI describes Agent-generated interfaces.
- AG-UI standardizes event delivery from an Agent to a frontend.

The A2A section argues that per-Agent glue code creates a maintenance and redeployment burden. Agent Cards allow runtime capability inspection, while the protocol carries messages and Task or Message results.

## What This Supports

The guide supports protocol layering and argues against stretching one protocol or one orchestration framework across tools, Agent peers, payments, and user interfaces.

## What This Does Not Prove

- The six-protocol stack is not a requirement for this project.
- Vendor examples are not a substitute for checking official protocol specifications and compatibility tests.

## Project Implications

- Keep the A2A adapter separate from tool access, domain commerce, payment, and UI adapters.
- Add only protocols required by a validated scenario.
- Treat the protocol guide as a responsibility map, not a committed platform stack.
