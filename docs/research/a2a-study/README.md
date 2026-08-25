# A2A 研究入口

> **导入来源**: [TsingFengIceberg/agent-systems-study](https://github.com/TsingFengIceberg/agent-systems-study) @ `98b4cbbba4877a8f40c52c5595f97a78bfaf1a07`<br>
> **导入日期**: 2026-08-26 | **性质**: 完整研究快照 | **状态**: draft

本目录从 `agent-systems-study/DOCS/a2a/` 全量导入。通用 A2A 协议学习和跨项目研究以来源仓库为权威版本；本仓库将其作为产品设计输入。协议事实需要修正时应先回写来源仓库，再按明确来源 commit 重新同步；Agent Federation Hub 自身的需求、ADR、实现和测试在本仓库其他正式文档中维护。

本目录用于研究 A2A（Agent2Agent）相关的协议、Registry、Gateway、Agent Runtime、任务生命周期、身份治理和生产部署。目标不是把 A2A 等同于某一个多 Agent 框架，而是理解如何让不同公司、语言、框架和部署环境中的 Agent 互相发现、建立信任、协作完成任务。

## 研究边界

```text
A2A Protocol
  -> AgentCard / Skill / Message / Task / Artifact / Streaming
Registry & Discovery
  -> Agent identity / version / endpoint / health / capability search
Gateway & Federation
  -> routing / auth delegation / policy / retry / rate limit / protocol adapter
Runtime & Eventing
  -> durable task / async execution / HITL / push / artifact / event bus
Governance
  -> tenant / RBAC / audit / trace / cost / security / conformance
```

重点区分以下项目类型：

- **标准协议与 SDK**：定义互操作消息和 AgentCard 语义；
- **Registry / Discovery**：保存 Agent 定义、版本、能力和运行时地址；
- **Gateway / Data Plane**：转发请求、执行认证、策略、限流和观测；
- **Agent Runtime / Platform**：运行 Agent、保存任务、处理事件和部署；
- **多 Agent 框架或 Demo**：解决单个垂直领域内的编排，不自动等于 A2A 联邦平台。

## 后续文档

| 文档 | 内容 | 状态 |
|---|---|---|
| [`protocol-and-interop.md`](protocol-and-interop.md) | A2A 原理、AgentCard、Message、Task、Artifact、Streaming、Push 与协议边界 | draft |
| [`agent-card.md`](agent-card.md) | AgentCard 发现、接口协商、Skills、安全声明、扩展卡片与 Registry 边界 | draft |
| [`context-and-orchestration.md`](context-and-orchestration.md) | 系统级与 Agent 内部 Context、History、Message 的区别，以及中心式、去中心化和混合编排 | draft |
| [`message-task-artifact.md`](message-task-artifact.md) | Send Message 的直接响应与 Task 分流、任务所有权、多轮补充、本地映射和 Artifact 交付 | draft |
| [`task-delivery-and-recovery.md`](task-delivery-and-recovery.md) | Polling、Streaming、Push、断线恢复与 Hub Task Reconciler | draft |
| [`reliability-errors-and-cancellation.md`](reliability-errors-and-cancellation.md) | 传输/协议/Task 失败、幂等边界、模糊超时、重试判断和取消竞态 | draft |
| [`authentication-and-authorization.md`](authentication-and-authorization.md) | TLS、认证、授权、`AUTH_REQUIRED`、授权链和凭证传递边界 | draft |
| [`operations-and-task-management.md`](operations-and-task-management.md) | 11 项核心操作、Task 所有权、查询、终态限制与 Push 配置生命周期 | draft |
| [`protocol-bindings.md`](protocol-bindings.md) | JSON-RPC、gRPC、HTTP+JSON 的等价映射、接口选择和线级互操作 | draft |
| [`versioning-and-compatibility.md`](versioning-and-compatibility.md) | 产品版本与协议版本、逐请求协商、多版本迁移和安全回退边界 | draft |
| [`extensions-and-negotiation.md`](extensions-and-negotiation.md) | 扩展声明、请求级启用、对象扩展点、必需性和 URI 版本治理 | draft |
| [`content-and-media-exchange.md`](content-and-media-exchange.md) | Part 内容模型、文件与结构化数据、媒体协商和内容安全边界 | draft |
| [`agent-card-signing-and-caching.md`](agent-card-signing-and-caching.md) | JCS/JWS 签名验证、字段存在性、HTTP 缓存和 Registry 信任边界 | draft |
| [`push-notification-security.md`](push-notification-security.md) | Webhook 反向信任、SSRF、Callback 认证、重复投递和最终对账 | draft |
| [`wire-contract-and-conformance.md`](wire-contract-and-conformance.md) | ProtoJSON、字段 Presence、错误与时序等价、Inspector/TCK 测试范围 | draft |
| [`custom-bindings.md`](custom-bindings.md) | 自定义传输的完整映射、Service Parameters、安全、Streaming 和 URI 版本 | draft |
| [`migration-and-legacy-compatibility.md`](migration-and-legacy-compatibility.md) | 旧对象名、`kind` 移除、能力字段迁移和兼容层退役策略 | draft |
| [`plug-and-play-federation.md`](plug-and-play-federation.md) | 即插即用定义、LangGraph 边界、三种接入通道、项目源码核验和通用平台目标架构 | draft |
| `registry-discovery.md` | Nacos、AgentCard Registry、版本、Endpoint、健康和跨组织发现 | planned |
| `gateway-federation.md` | IBM ContextForge、Archestra、agentgateway 与 Federation Control Plane | planned |
| `runtime-task-lifecycle.md` | Agent Stack、Solace Agent Mesh、kagent、Durable Task、事件和 HITL | planned |
| [`project-landscape.md`](project-landscape.md) | 开源项目、官方示例、商业平台和小型实现的分层清单 | draft |
| `qa.md` | A2A 讨论、待核验点和源码/官方文档证据 | planned |

## 当前判断

完整的中立 A2A 联邦平台仍然少见。经本地源码核验，开源生态更像分层组合：Nacos 提供 Agent Registry、版本、Runtime Endpoint 和健康控制面；agentgateway 提供 A2A Data Plane；Solace Agent Mesh 提供外部 A2A Proxy 和 Mesh 发现；Routa 展示 Workflow 按 Agent Card URL 调用；Agent Stack 与 Bindu 提供 Provider 侧 Wrapper。自研平台的差异化应落在把这些能力组成“动态接入、统一治理、可替换调用”的完整闭环，而不是再实现一个固定的内部多 Agent 编排器。

## 相关入口

- [Agent Federation Hub 项目说明](../../../README.md)
- [来源仓库 A2A 目录](https://github.com/TsingFengIceberg/agent-systems-study/tree/98b4cbbba4877a8f40c52c5595f97a78bfaf1a07/DOCS/a2a)
- [来源仓库概念底座](https://github.com/TsingFengIceberg/agent-systems-study/tree/98b4cbbba4877a8f40c52c5595f97a78bfaf1a07/DOCS/concepts)
- [来源仓库横向对比](https://github.com/TsingFengIceberg/agent-systems-study/tree/98b4cbbba4877a8f40c52c5595f97a78bfaf1a07/DOCS/comparison)
- [来源仓库综合归纳](https://github.com/TsingFengIceberg/agent-systems-study/tree/98b4cbbba4877a8f40c52c5595f97a78bfaf1a07/DOCS/synthesis)
