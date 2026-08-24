<h1 align="center">Agent Federation Hub</h1>

<p align="center"><a href="./README_en.md">English</a> | 中文</p>

Agent Federation Hub 是一个面向跨领域、跨组织 Agent 协作的研究型开源项目。它的目标不是替某个业务内部编排所有 Agent，而是探索如何让独立部署、使用不同框架、拥有不同权限边界的 Agent 系统，通过可发现、可认证、可观测、可恢复的协议协作完成一项任务。

> 当前状态：研究与协议学习阶段（planned）。尚未承诺完整实现，也不把交接文档中的讨论推断当作已验证的产品能力。

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

## 我的 Agent 项目

这里也汇总目前正在建设的几个 Agent 相关项目，分别覆盖应用、多 Agent Harness、业务 Agent 和跨领域平台方向：

| 项目 | 简介 |
| --- | --- |
| [Competitive Analysis Agent](https://github.com/TsingFengIceberg/competitive-analysis-agent) | 基于 LangGraph 的竞品情报协作系统，由多个专职 Agent 完成采集、分析、审查和可溯源报告生成。 |
| [Coquo Code](https://github.com/TsingFengIceberg/coquo-code) | 面向本地单用户的 Coding Agent CLI，在受控 workspace 中执行工具、管理可恢复 Session、Task 和 Team。 |
| [Curmerce](https://github.com/TsingFengIceberg/curmerce) | 面向兴趣消费的社区内容与多模式交易平台，并规划接入基于商品和社区经验的消费 Agent。 |
| [Agent Federation Hub](https://github.com/TsingFengIceberg/agent-federation-hub) | 正在规划的通用跨领域、跨组织 A2A Agent 联邦平台，探索发现、信任、任务协作和异步互操作。 |

## 先读什么

1. 在本地先读取 `.handoff/current.md`：本项目的背景、目标、未决问题和下一步。
2. 再按任务读取 `.handoff/decisions/` 下的架构决策和 `.handoff/research-index.md`。
3. 公开的正式设计文档会随着协议学习和源码核验逐步迁移到 `docs/`。

## 计划中的验证场景

狼人杀用于验证私有信息、回合状态和对抗性协作；软件开发、采购、研究、应急响应、内容制作、个人助理、IoT、AIOps 和 Agent Marketplace 用于验证长任务、审批、异步、事件、多模态、身份、计费和不可信 Agent 等正交能力。场景是测试平台通用性的工具，不是平台的固定业务范围。

## 项目阶段

- **Phase 0：协议学习**：理解 AgentCard、Message、Task、Artifact、Streaming、Push、认证、取消、恢复和错误语义。
- **Phase 1：最小互操作样例**：实现一个 A2A Client、Server、Registry 和可观测任务流。
- **Phase 2：异步与治理**：加入 AAMP 适配器、委托身份、租户、审计、重试和人工审批。
- **Phase 3：多场景验证**：用正交场景验证同一核心是否能复用。

## 许可证与实现承诺

许可证、技术栈和生产部署方案尚未定稿。实现前先以协议和可重复的互操作测试为依据，避免把某一个 Demo 的 API 直接升级成平台契约。
