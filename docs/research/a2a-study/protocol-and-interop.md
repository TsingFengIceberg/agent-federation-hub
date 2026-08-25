# A2A 协议原理与互操作模型

> **日期**: 2026-08-25 | **状态**: draft | **证据状态**: verified（协议基础对象）/ inference（平台启发） | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

A2A 是独立 Agent 系统之间的外部协作协议：不同公司、框架和部署环境中的 Agent，可以在不暴露内部 Prompt、Memory、Tool 和编排图的情况下，发现彼此的能力、委托长期任务、交换进度并交付结构化产物。

它标准化的是 Agent 的**外部协作契约**，而不是 Agent 的**内部实现**。

## 为什么普通 HTTP API 不够

单个 REST API 可以定义某个业务动作，却不会天然统一以下 Agent 协作问题：

- 如何发现远程 Agent 能做什么、接受什么输入以及怎样认证；
- 长任务如何表示、查询、取消和恢复；
- Agent 中途需要用户补充输入或认证时怎样表达；
- 文本、结构化数据、文件和多媒体怎样统一承载；
- 调用方如何通过同步响应、流式事件或异步推送取得进度和结果；
- 成功、失败、取消与拒绝怎样形成一致的生命周期语义。

A2A 将这些共性抽象为 AgentCard、Message、Part、Task、TaskState 和 Artifact。官方项目将其目标概括为连接不同生态中的 Agent、支持长期协作并保持内部实现不透明，见 [A2A README](../../../submodules/a2a/README.md#why-a2a)。

## 交互角色

一次 A2A 交互包含 Client Agent 和 Remote Agent（A2A Server）：

```text
Client Agent                         Remote Agent
发现并选择能力                       发布 AgentCard
发送 Message                         接受请求并创建 Task
查询、订阅或取消 Task                更新状态并执行内部流程
接收状态与 Artifact                  交付结果
```

这只是一次通信中的角色。同一个 Agent 可以作为上游请求的 Server，也可以继续作为 Client 向其他 Agent 委托任务。A2A 不要求 Remote Agent 暴露其内部是单 Agent、LangGraph 多节点系统还是其他 Runtime。

## 核心对象

### AgentCard：能力与连接契约

AgentCard 是 Agent 对外发布的自描述清单，声明名称、描述、版本、接口、能力、安全要求、默认输入输出媒体类型和 Skills。源码中的结构定义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L358)。

它回答：

- 这个 Agent 是谁、由谁提供；
- 它承诺擅长什么；
- 从哪个接口、使用哪个协议版本访问；
- 是否支持 Streaming、Push Notification 或协议扩展；
- 接受和产出哪些媒体类型；
- 调用前需要满足什么认证要求。

AgentCard 不是运行中的任务状态，也不是 Registry 本身。Registry 可以收集、索引和治理 AgentCard，但 AgentCard 只描述一个 Agent 的公开契约。

### Part：最小内容单元

Part 是 Message 和 Artifact 的内容容器，当前 schema 支持：

- `text`：文本；
- `raw`：原始字节，在 JSON 中编码；
- `url`：指向文件或媒体的地址；
- `data`：任意结构化 JSON 值；
- `filename`、`media_type` 和自定义 metadata。

这使 A2A 不局限于两个 LLM 交换字符串，而能传递结构化表单、文件和多媒体。定义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L221)。

### Message：一次交流

Message 是 Client 与 Remote Agent 之间的一次通信单元，包含唯一 `message_id`、发送者角色、若干 Part，以及可选的 `context_id`、`task_id`、metadata 和被引用的其他 Task。

`context_id` 将多次交互组织到同一协作上下文；`task_id` 表示这条消息正在创建、补充或继续哪项具体工作。定义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L254)。

### Task：可持续追踪的工作实体

Task 是 A2A 的核心工作单元。它由 Server 为新任务生成 ID，并保存：

- `context_id`；
- 当前 `TaskStatus`；
- 交互历史；
- 已产生的 Artifacts；
- 自定义 metadata。

Task 不是一条聊天消息，而是一项工作从接受、执行、中断到终止的完整生命周期，定义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L163)。

