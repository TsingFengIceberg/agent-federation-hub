# A2A 系统中的 Context、History、Message 与编排拓扑

> **日期**: 2026-08-25 | **状态**: draft | **证据状态**: verified（A2A 对象语义）/ inference（平台拓扑） | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

A2A 的 `contextId`、Task history 和 Message 是跨系统协作对象，不等于某个 LLM 的上下文窗口、Prompt transcript 或长期 Memory。一个 A2A 系统可以有全局控制面，但不必须存在一个知道全部业务内容的“全局 Agent”。

## 为什么同名概念容易混淆

传统 Agent 应用和 A2A 协作系统都会使用 context、history、message，但它们处于不同边界：

```text
模型调用层
  -> Prompt、上下文窗口、模型消息

单个 Agent Runtime
  -> session、内部状态、checkpoint、memory、tool result

A2A 协作层
  -> contextId、Task、Message、Task history、Artifact

联邦平台控制面
  -> Agent 注册、权限、路由、健康、审计、成本与策略
```

同一个词出现在不同层，不代表它们是同一个对象或应当放进同一个数据库。

## Context 的三种含义

### 模型内部 Context

模型内部 Context 是某一次 LLM 推理能够看到的输入，通常包括 system prompt、用户消息、工具结果、压缩摘要和检索内容。它受上下文窗口限制，并直接影响模型下一次生成。

它回答的是：**这次模型推理看到了什么？**

### 单个 Agent 的 Runtime Context

Agent Runtime Context 比模型窗口更宽，可以包括 session 状态、工作目录、LangGraph State、checkpoint、长期 Memory、权限、当前用户和工具执行记录。Runtime 会从这些状态中挑选一部分放进下一次模型调用。

它回答的是：**这个 Agent 为了继续运行保存了什么内部状态？**

### A2A 的 Collaboration Context

A2A `contextId` 是把一组相关 Message 和 Task 关联到同一协作事项的标识。它更像案件号、项目号或工单会话号，而不是一块所有 Agent 都能直接读取的全局 Prompt。

例如一个采购事项可以包含：

```text
contextId = procurement-2026-0815

Task A = 供应商询价
Task B = 法务审查
Task C = 财务预算确认
```

三个 Task 可以属于同一 `contextId`，但三个 Remote Agent 不会因此自动看到彼此的全部内部状态。调用方仍要通过 Message、Artifact 或受控共享存储明确传递必要信息。

它回答的是：**这些跨 Agent 交互属于同一件什么事？**

## Context 不是自动全共享空间

A2A 协议提供关联标识，不规定一个全局 Context Store，更不会自动复制所有参与者的内容。生产平台通常要把信息分成三类：

| 范围 | 典型内容 | 默认可见性 |
|---|---|---|
| 联邦控制面 | Agent 身份、Endpoint、权限、健康、路由、审计 | 平台按管理权限可见 |
| 协作事项 | `contextId`、Task 摘要、共享 Artifact、必要业务字段 | 参与该事项且获授权的主体可见 |
| Agent 私有状态 | Prompt、Memory、内部 history、工具凭证、LangGraph checkpoint | 只属于该 Agent 系统 |

因此合理模型不是“所有 Agent 共享一个巨大 Context”，而是：

```text
少量可治理的共享协作事实
+ 每个 Agent 独立的私有运行状态
+ 通过 Message / Artifact 明确交换的数据
```

这也是 A2A 能跨公司工作的前提。供应商 Agent 没有理由读取采购方的全部内部对话，采购方也不应取得供应商的 Prompt、客户数据或 Memory。

## History 的三种含义

### 模型对话 History

模型 History 是为了下一次推理而组织的消息序列。它可能被裁剪、摘要、重写或选择性恢复，不一定是完整审计记录。

### Agent Runtime History

Runtime History 可以包含模型消息、工具调用、观察结果、状态转换、人工审批和 checkpoint。它服务恢复、调试和后续推理，通常是内部实现细节。

### A2A Task History

A2A Task 的 `history` 是与该远程任务相关的协议 Message 集合。它用于让 Client 理解任务交互过程，但不承诺暴露 Remote Agent 的内部推理、工具调用或所有工作日志。

可以记成：

```text
模型 History      = 为下一次思考准备的材料
Runtime History   = 本 Agent 内部发生过什么
A2A Task History  = 双方围绕这张工单正式交流过什么
审计日志          = 平台需要证明谁在何时做了什么
```

这些记录可以相互投影，但不能简单共用一张“history 表”就认为语义相同。

## Message 的三种含义

### 模型 Message

模型 Message 通常使用 `system`、`user`、`assistant`、`tool` 等角色，目标是构造一次模型输入。

### Agent 内部事件或消息

Agent Runtime 还可能有 ToolMessage、状态事件、队列消息和 UI event。它们不一定会离开本系统，也不一定符合 A2A Schema。

### A2A Message

