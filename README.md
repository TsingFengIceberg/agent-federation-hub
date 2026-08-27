<h1 align="center">Agent Federation Hub</h1>

<p align="center"><a href="./README_en.md">English</a> | 中文</p>

Agent Federation Hub 是一个面向跨领域、跨组织 Agent 协作的研究型开源项目。它的目标不是替某个业务内部编排所有 Agent，而是探索如何让独立部署、使用不同框架、拥有不同权限边界的 Agent 系统，通过可发现、可认证、可观测、可恢复的协议协作完成一项任务。

> 当前状态：A2A 协议与开源生态研究基线已完整导入；仓库已有 A2A `1.0` JSON-RPC/SSE 互操作基线、可信 Principal/Scope 边界、可替换 SecretProvider、PostgreSQL 事务存储、多实例工作租约、持久 Push inbox 和基于已提交 Event 的连续 SSE。JWT 当前使用静态 PEM 公钥，动态 OIDC/JWKS、限流、外部 Artifact 对象存储、备份/HA 验证以及协议对齐的完整 Inspector/TCK 仍未完成；本地 journal 仍只适合单进程开发。

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
| [`docs/specifications/task-event-artifact-contract.md`](docs/specifications/task-event-artifact-contract.md) | 已实现的首版联邦 Task、Event 与 Artifact 契约 |
| [`docs/architecture/phase-one-hub-conformance-boundary.md`](docs/architecture/phase-one-hub-conformance-boundary.md) | Hub、Push、TCK、Registry/Gateway 与 AAMP 的当前能力边界 |
| [`docs/adr/0003-authenticated-principal-and-policy-boundary.md`](docs/adr/0003-authenticated-principal-and-policy-boundary.md) | 已实现的认证 Principal、授权、审计和 SecretProvider 边界 |
| [`docs/adr/0004-postgresql-leased-background-execution.md`](docs/adr/0004-postgresql-leased-background-execution.md) | PostgreSQL 事务、多实例租约与持久 Push inbox 决策 |

通用 A2A 协议和跨项目研究以 `agent-systems-study` 为权威来源，本仓库保留可追溯的完整快照并按来源 commit 单向同步；本项目自身的架构、ADR、规格、实现和测试只在本仓库演进。

## 计划中的验证场景

狼人杀用于验证私有信息、回合状态和对抗性协作；软件开发、采购、研究、应急响应、内容制作、个人助理、IoT、AIOps 和 Agent Marketplace 用于验证长任务、审批、异步、事件、多模态、身份、计费和不可信 Agent 等正交能力。场景是测试平台通用性的工具，不是平台的固定业务范围。

## 项目阶段

- **Phase 0：协议基线与一致性验证**：已选择 A2A `1.0` JSON-RPC/SSE 初始 Profile，并完成自有 Go/Python 互操作与契约测试；与所选协议修订一致的完整 Inspector/TCK 仍待完成。
- **Phase 1：最小互操作样例**：首个 Go Hub 服务切片已实现内置 Agent Card 注册、持久任务日志、可恢复事件流、取消、对账、租户隔离和 Push 接收；分布式与生产加固不在当前完成声明内。
- **Phase 2：异步与治理**：已实现首版 JWT Principal、Scope 授权、结构化审计、SecretProvider、PostgreSQL 租约后台对账和持久 Push inbox；动态身份联盟、限流、AAMP 传输和人工审批仍待实现。
- **Phase 3：多场景验证**：用正交场景验证同一核心是否能复用。

## 许可证与实现承诺

许可证、技术栈和生产部署方案尚未定稿。实现前先以协议和可重复的互操作测试为依据，避免把某一个 Demo 的 API 直接升级成平台契约。