### Artifact：正式任务产物

Artifact 表示 Task 的输出，例如报告、报价单、JSON 数据、代码补丁、图片或视频。Artifact 由一个或多个 Part 组成，拥有任务内唯一 ID、名称、描述和 metadata，定义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L279)。

三个对象的边界可以概括为：

```text
Message  = 交流了什么
Task     = 正在完成什么工作
Artifact = 正式交付了什么
```

## Task 生命周期

当前 schema 定义的主要状态如下：

| 状态 | 类型 | 含义 |
|---|---|---|
| `SUBMITTED` | 进行中 | Server 已接受任务 |
| `WORKING` | 进行中 | Agent 正在处理 |
| `INPUT_REQUIRED` | 中断 | 需要 Client 或用户补充输入 |
| `AUTH_REQUIRED` | 中断 | 继续执行前需要认证或授权 |
| `COMPLETED` | 终态 | 成功完成 |
| `FAILED` | 终态 | 执行失败 |
| `CANCELED` | 终态 | 任务被取消 |
| `REJECTED` | 终态 | Agent 决定不执行任务 |

状态定义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L186)。典型流转为：

```text
SUBMITTED -> WORKING -> COMPLETED
                |
                +-> INPUT_REQUIRED -> WORKING
                +-> AUTH_REQUIRED  -> WORKING
                +-> FAILED / CANCELED / REJECTED
```

中断状态表达的是“条件尚未满足但任务仍可能继续”，与终态不同。客户端通过后续 Message 补齐信息或授权，Remote Agent 再恢复内部执行；协议不规定内部 checkpoint 必须怎样实现。

## 同步、流式和异步交付

### 同步响应

Client 发送 Message 并等待 Remote Agent 返回 Message、Task 或终止结果，适合短任务。同步并不意味着所有任务都必须立即完成；请求配置可以决定是否在任务尚未终止时尽快返回。

### Streaming

客户端保持流式连接，接收状态事件和 Artifact 增量：

```text
Client -> SendStreamingMessage
Client <- WORKING
Client <- Artifact chunk
Client <- Artifact chunk
Client <- COMPLETED
```

`TaskStatusUpdateEvent` 表示状态变化，`TaskArtifactUpdateEvent` 支持对同一 Artifact 追加 chunk 并标记最后一块，见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L295)。

### Push Notification

Client 为 Task 注册回调配置后可以断开连接；Remote Agent 在任务变化时主动通知回调端点。它适合跨组织长流程、离线 Agent、邮件和 IM 等异步场景。