A2A Message 是两个独立 Agent 系统之间的协议通信单元，包含稳定的 `messageId`、角色、一个或多个 Part，以及可选的 `contextId` 和 `taskId`。Part 可以是文本、结构化数据、字节或 URL。

A2A Message 到达 Remote Agent 后，Remote Agent 可以把它转换成自己的内部模型消息，但这种转换属于 Remote Agent 的实现：

```text
A2A Message
  -> 协议校验与授权
  -> Remote Agent 的输入适配
  -> 内部 State / Prompt / Workflow
  -> 内部模型调用和工具执行
  -> A2A Task / Message / Artifact
```

## `contextId`、`taskId` 与 `messageId`

用“案件、工单、来函”最容易区分：

| A2A 字段 | 比喻 | 作用 |
|---|---|---|
| `contextId` | 案件号 | 关联一组长期协作事项 |
| `taskId` | 工单号 | 标识一项可追踪、可取消、可恢复的具体工作 |
| `messageId` | 来函编号 | 标识某一次发送的消息，便于去重和追踪 |

同一案件可以有多张工单，一张工单可以有多次消息。一个 Agent 的内部 checkpoint 通常不会使用这三个 ID 直接替代自己的 State key，但可以保存映射关系。

## 是否必须有一个全局 Agent

不必须。需要“全局把控”不等于必须让一个 LLM Agent 掌握所有内容。至少有三种拓扑。

### 中心协调者

```text
用户 -> Coordinator Agent -> 专业 Agent A / B / C
```

Coordinator 负责拆解任务、选择 Agent、汇总结果。优点是路径清楚，缺点是容易成为瓶颈、单点和信息汇聚风险。

### 去中心化协作

```text
Agent A -> Agent B -> Agent C
    \-----------> Agent D
```

参与者根据公开能力继续委托其他 Agent。它减少中央业务决策，但更依赖身份、策略、预算、循环检测、可观测性和跨节点一致性。

### 混合拓扑

生产系统更可能采用混合方式：

```text
控制面：Registry / Policy / Identity / Audit / Budget
业务面：一个或多个 Coordinator / Workflow / Agent 间直接协作
数据面：Gateway / Proxy / Event Mesh
```

全局控制面掌握地址、权限、健康和任务元数据，但不必读取所有业务正文；业务协调者只取得完成当前事项所需的最小上下文。

## LangGraph 可以放在哪里

LangGraph 与 A2A 不是二选一。它至少可以出现在两个位置：

1. 一个远程 Agent 内部使用 LangGraph 实现自己的垂直领域流程，对外只暴露 A2A；
2. Hub 内部使用 LangGraph 实现某个已知业务 Workflow，再通过 A2A 节点调用外部 Agent。

例如：

```text
Federation Hub Workflow（可使用 LangGraph）
  -> 法务公司 A2A Agent（内部也可使用 LangGraph）
  -> 财务公司 A2A Agent（内部实现未知）
  -> 物流公司 A2A Agent（内部可能是传统服务）
```

LangGraph 管的是一个所有者可定义的状态图；A2A 管的是独立系统间的远程协作契约。只有当所有节点都由同一团队控制、部署和升级时，直接把它们写成 LangGraph 节点通常更简单。

## 平台设计原则

- 不把 `contextId` 实现成无权限边界的全局共享内存；
- 不把 Task history 当作 Remote Agent 的完整推理过程；
- 不要求 Agent 上传 Prompt、Memory 或内部编排图才能接入；
- 控制面元数据与业务正文分开授权、存储和保留；
- Message、Task、Artifact 和内部 Runtime State 使用明确映射，不混成一个对象；
- 去中心化调用仍必须经过调用身份、预算、循环检测和审计约束。

## QA / 讨论记录

### Q: A2A 的 `contextId` 是不是整个系统的全局 Context？

> **状态**: verified / inference | **来源**: source-code / discussion

它是跨消息、跨任务的协作关联标识，可以由平台用于组织系统级事项，但协议没有规定一块所有 Agent 自动共享的上下文内存。共享什么内容仍由 Message、Artifact、权限和平台存储策略决定。

### Q: 全局控制是否意味着必须设计一个全局 Agent？

> **状态**: inference | **来源**: architecture reasoning

不意味着。Registry、Policy、Gateway、Task Store 和 Audit 可以提供确定性的全局控制；业务协调可以由中心 Agent、Workflow、多个局部协调者或去中心化协作完成。

### Q: A2A 系统是否仍然拥有传统 Agent 的 context、history 和 message？

> **状态**: verified | **来源**: protocol / discussion

拥有，但分层后的指代不同。每个 Agent 仍有自己的内部 Context 和 History；A2A 又增加跨系统的 Context ID、协议 Message 和 Task history。实现时必须保留边界和映射关系。

## 相关文档

- [A2A 协议原理与互操作模型](protocol-and-interop.md)
- [AgentCard：发现、协商与信任边界](agent-card.md)
- [从 Message 到 Task 与 Artifact](message-task-artifact.md)
- [A2A 即插即用与通用联邦平台](plug-and-play-federation.md)
