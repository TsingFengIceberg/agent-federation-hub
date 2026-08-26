# OpenAI: Skills for Agents SDK Maintenance

> **Source**: [OpenAI Developers](https://developers.openai.com/blog/skills-agents-sdk/)<br>
> **Published**: 2026-03-09<br>
> **Evidence status**: `verified` official vendor engineering publication; project implications are `draft`

## Relevant Content

OpenAI describes its Agents SDK as an application framework for multiple Agents, tools, and human-in-the-loop controls. The article focuses on maintaining the Python and TypeScript SDK repositories through repository-local skills, `AGENTS.md`, automated verification, compatibility rules, and integration tests.

This is useful primarily as a boundary example: the SDK supplies in-application Agent construction and handoff capabilities, while repository policy governs development and compatibility of that runtime.

## What This Supports

The article supports the continued role of an internal Agent SDK and demonstrates that compatibility and repeatable verification need explicit controls even inside one project.

## What This Does Not Prove

- It does not discuss cross-organization Agent federation or A2A.
- Repository skills and Agent runtime protocols solve different problems.

## Project Implications

- Do not position the Hub as a replacement for Agents SDK, LangGraph, or ADK.
- Apply explicit compatibility tests and release rules to future protocol adapters.
- Keep provider runtimes replaceable behind adapter contracts.
