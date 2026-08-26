# Google: Long-Running Agents with ADK

> **Source**: [Google Developers Blog](https://developers.googleblog.com/build-long-running-ai-agents-that-pause-resume-and-never-lose-context-with-adk/)<br>
> **Published**: 2026-05-12<br>
> **Evidence status**: `verified` official vendor publication; project implications are `draft`

## Relevant Content

Google argues that real enterprise work spans days, approvals, vendor replies, and cross-team handoffs. The article separates durable workflow state from chat history and demonstrates:

- an explicit durable state machine;
- persistent checkpoint and resume across process restarts;
- event-driven dormancy rather than polling or blocked threads;
- human approval gates;
- delegation to focused subagents inside ADK.

## What This Supports

The article supports retaining an in-domain runtime for workflow execution and demonstrates why a cross-Agent protocol alone is not a durable workflow engine.

## What This Does Not Prove

- ADK is not established as the runtime choice for this project.
- Local checkpoint semantics do not automatically solve cross-system ambiguous delivery or recovery.

## Project Implications

- Distinguish Hub reconciliation state from provider runtime checkpoints.
- Define which party owns pause/resume and how that state is reported over A2A.
- Test provider restart, delayed external input, and cancellation races separately.
