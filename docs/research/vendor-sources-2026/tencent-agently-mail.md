# Tencent: Agently Mail

> **Source**: [Tencent AgentlyMail README at `ba86f10`](https://github.com/Tencent/AgentlyMail/blob/ba86f10be14ab2a51e3e23bd425c03780a83512a/README.md)<br>
> **First published**: 2026-06-01 in [`9badd32`](https://github.com/Tencent/AgentlyMail/commit/9badd32b41c92f4340b141cda8f94fa796daefa9)<br>
> **Evidence status**: `verified` official vendor repository; service behavior and protocol details remain `to-verify`

## Relevant Content

Tencent's QQ Mail team describes a mailbox service made specifically for Agents and isolated from a user's personal mailbox. The published skill covers OAuth login, mailbox identity and aliases, listing and searching messages, reading content, sending, replying, forwarding, deletion, and attachment upload and download. Sending is documented with a two-stage confirmation control.

The interface is delivered as a skill and CLI rather than as an Agent-to-Agent task protocol. The mailbox provides a durable asynchronous channel that existing Agents can use without sharing one orchestration runtime.

## What This Supports

The source supports asynchronous mailbox integration as a real product category for Agents. It reinforces the value of keeping email-like delivery, attachments, user authorization, and confirmation semantics in an adapter rather than forcing them into the synchronous A2A path.

## What This Does Not Prove

- Agently Mail does not claim A2A or AAMP compatibility.
- A mailbox for an Agent is not automatically a federated Agent identity, capability registry, Task state machine, or trust protocol.
- The README does not specify delivery guarantees, threading identity, idempotency, retention, push behavior, or cross-organization policy.

## Project Implications

- Keep AAMP/email support as an asynchronous adapter over the internal task and artifact model.
- Add mailbox threading, attachment provenance, confirmation, deduplication, and delayed-delivery questions to the AAMP evaluation.
- Do not infer a remote Agent's capabilities or authorization merely from possession of an email address.
