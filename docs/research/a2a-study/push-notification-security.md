# A2A Push Notification 安全与 Webhook 信任

> **日期**: 2026-08-26 | **状态**: draft | **证据状态**: verified（协议要求）/ inference（生产加固） | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

Push Notification 将调用方向从 Client 调用 Remote Agent 反转为 Remote Agent 主动访问 Client Callback，因此同时引入 Server 侧 SSRF、Callback 身份验证、Secret 管理、重复投递和重放风险。Push 只是更新通知，Task 快照仍是最终事实源。

## 信任方向为什么变化

普通 A2A 请求中，Client 根据 Agent Card 找到 Remote Agent，并向它提交认证凭证。Push 中，Client 先注册 Callback，随后 Remote Agent 成为 Webhook Caller：

```text
normal request: Client -> Remote Agent
push callback:  Remote Agent -> Client Callback
```

因此原有 Client 到 Server 的 Token、网络策略和授权不能自动复用到反向链路。Push 配置、回调凭证和接收端必须作为单独安全边界设计。

## Remote Agent 的 SSRF 风险

恶意 Client 可以尝试把 Callback 指向本机、云元数据服务或内部管理接口。规范要求 Agent 校验 Webhook URL，并建议拒绝 localhost、link-local 和私有网段，必要时使用 allowlist，见 [Push Notification Security](../../../submodules/a2a/docs/specification.md#132-push-notification-security)。

生产校验应覆盖注册和每次投递，而不只是检查原始字符串：

- 仅允许 HTTPS 和批准的端口；
- 解析域名后检查全部 IP，防止 DNS rebinding；
- 拒绝 loopback、private、link-local 和内部服务网段；
- 限制或禁止重定向，并重新验证重定向目标；
- 设置连接、读取、响应体大小和总执行超时；
- 对 Callback 域名、租户和注册主体建立 allowlist 或策略。

规范建议 Webhook 调用使用合理超时，通常为 10-30 秒。

## Callback 认证

`PushNotificationConfig.authentication` 通过 `scheme` 和 `credentials` 描述 Remote Agent 调用 Callback 时携带的认证信息，结构见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L328)。

```text
Credential A: Client -> Remote Agent
Credential B: Remote Agent -> Callback
```

两个方向的凭证不应默认复用。Client 应为每个 Push 配置使用唯一、单用途、可轮换的 Callback Token；Remote Agent 应加密存储并限制日志暴露。Callback 必须验证认证信息，不能只凭 Task ID 接受请求。

## Callback 接收流程

```text
receive webhook
  -> authenticate caller
  -> validate expected task / tenant / sender
  -> validate payload and size
  -> idempotently persist or enqueue
  -> return HTTP 2xx
  -> asynchronously reconcile Task if needed
```

规范要求接收成功时返回 `2xx`。生产系统最好在事件已经持久化或进入可靠队列后再确认，避免先返回成功、随后本地进程崩溃而永久丢失通知。

## 重试、重复和乱序

Remote Agent 应对失败回调采用指数退避，并可以在连续失败达到上限后停止投递。这里重试的是通知交付，不是重新执行 Task。

A2A 不承诺 exactly-once，Callback 可能重复、延迟、乱序，也可能与 Polling 和 Streaming 同时到达。接收方应：

- 按 `(remoteAgent, taskId, artifactId, state/timestamp)` 等稳定事实幂等合并；
- 不因较晚到达的旧状态覆盖已经确认的新状态；
- 将 Push 当作触发 `GetTask` 对账的信号；
- 对异常频率实施 rate limit 和告警。

核心协议没有通用 Webhook Event ID、请求签名时间戳和统一防重放窗口。更强的防重放需要 Gateway 签名、一次性凭证或协议 Extension。

## Push 不是事实存储

Callback Payload 使用与 Streaming 相同的 `StreamResponse` 语义，可以携带状态或 Artifact 更新，但通知可能丢失。Hub 的正确恢复策略是保存远端 Task 绑定，并使用 `GetTask` 获取当前权威快照，而不是要求 Webhook 历史完整重放。

> **精髓：Push 负责及时提醒，GetTask 负责最终对账，Task 负责保存远端事实。**

## QA / 讨论记录

### Q: Push 配置中的 Token 能否复用 Client 调用 Remote Agent 的 Token？

> **状态**: verified / inference | **来源**: protocol direction / security reasoning

不应默认复用。两者受众和调用方向相反，复用会扩大泄露后的权限范围。应使用面向 Callback 的独立凭证。

### Q: Callback 收到 `COMPLETED` 后能否直接关闭本地任务？

> **状态**: verified / inference | **来源**: protocol / reliability reasoning

可以把它作为强更新信号，但关键流程仍应通过 `GetTask` 取得最终 Artifacts 和权威快照后再完成本地对账。

### Q: Remote Agent 重试 Webhook 是否会重新运行 Task？

> **状态**: verified | **来源**: official specification

不会。Webhook 重试只重新投递已有 Task 更新，不应触发业务任务重新执行。

## 相关文档

- [Task 更新交付与断线恢复](task-delivery-and-recovery.md)
- [认证、授权与任务中授权](authentication-and-authorization.md)
- [错误、幂等、重试与取消](reliability-errors-and-cancellation.md)
- [A2A 操作集与 Task 管理边界](operations-and-task-management.md)
