# 从 Message 到 Task 与 Artifact

> **日期**: 2026-08-25 | **状态**: draft | **证据状态**: verified | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

Message 是一次跨 Agent 交流，Task 是 Remote Agent 创建并持续管理的一项工作，Artifact 是该工作产生的正式输出。一次 Send Message 可以直接得到 Message，也可以得到一个处于任意合法状态的 Task；是否创建 Task 由 Remote Agent 根据工作语义决定。

## 用办事大厅建立直觉

向法律咨询柜台询问“试用期最长多久”，柜台可以当场回答：

```text
Client Message
  -> Remote Message
```

提交一份 80 页合同并要求风险审查，则需要建立可追踪工单：

```text
Client Message
  -> Remote Task
  -> status updates / additional Messages
  -> Artifacts
  -> terminal state
```

可以记成：

```text
Message  = 这一次交流了什么
Task     = 对方正在办理什么工作
Artifact = 对方正式交付了什么
```

## Send Message 的三类结果

### 直接返回 Message

适合无需长期追踪的回答。Remote Agent 返回的 Message 必须带 `contextId`，但不必带 `taskId`，因为没有创建工单。

### 返回 Task

适合需要持续执行、查询、取消、多轮补充或正式产物的工作。新 Task 的 ID 由 Server 生成；Task 可以在返回时仍为 `SUBMITTED/WORKING`，也可以已经进入中断态或终态。

### 返回协议错误

请求格式、认证、能力或协议操作不合法时返回错误。业务工作已经建立但后来失败，应表现为 Task 的 `FAILED` 等状态，而不是把所有业务失败都混成传输错误。

## 谁决定是否创建 Task

Remote Agent 根据自身能力和执行方式决定直接返回 Message 还是创建 Task。Client 可以表达是否希望非流式调用尽快返回，但不能要求 Remote Agent 把本应追踪的工作伪装成普通聊天。

当前 `SendMessageConfiguration.return_immediately` 的语义是：

- `true`：创建 Task 后立即返回，即使仍在执行；
- `false`：等待 Task 到达终态或 `INPUT_REQUIRED/AUTH_REQUIRED` 中断态后再返回。

它只决定这次请求等待多久，不改变 Task 是否在后台继续执行，也不等同于 Streaming。定义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L143)。

## Task 的所有权与本地映射

Task 是 Remote Agent 的工作实体，所以新 `taskId` 由 Server 生成，定义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L163)。Federation Hub 不应把自己的本地任务 ID 强行当成远端任务 ID，而应保存映射：

```text
LocalTaskBinding
├── local_task_id
├── remote_agent_id
├── remote_agent_version / endpoint snapshot
├── remote_task_id
├── remote_context_id
├── credential / tenant reference
└── latest observed status and artifacts
```

同一个本地 Workflow 可以委托多个 Remote Agent，因此必须使用 `(remote agent identity, remote taskId)` 等明确范围定位远端任务，不能假设所有 Agent 的 Task ID 全局唯一。

## 一次完整长任务

```text
Client Agent                           Remote Agent
    |                                      |
    |-- Message: 审查合同 ---------------->|
    |<-- Task: SUBMITTED ------------------|
    |<-- Task: WORKING --------------------|
    |<-- Task: INPUT_REQUIRED -------------| 缺少适用地区
    |-- Message: 适用中国大陆 ------------>|
    |<-- Task: WORKING --------------------|
    |<-- Artifact: risk-report.xlsx -------|
    |<-- Task: COMPLETED ------------------|
```

`INPUT_REQUIRED` 和 `AUTH_REQUIRED` 是中断态，不是终态。Client 补充 Message 或在带外完成授权后，Remote Agent 可以继续原 Task。

## 多轮 Message 如何关联 Task

A2A Message 可以携带 `contextId` 和 `taskId`：

- Client 两者都不提供：Remote Agent 可以开始新的 Context 或 Task；
- 只提供 `contextId`：在同一协作事项中发起新交流或新 Task；
- 只提供 `taskId`：Server 从 Task 推断正确的 `contextId`；
- 两者都提供：必须与已有 Task 的关联一致，否则请求无效。

