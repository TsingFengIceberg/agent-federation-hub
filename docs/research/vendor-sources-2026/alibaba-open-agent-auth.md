# Alibaba: Open Agent Auth

> **Source**: [Alibaba Open Agent Auth README at `d75da12`](https://github.com/alibaba/open-agent-auth/blob/d75da121a66f8b2ae5be009a98e050fd1dc4c1e6/README.md)<br>
> **First published**: 2026-02-14 in [`39a45b9`](https://github.com/alibaba/open-agent-auth/commit/39a45b93b22a7ec7ad07d7e4614fd7161956e8aa)<br>
> **Evidence status**: `verified` official vendor public-beta implementation; Agent-to-Agent authorization is `planned`, not implemented evidence

## Relevant Content

Open Agent Auth implements an enterprise authorization flow based on the Agent Operation Authorization Internet-Draft and existing OAuth 2.0, OpenID Connect, WIMSE, W3C Verifiable Credentials, OPA, and MCP components. Its current architecture binds user identity, a request-level workload identity, and an operation authorization token, while keeping temporary keys scoped to a virtual workload and recording semantic audit evidence.

The implementation focuses on a user-Agent-resource flow and includes an MCP adapter. Its roadmap lists Agent-to-Agent authorization, chained delegation, cross-Agent identity verification, authorization-server discovery, and routing as future work.

## What This Supports

The project provides implementation evidence that Agent authorization needs more than inbound API authentication: user consent, workload identity, narrow operation policy, token binding, resource-server enforcement, and auditable evidence are distinct parts of the flow.

## What This Does Not Prove

- The repository labels itself public beta and says mission-critical production use should wait for 1.0.
- Its README does not show completed Agent-to-Agent authorization; that capability is an unchecked roadmap item.
- MCP integration does not establish A2A integration or cross-company federation.
- A single trust domain and Authorization Server do not solve federation among unrelated identity providers by themselves.

## Project Implications

- Evaluate its token and evidence model as one candidate input, not as the Hub's selected authorization architecture.
- Separate implemented user-to-resource authorization from future Agent-to-Agent delegation claims.
- Define how organization-local policy and resource-server enforcement interact with Hub policy without requiring one global policy engine.
- Add maturity, revocation, replay, token audience, key rotation, and multi-issuer trust tests before any integration decision.
