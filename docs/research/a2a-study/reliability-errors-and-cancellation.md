# 错误、幂等、重试与取消

> **日期**: 2026-08-25 | **状态**: draft | **证据状态**: verified（协议保证）/ inference（Client 恢复策略） | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

A2A 区分请求到达 Task 之前的传输/协议错误和 Task 建立后的业务终态；读取与取消具有幂等语义，但 Send Message 只“可能”幂等。`messageId` 提供重复检测身份，不保证所有 Agent 都会持久化去重，因此模糊超时不能无条件重发。

## 四类失败位置

| 位置 | 示例 | Task 是否已经创建 |
|---|---|---|
| 传输失败 | DNS、TLS、连接中断、请求超时 | 不确定 |
| 协议请求错误 | 参数、媒体类型、版本或操作不支持 | 通常没有 |
| 认证/授权错误 | 凭证缺失、失效或权限不足 | 通常没有 |
| Task 执行结果 | `FAILED`、`REJECTED`、`CANCELED` | 已经创建 |

协议错误说明请求不能按当前形式受理；Task `FAILED` 说明工作已经受理，但执行没有成功。Client 不应把二者都简化成一个“调用失败”布尔值。

## 为什么超时最危险

```text
Client -- SendMessage --> Remote Agent
                   X 响应途中断线
```

Client 无法从超时判断：

- 请求是否离开本机；
- Server 是否收到并验证请求；
- 是否已经创建 Task；
- Task 是否仍在执行或已经完成；
- 只有响应还是连同请求一起丢失。

因此 Transport timeout 表示结果未知，不等同于 Task `FAILED`。

## `messageId` 与幂等

`messageId` 由 Message 创建者生成，是一次协议 Message 的稳定身份。Client 重试同一逻辑发送时不应生成新的 ID，否则 Remote Agent 无法识别其为重复请求。

但当前规范只规定：

- Get Task、List Tasks、Get Extended Agent Card 天然幂等；
- Send Message **可能**幂等，Agent 可以使用 `messageId` 检测重复；
- Cancel Task 幂等，多次取消具有相同效果；若已取消 Task 被清理，重复取消可以返回 `TaskNotFoundError`。

