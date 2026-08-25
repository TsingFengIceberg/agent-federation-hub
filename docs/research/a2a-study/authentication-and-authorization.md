# 认证、授权与任务中授权

> **日期**: 2026-08-25 | **状态**: draft | **证据状态**: verified（协议要求）/ inference（委托令牌实现） | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

Authentication 证明调用者是谁，Authorization 判断该身份能否执行当前操作，`AUTH_REQUIRED` 则表示一个已经受理的 Task 在执行中还需要新的授权或人工批准。三者发生在不同时间，不能都归为“权限错误”。

## 用办公楼建立直觉

```text
Authentication = 门卫确认你是谁
Authorization  = 确认你能进入哪个房间、操作哪些资料
AUTH_REQUIRED  = 办事过程中发现下一步还需要额外签字
```

对应的外部表现：

| 情况 | 表现 | Task 是否通常已建立 |
|---|---|---|
| 凭证缺失或失效 | 401 / UNAUTHENTICATED / Binding 错误 | 没有 |
| 身份有效但无权操作 | 403 / PERMISSION_DENIED / Binding 错误 | 没有或不能访问目标 Task |
| 执行到一半需要新批准 | Task `AUTH_REQUIRED` | 已建立并暂停 |

## TLS 与 Server 身份

在 Client 提交自身凭证之前，先要确认连接的确是目标 Server。生产 HTTP Binding 必须使用 HTTPS，gRPC 必须使用 TLS；Client 应验证 Server 证书和信任链。否则 Client 可能把 Token 和业务数据交给伪造 Agent。要求见 [specification.md](../../../submodules/a2a/docs/specification.md#71-protocol-security)。

## Agent Card 如何声明认证

Agent Card 分两部分：

```text
securitySchemes
  = 支持的锁和钥匙种类及取得方式

securityRequirements
  = 实际访问 Agent 所要求的方案和 Scope
```

当前标准 Security Scheme 包括 API Key、HTTP Basic/Bearer、OAuth 2.0、OpenID Connect 和 Mutual TLS，模型见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L501)。具体 Skill 还可以声明自己的 `securityRequirements`，覆盖或细化全局访问要求。

Agent Card 只能描述取得和传输凭证的方法，不能把真实 Token、密码或私钥直接发布在卡片里。

## Client 认证流程

规范流程为：

```text
fetch Agent Card
  -> discover security requirements
  -> acquire credentials out of band
  -> attach credentials to every A2A request
  -> Server authenticates caller
```

“带外取得”表示 OAuth Authorization Server、密钥系统、企业 SSO 或其他安全流程负责签发凭证，而不是让 A2A Message 自己充当登录协议。不同 Binding 使用 HTTP Header 或 gRPC Metadata 传输凭证。

## Server 授权责任

认证成功后，Server 仍要按自身策略检查：

- 调用者是否可以使用目标 Skill；
- 是否允许发起、读取、补充或取消该 Task；
- 是否可以取得 Artifact；
- 是否拥有要求的 OAuth Scope；
- 是否属于正确用户、项目、组织或租户；
- 是否允许配置 Task Push Callback。