Streaming 依赖持续连接，Push 面向断开后的回调，两者不能混为一种机制。项目 README 将主要交互概括为同步响应、SSE Streaming 和异步 Push，见 [A2A README](../../../submodules/a2a/README.md#key-features)。

## 一次完整交互

以跨组织采购为例：

1. 办公助理 Agent 获取供应商 Agent 的 AgentCard。
2. 根据 Skill、媒体类型、安全要求和接口版本判断是否可以调用。
3. 发送包含产品、数量、截止日期和规格文件的 Message。
4. 供应商 Agent 创建 Task，状态从 `SUBMITTED` 进入 `WORKING`。
5. 如果缺少发票类型，Task 进入 `INPUT_REQUIRED` 并携带说明 Message。
6. 办公助理补充信息，供应商 Agent 恢复内部流程。
7. 供应商 Agent 通过 Artifact 返回结构化报价和 PDF。
8. Task 进入 `COMPLETED`；调用方可以查询、订阅或在执行中取消它。

调用方只依赖公开协议，不需要知道供应商内部用了几个 Agent、什么模型或哪些工具。

## A2A、MCP 与 LangGraph 的边界

| 技术 | 主要连接对象 | 核心问题 |
|---|---|---|
| MCP | Agent 与 Tool / Resource | Agent 如何调用搜索、数据库、文件系统和业务能力 |
| LangGraph | 一个系统内部的状态和节点 | 如何编排、路由、checkpoint、interrupt 和恢复 |
| A2A | 独立 Agent 系统之间 | 如何发现远程能力、委托并跟踪长期任务 |

典型组合为：

```text
办公助理 Agent
      |
      | A2A
      v
采购领域 Agent 系统
      |
      | LangGraph
      v
询价 Agent -> 审核 Agent -> 下单 Agent
      |
      | MCP
      v
商品库、订单系统、邮件服务
```

精髓是：**LangGraph 管内部流程，MCP 接工具和资源，A2A 连接独立 Agent 系统。**

## 协议与平台的边界

A2A 不自动提供完整的生产平台。以下能力仍属于 Registry、Gateway、Runtime 或 Governance 层：

- AgentCard 的收集、搜索、健康检查和版本治理；
- 动态路由、负载均衡、限流、重试和跨网络策略；
- 多租户、身份信任、用户权限委托、审计和密钥治理；
- Task、事件与 Artifact 的持久化、恢复、补偿和清理；
- 计费、配额、质量评估、恶意 Agent 防护和全链路追踪；
- 领域内部的 Agent 选择、Workflow 和业务事务。

因此面向生产的 Federation Hub 更接近：

```text
A2A Protocol
  + Registry / Discovery
  + Gateway / Routing
  + Identity / Delegation
  + Durable Task / Artifact Store
  + Audit / Trace / Evaluation
  + Tenant / Policy / Quota
```

这部分是根据协议边界得到的架构推论，状态为 `inference`，后续需要结合 Nacos、agentgateway、Agent Stack 等项目继续核验。

## QA / 讨论记录

### Q: A2A 是否只是两个 LLM 互相聊天？

> **状态**: verified | **来源**: source-code / official-docs

不是。A2A 把远程 Agent 建模为可发现、可委托和可追踪的服务主体，核心还包括 Task 生命周期、结构化 Artifact、流式更新、异步 Push 和安全要求。

### Q: AgentCard 是否等于 Agent Registry？

> **状态**: verified / inference | **来源**: source-code / discussion

不等于。AgentCard 是单个 Agent 的自描述契约；Registry 是收集、索引、健康检查、筛选和治理这些契约的平台组件。

### Q: A2A 是否取代 MCP 或 LangGraph？

> **状态**: verified | **来源**: official-docs / discussion

不取代。MCP 主要连接 Agent 与工具或资源，LangGraph 主要处理应用内部的状态编排，A2A 处理独立 Agent 系统之间的远程协作。

## 相关文档

- [A2A 研究入口](README.md)
- [A2A 项目景观](project-landscape.md)
- [AgentCard：发现、协商与信任边界](agent-card.md)
- [Context、History、Message 与编排拓扑](context-and-orchestration.md)
- [从 Message 到 Task 与 Artifact](message-task-artifact.md)
- [Task 更新交付与断线恢复](task-delivery-and-recovery.md)
- [错误、幂等、重试与取消](reliability-errors-and-cancellation.md)
- [认证、授权与任务中授权](authentication-and-authorization.md)
- [A2A 操作集与 Task 管理边界](operations-and-task-management.md)
- [协议 Binding 与互操作](protocol-bindings.md)
- [A2A 版本协商与兼容性](versioning-and-compatibility.md)
- [Extension 扩展与能力协商](extensions-and-negotiation.md)
- [Part、媒体类型与业务数据交换](content-and-media-exchange.md)
- [Agent Card 签名、规范化与缓存](agent-card-signing-and-caching.md)
- [Push Notification 安全与 Webhook 信任](push-notification-security.md)
- [A2A 线级数据契约与一致性测试](wire-contract-and-conformance.md)
- [A2A 自定义 Binding 设计](custom-bindings.md)
- [A2A 旧版迁移与遗留兼容](migration-and-legacy-compatibility.md)
- [A2A 即插即用与通用联邦平台](plug-and-play-federation.md)
- [来源仓库 MCP 概念底座](https://github.com/TsingFengIceberg/agent-systems-study/blob/98b4cbbba4877a8f40c52c5595f97a78bfaf1a07/DOCS/concepts/mcp.md)