精确语义见 [specification.md](../../../submodules/a2a/docs/specification.md#331-idempotency)。这意味着：

```text
稳定 messageId
  = 提供去重依据
  != 协议保证 exactly-once
```

Remote Agent 仍需持久化 `(caller identity, messageId)`、处理结果或 Task 关联，并定义保留期与并发冲突，才能真正去重。

## 重试判断

| 情况 | 是否自动重试 | 原因 |
|---|---|---|
| Get/List 临时网络失败 | 可以，带退避 | 读取天然幂等 |
| Cancel 临时网络失败 | 可以 | Cancel 协议幂等，但仍需读取最终 Task |
| Send 在连接前明确失败 | 通常可以 | 请求可确认没有到达 |
| Send 在响应前超时 | 不应无条件重试 | 可能已经创建 Task |
| 401 且 Token 可刷新 | 刷新后重试 | 需要先改变凭证状态 |
| 403 | 通常不重试 | 相同身份和请求不会获得新权限 |
| Validation / Unsupported | 修复请求后再发 | 原请求永久不合法 |
| 429/503 或系统临时错误 | 按 Retry-After/退避 | Server 可能暂时不可用 |
| Task `FAILED` | 不是传输重试 | 是否重做是新的业务决策 |

对模糊 Send timeout，Client 应先使用已知 `taskId` 查询；如果只知道 `contextId`，可以 List Tasks 并核对 history/关联；只有确认 Remote Agent 的去重契约后，才用原 `messageId` 重发。

## 推荐的 Client Outbox

> **状态**: inference

发送前先持久化：

```text
OutboundMessage
├── remote_agent_identity
├── selected_interface / protocol_version
├── message_id
├── context_id / task_id
├── request_digest
├── credential identity reference
├── send attempts and timestamps
└── unknown / acknowledged / bound-to-task state
```

它不能让不支持去重的 Server 自动变幂等，但能避免 Client 因进程重启遗失“自己发送过什么”，并为人工/自动对账提供证据。

## 错误类别

规范要求所有 Binding 保留机器错误码、人类可读说明和可选结构化 details，主要类别包括：

- Authentication：缺失或无效凭证；
- Authorization：身份有效但权限不足；
- Validation：参数、Message 或媒体类型非法；
- Resource：Task 不存在或不可访问；
- System：内部错误、临时不可用、下游超时或限流。

Server 不应区分“Task 不存在”和“Task 存在但你无权访问”，以免泄露其他调用者的资源。错误模型见 [specification.md](../../../submodules/a2a/docs/specification.md#332-error-handling)。

## A2A 专用错误

| 错误 | 含义 |
|---|---|
| `TaskNotFoundError` | Task 不存在、不可访问、过期或已清理 |
| `TaskNotCancelableError` | Task 不处于可取消状态 |
| `PushNotificationNotSupportedError` | Agent 未声明 Push 能力 |
| `UnsupportedOperationError` | 操作或当前状态不支持 |
| `ContentTypeNotSupportedError` | Message/Artifact 媒体类型不支持 |
| `InvalidAgentResponseError` | Agent 返回内容不符合当前操作规范 |
| `ExtendedAgentCardNotConfiguredError` | 请求扩展卡片但 Server 未配置 |
| `ExtensionSupportRequiredError` | Client 未声明支持 Server 要求的必需扩展 |
| `VersionNotSupportedError` | 请求的 A2A 协议版本不受支持 |

不同 Binding 会把相同错误语义映射到 JSON-RPC code、gRPC Status 或 HTTP Status；Client 应判断 A2A 错误类型，不能只看 HTTP 400/500。

## Cancel 是请求，不是强制删除

Cancel Task 要求 Server 尝试取消正在进行的 Task，并返回更新后的 Task。它不是：

- 删除 Task；
- 回滚外部副作用；
- 保证瞬间停止；
- Client 单方面把状态改为 `CANCELED`。

典型竞态：

```text
Client: CancelTask
            ||
Server: Task completes
```

如果 Task 已经完成、失败、取消或拒绝，Server 可以返回 `TaskNotCancelableError`。不存在或无权访问则返回 `TaskNotFoundError`。Client 必须以 Cancel 返回值或后续 Get Task 的事实为准，不能先在本地宣告取消成功。操作语义见 [specification.md](../../../submodules/a2a/docs/specification.md#315-cancel-task)。

## 取消与业务补偿

Task 进入 `CANCELED` 只表示 Remote Agent 停止继续处理，不证明此前副作用已回滚。例如采购 Agent 可能已经发送邮件或预留库存。退款、撤销订单和删除文件属于业务补偿，需要 Remote Agent 自己的 Skill、Workflow 或扩展契约，A2A 核心状态机不定义通用事务回滚。

## QA / 讨论记录

### Q: 相同 `messageId` 重发是否一定只创建一个 Task？

> **状态**: verified | **来源**: official specification

不一定。规范只允许 Agent 使用 `messageId` 实现幂等，没有把 Send Message 定义成必然幂等。需要确认具体 Agent 的去重实现和保留期。

### Q: HTTP 200 是否表示 Task 成功？

> **状态**: verified | **来源**: protocol semantics

不表示。HTTP/Binding 成功只说明协议响应成功返回；返回的 Task 仍可能是 `WORKING`、`FAILED`、`REJECTED` 等状态。JSON-RPC 还可能用 HTTP 200 承载 JSON-RPC error。

### Q: Task `FAILED` 后能否重试同一个 Send Message？

> **状态**: inference | **来源**: protocol boundary

不能把它当普通传输重试。原 Task 已有明确业务结果；是否建立新 Task、继续原 Context 或执行补偿，由业务和 Agent 契约决定。

### Q: Cancel 成功是否自动撤销全部副作用？

> **状态**: verified | **来源**: protocol boundary

不保证。A2A Cancel 管 Task 生命周期，不定义跨业务系统事务和补偿语义。

## 相关文档

- [从 Message 到 Task 与 Artifact](message-task-artifact.md)
- [Task 更新交付与断线恢复](task-delivery-and-recovery.md)
- [认证、授权与任务中授权](authentication-and-authorization.md)
