<h1 align="center">Agent Federation Hub</h1>

<p align="center"><a href="./README_en.md">English</a> | 中文</p>

Agent Federation Hub 是一个面向跨领域、跨组织 Agent 协作的研究型开源项目。它的目标不是替某个业务内部编排所有 Agent，而是探索如何让独立部署、使用不同框架、拥有不同权限边界的 Agent 系统，通过可发现、可认证、可观测、可恢复的协议协作完成一项任务。

> 当前状态：A2A 协议与开源生态研究基线已完整导入；平台实现、A2A Inspector / TCK 一致性验证和具体技术选型仍待开展。导入的研究结论不等同于已经实现的产品能力。

## 设计方向

```text
A2A Protocol
  -> Registry / Discovery
  -> Gateway / Data Plane
  -> Agent Runtime / Domain Workflow
  -> Event and Async Adapters
  -> Governance, Evaluation, and Product Surface
```

初步边界是：以 A2A 作为跨 Agent 的主协议，AAMP 作为邮箱型异步协作适配器；LangGraph 等框架负责单个领域内部的多 Agent 编排。Nacos、agentgateway、Agent Stack、Solace Agent Mesh、ShrimpCrab 等项目用于研究不同层次的实现取舍，而不是直接拼接成既定技术栈。

## 先读什么

1. 在本地先读取 `.handoff/current.md`：本项目的背景、目标、未决问题和下一步。
2. 阅读 [`docs/research/a2a-study/README.md`](docs/research/a2a-study/README.md)：已导入的 A2A 协议与跨项目研究入口。
3. 再按任务读取 `.handoff/decisions/` 下的架构决策；`.handoff/` 只保存本地交接状态，不替代正式文档。

## 研究基础

| 入口 | 用途 |
|---|---|
| [`docs/README.md`](docs/README.md) | 正式文档总入口和文档所有权边界 |
| [`docs/research/a2a-study/`](docs/research/a2a-study/) | 从 `agent-systems-study` 固定 commit 完整导入的 A2A 研究快照 |
| [`submodules/`](submodules/) | A2A、AAMP、Registry、Gateway、Runtime 与示例项目的固定源码版本 |

通用 A2A 协议和跨项目研究以 `agent-systems-study` 为权威来源，本仓库保留可追溯的完整快照并按来源 commit 单向同步；本项目自身的架构、ADR、规格、实现和测试只在本仓库演进。

## 计划中的验证场景

狼人杀用于验证私有信息、回合状态和对抗性协作；软件开发、采购、研究、应急响应、内容制作、个人助理、IoT、AIOps 和 Agent Marketplace 用于验证长任务、审批、异步、事件、多模态、身份、计费和不可信 Agent 等正交能力。场景是测试平台通用性的工具，不是平台的固定业务范围。

## 项目阶段

- **Phase 0：协议基线与一致性验证**：研究笔记已导入；下一步确定初始 A2A 版本与 Binding Profile，并用 Inspector / TCK 和最小契约测试核验实现理解。
- **Phase 1：最小互操作样例**：实现一个 A2A Client、Server、Registry 和可观测任务流。
- **Phase 2：异步与治理**：加入 AAMP 适配器、委托身份、租户、审计、重试和人工审批。
- **Phase 3：多场景验证**：用正交场景验证同一核心是否能复用。

## 许可证与实现承诺

许可证、技术栈和生产部署方案尚未定稿。实现前先以协议和可重复的互操作测试为依据，避免把某一个 Demo 的 API 直接升级成平台契约。
