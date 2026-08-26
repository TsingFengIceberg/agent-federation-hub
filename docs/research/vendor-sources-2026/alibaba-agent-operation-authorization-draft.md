# Alibaba and Cisco: Agent Operation Authorization Internet-Draft

> **Source**: [IETF Datatracker](https://datatracker.ietf.org/doc/draft-liu-agent-operation-authorization/) and [version 01 text](https://www.ietf.org/archive/id/draft-liu-agent-operation-authorization-01.txt)<br>
> **Published**: 2026-02-27<br>
> **Evidence status**: `verified` 2026 Internet-Draft authored by Dapeng Liu and Hongru Zhu of Alibaba and Suresh Krishnan of Cisco; maturity and adoption are `to-verify`

## Relevant Content

The draft proposes fine-grained authorization for an Agent acting on behalf of a human principal. An Agent turns a proposed operation into a signed JWT, submits it through OAuth 2.0 Pushed Authorization Requests, obtains explicit user confirmation, and receives an Agent Operation Authorization Token that a resource server can enforce and audit.

Section 6 explicitly covers Agent-to-Agent delegation. A primary Agent may delegate only a narrower subset of its authority to another Agent. The Authorization Server validates each hop, authenticates the receiving Agent's workload identity, issues a fresh token, and extends a signed `delegation_chain` while preserving linkage to the original human principal.

## What This Supports

The draft directly supports treating delegated user intent, Agent workload identity, authority attenuation, evidence, and audit chains as cross-Agent concerns. Those concerns are absent from ordinary local workflow routing even when a runtime can call a subagent.

## What This Does Not Prove

- This is an Internet-Draft and explicitly remains work in progress; it is not an RFC or a demonstrated interoperability profile.
- It does not define Agent discovery, A2A Messages, Tasks, Artifacts, streaming, or transport bindings.
- Its flow assumes an Authorization Server capable of validating each delegation hop; cross-organization trust between different authorization domains is not fully resolved.
- Example policies and JWT claims require security review and implementation validation before adoption.

## Project Implications

- Add the draft to the delegated-authorization comparison alongside A2A authentication material and emerging workload-identity standards.
- Model authority as attenuated and traceable across delegation hops rather than forwarding an original bearer token unchanged.
- Keep authorization evidence linked to, but separate from, A2A Task and Message payloads.
- Test delegation across two trust domains; a shared Authorization Server should be one profile, not an unstated universal assumption.