每个 A2A 操作都必须执行授权检查，List Tasks 只能返回调用者可见的任务，Get/Cancel/Subscribe 也必须验证 Task 归属。授权模型由 Agent 定义，协议只规定必须执行边界检查，见 [specification.md](../../../submodules/a2a/docs/specification.md#131-data-access-and-authorization-scoping)。

为了防止枚举攻击，无权访问的 Task 可以和不存在的 Task 一样返回 `TaskNotFoundError`；Server 不应先查询并暴露资源存在性，再进行授权。

## `AUTH_REQUIRED` 的触发

Task 执行中可能遇到此前无法预知的授权点，例如：

- 需要用户 OAuth Token 调用第三方 API；
- 付款、删除或发送外部邮件前需要人工批准；
- 需要访问新的数据域或更高权限 Skill；
- 下游 Agent 要求额外授权。

Remote Agent 要把任务转为 `AUTH_REQUIRED`，必须：

1. 使用 Task 跟踪当前工作；
2. 把 TaskState 设为 `AUTH_REQUIRED`；
3. 通常在 TaskStatus Message 中解释需要什么授权；
4. 安排带外的安全凭证接收方式，除非已通过扩展约定安全的带内方式。

要求见 [specification.md](../../../submodules/a2a/docs/specification.md#761-in-task-authorization-agent-responsibilities)。

## Client 如何响应 `AUTH_REQUIRED`

Client 可以：

- 给用户展示授权或审批请求；
- 联系另一个 Agent、身份服务或人工审批人；
- 向原 Task 发送 Message，协商、纠正或拒绝要求；
- 通过约定的 HTTPS/OAuth/扩展通道把凭证直接交给需要它的 Agent。

凭证带外到达后，Remote Agent 可以直接恢复 Task，不要求 Client 再发一条“我授权了”的普通 Message。Client 应继续 Streaming、Push 或 Polling，以观察 Agent 何时真正恢复。

## 授权链

如果 Client 自己也是另一个上游 Task 的 Remote Agent，可以把授权需求继续向上游传播：

```text
User / Enterprise Client
  <- AUTH_REQUIRED -- Coordinator Agent
  <- AUTH_REQUIRED -- Purchasing Agent
  <- needs OAuth --- Payment Agent
```

A2A允许形成这种 Task 授权链，但不要求把同一 Token 沿链条逐站转发。普通 Message 中传递凭证会让每个中间 Agent 看见它，风险很高。

更安全的目标是：

- 凭证直接发给最终使用者；
- Token 的 audience 绑定目标 Agent/API；
- Scope 只覆盖当前动作；
- 有短有效期并可撤销；
- 中间 Agent 只得到“授权已完成”的状态，不得到可复用秘密。

这些令牌交换、委托和撤销细节不由 A2A 核心协议定义，需要身份系统或协议扩展完成。

## `AUTH_REQUIRED` 不等于已经授权

状态只表示“还缺授权”，本身不能作为任何操作的批准证明。A2A 也不定义随后得到的 Credential：

- 授权了什么动作；
- 对哪个资源生效；
- 有效多久；
- 能否复用到后续 Message；
- 如何撤销。

Remote Agent 必须根据凭证签发者、自己的实现或扩展契约验证 Scope。一次 Task 中取得的凭证不得默认授权后续所有消息，规范边界见 [specification.md](../../../submodules/a2a/docs/specification.md#764-in-task-authorization-scope)。

## Push 的反向认证

普通 A2A 调用是 Client 向 Remote Agent 证明身份；Push 时方向反过来，Remote Agent 调用 Client 的 Webhook。Push 配置可以描述回调认证信息，Remote Agent 必须按配置携带凭证，Client 必须验证回调真实性并幂等处理可能重复的通知。

这套 Callback credential 与 Client 调用 Remote Agent 使用的凭证不是同一方向，也不应默认复用。

## Extended Agent Card

公开 Agent Card 可以只披露基础能力和认证入口；`GetExtendedAgentCard` 必须认证后调用，Server 可以按调用者身份或权限返回不同的额外 Skills 和配置。认证只解决“允许看什么”，不意味着 Client 自动获得调用每个私有 Skill 的授权。

## A2A 没有定义什么

A2A定义认证方案的声明、凭证传输位置、Server/Client 责任和 `AUTH_REQUIRED` 状态，但不定义：

- 用户/Agent 身份目录；
- Token 签发基础设施；
- 跨组织信任联盟；
- OAuth Token Exchange 的统一流程；
- 委托链的 Scope、audience 和撤销模型；
- 人工审批系统；
- 业务角色和权限规则。

这些必须由企业身份系统、Agent 实现或标准扩展提供。

## QA / 讨论记录

### Q: 401、403 和 `AUTH_REQUIRED` 有什么本质区别？

> **状态**: verified | **来源**: official specification

401 表示调用者尚未通过认证；403 表示身份有效但无权执行当前请求；`AUTH_REQUIRED` 表示 Task 已经受理，执行到某一步还需要额外授权。

### Q: 能否直接把 OAuth Token 放进 A2A Message？

> **状态**: verified | **来源**: official security guidance

默认不应这样做。规范建议通过带外安全通道把凭证直接交给需要它的 Agent。只有经过带外协商或协议扩展定义了安全的带内机制时才考虑 Message 传递，并应做目标绑定和加密。

### Q: Agent Card 声明 OAuth 是否意味着平台已经帮 Client 登录？

> **状态**: verified | **来源**: protocol boundary

不意味着。Agent Card 只描述认证要求与端点；Client 仍需通过相应身份系统带外取得凭证。

### Q: `AUTH_REQUIRED` 后收到一个 Token 就一定可以继续吗？

> **状态**: verified | **来源**: official specification

不一定。Remote Agent 必须验证 Token 的签发者、目标、Scope、有效期和当前操作；状态变化和 Token 的存在都不能替代授权校验。

## 相关文档

- [AgentCard：发现、协商与信任边界](agent-card.md)
- [错误、幂等、重试与取消](reliability-errors-and-cancellation.md)
- [Task 更新交付与断线恢复](task-delivery-and-recovery.md)
- [Push Notification 安全与 Webhook 信任](push-notification-security.md)
