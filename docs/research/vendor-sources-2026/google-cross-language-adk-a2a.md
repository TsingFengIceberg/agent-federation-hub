# Google: Cross-Language Multi-Agent Teams with ADK and A2A

> **Source**: [Google Developers Blog](https://developers.googleblog.com/build-cross-language-multi-agent-team-with-google-agent-development-kit-and-a2a/)<br>
> **Published**: 2026-06-22<br>
> **Evidence status**: `verified` official vendor publication; project implications are `draft`

## Relevant Content

The worked example connects a Python extraction Agent and a Go compliance Agent. The services are connected through A2A and the overall pipeline is orchestrated with Google's Agent Development Kit (ADK). Google frames different teams, languages, and deployment targets as a normal production constraint.

The article assigns separate responsibilities:

- A2A provides discovery through Agent Cards, protocol communication, and a task lifecycle for synchronous and asynchronous work.
- ADK provides the local pipeline and represents a remote A2A service as a subagent abstraction.
- Neither Agent imports or executes the other Agent's implementation language or packages.

## What This Supports

This is the clearest official example that an in-domain orchestration runtime and a cross-Agent interoperability protocol are complementary rather than competing choices.

## What This Does Not Prove

- The example uses two services in one demonstrator and does not by itself validate cross-company trust or tenancy.
- Some object and endpoint examples must be checked against the selected current A2A version before implementation.

## Project Implications

- The minimum testbed should use two implementations or languages and retain a local orchestrator on at least one side.
- The Hub must not require a shared runtime.
- Current protocol version and binding details must come from the selected A2A specification, not copied from this tutorial.
