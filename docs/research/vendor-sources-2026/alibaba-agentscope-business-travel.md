# Alibaba AgentScope: Multi-Agent Business-Travel Assistant

> **Source**: [AgentScope Blog](https://agentscope.io/blog/alibaba-business-travel/)<br>
> **Published**: 2026-03-15<br>
> **Evidence status**: `verified` official vendor-hosted user story; reported business results have not been independently reproduced

## Relevant Content

Alibaba's AliGo team reports replacing an increasingly large single prompt with more than ten cooperating Agents for intent recognition, travel planning, information retrieval, and enterprise knowledge. The implementation uses AgentScope for the Python Agent layer and Java for authentication, service integration, and MCP services.

Each Agent normally owns its own conversation history. Context is shared selectively according to business relationships and minimum-necessary access. The system adds chain-level observability and automated evaluation because failures can occur in the planner, a specialist Agent, an LLM call, a tool call, or retrieval. The article reports increasing item-collection accuracy from about 50 percent to over 90 percent, while also acknowledging increased multi-Agent latency.

## What This Supports

The article is strong evidence for in-domain multi-Agent orchestration when one organization owns the workflow, business decomposition, context policy, and evaluation loop. It also shows why observability and context isolation are local runtime responsibilities rather than consequences of adopting an inter-Agent wire protocol.

## What This Does Not Prove

- The system does not claim cross-company Agent federation or A2A interoperability.
- The reported accuracy change is a vendor user story, not an independent controlled comparison of AgentScope and LangGraph.
- Framework selection for AliGo does not establish AgentScope as the runtime choice for this project.
- Selective context sharing inside one application is not a substitute for cross-organization identity, consent, and trust.

## Project Implications

- Use this case as a positive example of what remains inside a domain runtime: planning, specialist routing, context sharing, local memory, evaluation, and UI treatment of latency.
- Define the Hub boundary so an AliGo-like system can appear as one federated Agent without exposing its ten-plus internal Agents.
- Compare federation overhead separately from the latency and quality effects of internal multi-Agent decomposition.
