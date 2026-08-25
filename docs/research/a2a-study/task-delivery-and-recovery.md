# Task 更新交付与断线恢复

> **日期**: 2026-08-25 | **状态**: draft | **证据状态**: verified（协议语义）/ inference（Hub Reconciler） | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

Task 是远程工作的事实源；Polling、Streaming 和 Push Notification 是观察同一个 Task 的三种更新通道。网络连接和通知都可能中断、重复或遗漏，生产平台必须保存远端任务关联，并通过 Task 快照重新对账，而不能把某一条连接当作任务本身。

## 用医院病历建立直觉

把 Task 想成医院保存的病历，把三种更新机制想成了解诊疗进度的方式：

- Polling：每隔一段时间询问护士；
- Streaming：持续观看实时诊疗屏幕；
- Push：留下电话号码，有变化时由医院通知。

病历存在于医院系统，不存在于显示屏、电话或某次询问中。对应到 A2A：

```text
Task      = 持久工作事实
Polling   = 主动获取最新快照
Streaming = 实时观察通道
Push      = 断开请求后的异步通知通道
```

## Polling：反复取得最新 Task

Client 保存 Remote Agent 身份和 `taskId`，定期调用 `GetTask`：

```text
GetTask(task-123) -> WORKING
GetTask(task-123) -> WORKING
GetTask(task-123) -> COMPLETED + Artifacts
```

Polling 不依赖长连接，适合简单客户端、受限网络和平台重启后的恢复。代价是查询延迟与服务压力，并且可能观察不到两个查询间的短暂中间状态。

实现时需要：

- 根据任务类型设置基础间隔和最大间隔；
- 对持续 `WORKING` 使用退避和抖动，避免大量任务同一时刻查询；
- 遇到终态立即停止；
- 区分暂时网络错误、Task 不存在和认证失效；
- 为超过业务 SLA 的任务触发告警或补偿，而不是擅自把远端 Task 改成失败。

`GetTask` 返回当前状态、Artifacts 和按请求限制的 history，协议操作见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L44)。

## Send Streaming Message：从创建任务开始观察

`SendStreamingMessage` 在发送初始 Message 的同时建立更新流。协议允许两种流形态：

```text
Message-only stream
  Message -> close

Task lifecycle stream
  Task snapshot
  -> TaskStatusUpdateEvent / TaskArtifactUpdateEvent ...
  -> terminal state -> close
```

