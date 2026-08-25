# A2A 操作集与 Task 管理边界

> **日期**: 2026-08-25 | **状态**: draft | **证据状态**: verified | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

A2A 不是允许 Client 对远端 Task 做任意 CRUD 的任务数据库协议。Client 通过 Message 表达意图，并使用查询、订阅、取消和 Push 配置等管理操作；Remote Agent 决定是否创建 Task，并独占 Task 状态推进和 Artifact 产出的事实权。

## 完整操作集

当前规范定义 11 项与 Binding 无关的核心操作：

| 类别 | 操作 | 作用 |
|---|---|---|
| 发起工作 | `SendMessage` | 发送 Message，得到直接 Message 或可追踪 Task |
| 发起并观察 | `SendStreamingMessage` | 发送 Message，并流式接收 Task、状态和 Artifact 事件 |
| 查询 | `GetTask` | 获取指定 Task 的当前快照 |
| 查询 | `ListTasks` | 按权限范围、Context、状态和时间列举 Task |
| 控制 | `CancelTask` | 请求 Remote Agent 尝试取消未终止 Task |
| 观察 | `SubscribeToTask` | 为已有、未终止 Task 建立更新流 |
| Push 配置 | `CreateTaskPushNotificationConfig` | 为 Task 创建异步回调配置 |
| Push 配置 | `GetTaskPushNotificationConfig` | 查询一项回调配置 |
| Push 配置 | `ListTaskPushNotificationConfigs` | 列举 Task 的回调配置 |
| Push 配置 | `DeleteTaskPushNotificationConfig` | 幂等删除一项回调配置 |
| 能力发现 | `GetExtendedAgentCard` | 认证后获取可能更详细的 Agent Card |

与 Binding 无关的操作语义见 [specification.md](../../../submodules/a2a/docs/specification.md#3-a2a-protocol-operations)，服务定义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L649)。

## 为什么没有 CreateTask

`SendMessage` 是发起交互的唯一核心入口。Remote Agent 可以对简单请求直接返回 Message，也可以为需要追踪的工作创建 Task：

```text
Client Message
  -> Remote Agent 判断处理方式
     -> 直接回答：Message
     -> 可追踪工作：Task
```

Task 表示 Remote Agent 对一项工作的执行承诺，不是 Client 在远端任务表中直接插入的记录。因此协议没有独立 `CreateTask`。

## 为什么没有通用 UpdateTask

Client 不能把远端 Task 直接改成 `COMPLETED`、替换状态说明或写入 Artifact。它只能：

- 发送后续 Message，补充信息、回应 `INPUT_REQUIRED` 或继续同一 Task；
- 请求取消，但不能保证取消一定成功；
- 查询或订阅 Remote Agent 发布的事实；
- 为 Task 管理 Push 回调配置。

Remote Agent 才知道内部执行是否真正完成，因此由它创建 Task、推进状态并发布 Artifact。这条所有权边界防止跨组织调用方篡改对方的执行和审计事实。

## Send Message 的阻塞模式

`SendMessageConfiguration.returnImmediately` 控制非流式调用何时返回：

| 配置 | 行为 |
|---|---|
| 未设置或 `false` | 默认阻塞到终态，或 `INPUT_REQUIRED` / `AUTH_REQUIRED` 中断态 |
| `true` | Task 创建后立即返回，由 Client 后续 Poll、Subscribe 或等待 Push |

它不影响直接 Message 响应、Streaming 操作和已经配置的 Push 通知。两种模式只决定非流式调用等待到哪个阶段，不改变 Task 的所有权和后续管理方式。

## GetTask 与 ListTasks

`GetTask` 获取指定 Task 的当前状态、Artifacts 和按请求限制的 History，适合轮询、流断开后恢复和收到 Push 后对账。

`ListTasks` 是带授权范围的任务工作台，而不是全局任务枚举接口。规范要求：

- 只返回当前认证 Client 有权访问的 Task；
- 使用 `pageToken` / `nextPageToken` 游标分页；
- 按状态时间从新到旧排序；
- `includeArtifacts` 默认为 `false`，此时应省略 Artifacts 字段；
- 可按 `contextId`、状态和状态更新时间筛选。

`historyLength` 在多个查询操作中保持同一语义：未设置表示 Client 不施加上限、`0` 表示请求省略 History、正整数 N 表示最多返回最近 N 条 Message；Server 仍可以施加更低的自身上限。

## 终态 Task 的边界

`COMPLETED`、`FAILED`、`CANCELED` 和 `REJECTED` 是终态。进入终态后：

- 不能再向该 Task 发送 Message；
- 不能再调用 `SubscribeToTask` 建立更新流；
- Client 应使用 `GetTask` 获取最终快照；
- 新工作应从新的 `SendMessage` 开始。

协议没有通用 `DeleteTask`。Task 保存、归档、保留期和删除属于 Server 的数据治理策略。

## Task 与 Push 配置是两个生命周期

一个 Task 可以对应多项 Push 配置，例如分别通知采购系统和审计系统。删除某项 Push 配置只停止向对应 Callback 发送通知，不取消或删除 Task；创建 Push 配置也不会改变 `returnImmediately` 或 Task 的执行方式。

```text
Task T-100
  |-- Push config A -> procurement callback
  `-- Push config B -> audit callback
```

Push 配置必须持续到 Task 完成或被显式删除；删除操作本身必须幂等。具体交付和恢复见 [Task 更新交付与断线恢复](task-delivery-and-recovery.md)。

## Extended Agent Card 不属于 Task CRUD

`GetExtendedAgentCard` 与 Task 管理共享同一服务操作面，但它解决的是认证后的能力发现。只有公开 Agent Card 声明 `extendedAgentCard` 能力时才可调用；返回内容可以按 Client 身份增加 Skills 或配置。它不创建、更新或查询 Task。

## 完整业务流程

```text
SendMessage(contract)
  <- Task T-100: WORKING

SubscribeToTask(T-100)
  <- current Task snapshot
  <- INPUT_REQUIRED: missing supplier qualification

SendMessage(taskId=T-100, qualification)
  <- WORKING
  <- Artifact: risk-report.pdf
  <- COMPLETED

GetTask(T-100, historyLength=10)
  <- final authoritative snapshot
```

Message 是 Client 可提交的动作，Task 是 Remote Agent 管理的工作事实，Artifact 是 Remote Agent 交付的正式结果。

## QA / 讨论记录

### Q: 为什么 A2A 不设计成普通 Task CRUD API？

> **状态**: verified | **来源**: official specification / discussion

因为跨 Agent 协作不是双方共同编辑一行数据库记录。Client 提交意图和控制请求，Remote Agent 对实际执行结果负责；限制写权限才能保持状态、Artifact 和审计事实可信。

### Q: CancelTask 是否等于任务一定会进入 CANCELED？

> **状态**: verified | **来源**: official specification

不等于。取消是请求而不是强制写状态；任务可能已经完成、处于不可取消阶段或根本不支持取消。Client 必须根据返回的最新 Task 判断结果。

### Q: 删除 Push 配置是否会删除 Task？

> **状态**: verified | **来源**: official specification

不会。Push 配置只是 Task 更新的通知通道，拥有独立生命周期。

## 相关文档

- [A2A 协议原理与互操作模型](protocol-and-interop.md)
- [从 Message 到 Task 与 Artifact](message-task-artifact.md)
- [Task 更新交付与断线恢复](task-delivery-and-recovery.md)
- [错误、幂等、重试与取消](reliability-errors-and-cancellation.md)
- [认证、授权与任务中授权](authentication-and-authorization.md)
- [协议 Binding 与互操作](protocol-bindings.md)