精确语义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L254)。这意味着 `contextId` 关联一组事项，`taskId` 定位具体工作，二者不能互换。

## Task Status 中的 Message

Task status 除了状态，还可以携带一条 Message，用于解释当前变化：

```text
state   = INPUT_REQUIRED
message = “请补充合同适用地区和签署主体”
```

状态是机器可判断的控制信号，Message 是给 Agent 或用户理解的内容。调用方不应只解析自然语言来猜任务是否完成。

## Artifact 为什么独立于 Message

普通 Message 可以说“审查完成”，但正式风险报告需要稳定身份、名称、描述、媒体类型和可追加内容。Artifact 因此是 Task 输出对象，而不是随意塞进最后一条聊天消息的附件。

例如一个 Task 可以产生：

```text
Artifact A = 风险清单 JSON
Artifact B = 带批注合同 PDF
Artifact C = 审查摘要 Markdown
```

每个 Artifact 在 Task 内有唯一 `artifactId`，由一个或多个 Part 组成，定义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L279)。

## Artifact 的增量交付

流式执行时，Remote Agent 可以通过 `TaskArtifactUpdateEvent` 多次发送同一个 Artifact：

- `append=true`：把本次 Part 追加到此前相同 `artifactId` 的内容；
- `lastChunk=true`：本次是该 Artifact 的最后一块。

因此调用方需要按 `(taskId, artifactId)` 聚合，而不是把每个事件当成一份新文件。定义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L307)。

## A2A Task 与内部 Workflow 的边界

Remote Agent 内部可能执行：

```text
文档解析 -> 条款识别 -> 风险评估 -> 人工复核
```

外部 Client 可能只看见：

```text
WORKING -> INPUT_REQUIRED -> WORKING -> COMPLETED
```

A2A Task 是外部工单，LangGraph State、checkpoint、内部 Tool Call 和审批节点是 Remote Agent 的生产流程。A2A 不要求公开内部图，也不保证 Remote Agent 的 Task 持久化方式。

## 平台实现容易犯的错误

- 把每个 Message 都强制创建成 Task，导致简单交互负担过重；
- 把 Task 当作一条聊天记录，无法正确取消、恢复和聚合 Artifact；
- 只保存远端 `taskId`，没有保存 Agent 身份和 Endpoint/版本快照；
- 把 `INPUT_REQUIRED` 当失败后重新创建 Task，丢失原工单关联；
- 只读状态说明 Message，不读取机器状态；
- 把 Artifact chunk 当成多个独立文件；
- 把 A2A Task history 当成 Remote Agent 的完整内部推理和审计日志。

## QA / 讨论记录

### Q: 为什么 Send Message 不总是返回 Task？

> **状态**: verified | **来源**: protocol source

A2A 同时支持无需任务开销的直接交流和需要长期追踪的工作。Remote Agent 可以返回 Message，也可以创建 Task；Streaming 同样支持 message-only stream 和 task lifecycle stream。

### Q: `returnImmediately=true` 是否等于异步 Push？

> **状态**: verified | **来源**: protocol source

不等于。它只要求非流式 Send Message 在创建 Task 后立即返回。之后 Client 仍需选择 Polling、Subscribe Streaming 或 Push Notification 获取更新。

### Q: Client 如何继续 `INPUT_REQUIRED` 的 Task？

> **状态**: verified | **来源**: protocol source

Client 发送关联原 `taskId` 的新 Message；可以同时携带匹配的 `contextId`，也可以只提供 `taskId` 让 Server 推断 Context。Remote Agent 接受补充后继续原任务。

### Q: Task 完成是否意味着 Artifact 一定只有一个？

> **状态**: verified | **来源**: protocol source

不是。一个 Task 可以包含多个 Artifact，每个 Artifact 又可以包含多个 Part，并可在 Streaming 中分块追加。

## 相关文档

- [A2A 协议原理与互操作模型](protocol-and-interop.md)
- [Task 更新交付与断线恢复](task-delivery-and-recovery.md)
- [Context、History、Message 与编排拓扑](context-and-orchestration.md)
- [Part、媒体类型与业务数据交换](content-and-media-exchange.md)
- [A2A 即插即用与通用联邦平台](plug-and-play-federation.md)