Message-only stream 适合无需 Task 追踪的直接回答；Task lifecycle stream 的首个对象是 Task，后续对象才是状态或 Artifact 增量。规范说明见 [specification.md](../../../submodules/a2a/docs/specification.md#312-send-streaming-message)。

A2A Streaming 不等同于 LLM Token Streaming。它可以承载文本增量，但协议核心关注的是 Task 状态和 Artifact 更新。Remote Agent 即使内部模型不流式输出，也可以发布“解析完成”“进入人工复核”等工作级事件。

## Subscribe To Task：为已有 Task 建立更新流

Client 可以先用普通 Send Message 和 `returnImmediately=true` 创建 Task，随后调用 `SubscribeToTask(taskId)`：

```text
SendMessage
  <- Task: WORKING

SubscribeToTask(taskId)
  <- current Task snapshot
  <- future status/artifact events
```

订阅流必须先返回订阅时的完整 Task 快照，再交付后续事件。这避免 `GetTask` 与 Subscribe 之间发生变化而产生观察缺口，见 [specification.md](../../../submodules/a2a/docs/specification.md#316-subscribe-to-task)。

已经进入 `COMPLETED`、`FAILED`、`CANCELED` 或 `REJECTED` 的终态 Task 不再接受 Subscribe；Client 应改用 `GetTask` 获取最终快照。`INPUT_REQUIRED` 和 `AUTH_REQUIRED` 是中断态而非终态，仍可继续观察和恢复。

## 同一 Task 的多个 Streaming 连接

A2A 允许同一个 Task 同时有多个活动流。协议要求：

- 更新广播给该 Task 的全部活动流；
- 每个流看到相同事件和相同顺序；
- 关闭一个流不影响其他流；
- Task 生命周期独立于任何一条连接。

因此关闭浏览器、SSE 或 gRPC Stream 不应自动取消 Task。取消是一项明确的协议操作，而不是网络副作用。

## Push Notification：Client 离线后的回调

Client 可以为 Task 注册回调配置。Task 变化时，Remote Agent 向回调 URL 发 HTTP POST，Payload 使用与 Streaming 相同的 `StreamResponse` 包装，可包含状态更新或 Artifact 更新。

```text
Client -> CreateTaskPushNotificationConfig

Client disconnects

Remote Agent -> callback: TaskStatusUpdateEvent
Remote Agent -> callback: TaskArtifactUpdateEvent
```

Push 配置应持续到 Task 完成或被显式删除；只有 Agent Card 声明支持 Push 时才能使用。协议操作见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L89)，Payload 语义见 [specification.md](../../../submodules/a2a/docs/specification.md#43-push-notification-objects)。

跨组织 Push 至少需要治理：

- 回调 URL 的域名、网络和 SSRF 校验；
- TLS 与 Remote Agent 到 Callback 的认证；
- callback token 或其他凭证的 Secret 管理；
- Task、租户和预期发送方校验；
- 重复、乱序、重试、超时和死信处理；
- 回调内容的大小与媒体限制；
- 审计和凭证撤销。

## 三种机制如何选择

| 机制 | 最适合 | 优点 | 主要代价 |
|---|---|---|---|
| Polling | 简单客户端、重启恢复、网络受限环境 | 实现简单、随时取得当前事实 | 延迟、查询压力、可能错过中间事件 |
| Streaming | 秒级进度、交互式 UI、Artifact 增量 | 实时、同连接交付多个事件 | 长连接、断线和背压治理 |
| Push | 数小时或数天任务、Client 可能离线 | 无需保持连接、跨系统回调 | 公网入口、安全、重试和去重复杂 |

生产 Hub 通常三者同时支持：前台使用 Streaming，后台配置 Push，Reconciler 最终仍通过 Polling/GetTask 对账。

## 断线后恢复的正确流程

```text
Stream disconnects
  -> load local binding(remoteAgentId, remoteTaskId, contextId)
  -> GetTask for current snapshot
  -> merge status and complete Artifacts
  -> if non-terminal, SubscribeToTask again
  -> continue periodic reconciliation
```

恢复的是对 Task 的观察，不是原来的 TCP、SSE、WebSocket 或 gRPC 连接。

协议明确提醒：Client 断线后可能无法补回所有状态更新 Message，关键业务事实不能只存在于短暂通知中。应落到 Task 当前状态、可重新获取的 Artifact、Remote Agent 的持久业务状态和 Hub 自己的审计记录，见 [specification.md](../../../submodules/a2a/docs/specification.md#37-messages-and-artifacts)。

## Task Reconciler 的平台责任

> **状态**: inference

Federation Hub 需要独立 Reconciler，而不是让 Web 请求或前端连接拥有任务生命周期：

```text
TaskReconciler
├── scan non-terminal local bindings
├── refresh remote Task snapshots
├── merge status and Artifact state
├── detect stalled / missing / unauthorized tasks
├── restore subscriptions where useful
├── schedule retry with backoff
├── emit local normalized events
└── close local work only after reconciliation
```

本地状态不能简单覆盖远端状态。需要保存最后观察时间、来源、远端时间戳和终态，并为重复 Push、重复 Polling 结果和 Stream 重连提供幂等合并。

## 容易犯的错误

- SSE 断开就把远端 Task 标记为失败；
- 前端页面关闭就停止所有后台任务管理；
- 只支持 Streaming，不保存 `remoteTaskId`；
- 把 Push 当成准确且只到达一次的事件总线；
- 重连后直接订阅，不先取得当前 Task 快照；
- 把每条状态说明 Message 当作必须完整重放的业务事实；
- 把 Transport 重试与业务 Task 重试混成一件事；
- 没有按 `(remoteAgent, taskId, artifactId)` 聚合 Artifact。

## QA / 讨论记录

### Q: Streaming 断开是否意味着 Task 被取消？

> **状态**: verified | **来源**: official specification

不意味着。Task 生命周期独立于单个流；取消必须通过显式 Cancel Task 操作表达。

### Q: Subscribe 前为什么还要先返回一个 Task 快照？

> **状态**: verified | **来源**: official specification

它提供订阅建立时的当前事实，避免 `GetTask` 与 Subscribe 两次操作之间的状态变化形成缺口。

### Q: 有了 Push 是否还需要 Polling？

> **状态**: verified / inference | **来源**: protocol / architecture reasoning

协议允许单独使用 Push，但生产 Hub 仍应保留按 Task 查询与对账能力，因为回调可能延迟、重复、失败或在平台停机期间无法处理。

### Q: A2A Streaming 是否就是模型逐 Token 输出？

> **状态**: verified | **来源**: official specification

不是。A2A Stream 交付 Message、TaskStatusUpdateEvent 和 TaskArtifactUpdateEvent；模型 Token 增量只是可能被封装在 Message 或 Artifact Part 中的一种实现。

## 相关文档

- [从 Message 到 Task 与 Artifact](message-task-artifact.md)
- [错误、幂等、重试与取消](reliability-errors-and-cancellation.md)
- [认证、授权与任务中授权](authentication-and-authorization.md)
- [A2A 操作集与 Task 管理边界](operations-and-task-management.md)
- [协议 Binding 与互操作](protocol-bindings.md)
- [Push Notification 安全与 Webhook 信任](push-notification-security.md)
- [A2A 协议原理与互操作模型](protocol-and-interop.md)
- [A2A 即插即用与通用联邦平台](plug-and-play-federation.md)
