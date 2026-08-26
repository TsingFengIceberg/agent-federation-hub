# Anthropic: Patterns and Problems in Emerging Multiagent Systems

> **Source**: [Anthropic Research](https://www.anthropic.com/research/multiagent-systems)<br>
> **Published**: 2026-08-13<br>
> **Evidence status**: `verified` official vendor research; generalization beyond the reported experiments remains `to-verify`

## Relevant Content

Anthropic distinguishes two interaction shapes:

- Agents can cooperate efficiently when another Agent is treated like a tool invocation with defined inputs and outputs.
- Coordination becomes harder when Agents are distinct, long-lived peers with their own goals and behavior and no clear hierarchy.

The research examines shared codebases, markets, and other social systems. It reports coordination failures, correlated behavior, resource contention, and collusion risks, and notes that real ecosystems will contain Agents with different contexts and models rather than only Claude instances.

## What This Supports

The distinction maps closely to internal subagent orchestration versus open federation among independent peers. It also supports treating multi-Agent interaction as a governance and systemic-risk problem, not only a routing problem.

## What This Does Not Prove

- The experiments do not evaluate A2A or a Federation Hub.
- A central forum is discussed as one possible coordination mechanism, not validated as a general solution.

## Project Implications

- Include untrusted peers, conflicting incentives, rate contention, and correlated failures in evaluation scenarios.
- Do not assume a central orchestrator can impose a shared hierarchy on independent organizations.
- Add policy, quotas, audit, and dispute evidence to marketplace scenarios.
