# A2A 版本协商与兼容性

> **日期**: 2026-08-26 | **状态**: draft | **证据状态**: verified | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

A2A 版本表示本次通信采用哪一版协议语义，而不是 Agent 软件自身的发布版本。Client 先从 Agent Card 选择兼容接口，再在每个请求中明确携带 `A2A-Version`；Server 必须按请求的 `Major.Minor` 语义处理，不支持时明确返回 `VersionNotSupportedError`，不能静默猜测或降级。

## 三种容易混淆的版本

| 位置 | 含义 | 示例 |
|---|---|---|
| `AgentCard.version` | Agent 产品或服务自身版本 | `2.7.1` |
| `AgentInterface.protocolVersion` | 某个接口暴露的 A2A 版本 | `1.0` |
| `A2A-Version` Service Parameter | 本次请求要求采用的协议版本 | `1.0` |

同一个 `2.7.1` 版本的 Agent 可以同时发布 A2A `0.3` 和 `1.0` 接口。产品升级不等于协议版本必然升级，协议升级也不要求改变 Agent 的业务版本编号。

## 版本格式

A2A 协商只使用规范版本中的 `Major.Minor`：

```text
0.3
1.0
1.1
```

规范自身的 Patch 版本不影响协议兼容性。`1.0.0`、`1.0.1` 和 `1.0.7` 在协议协商中都视为 `1.0`；请求、响应和 Agent Card 不应携带 Patch 版本，Server 也不得用 Patch 差异拒绝请求。规则见 [Versioning](../../../submodules/a2a/docs/specification.md#36-versioning)。

## 发现与选择流程

A2A 的版本协商不是连接建立后的多轮握手，而是 Client 先发现、再选择、最后由 Server 校验：

```text
fetch Agent Card
  -> read supportedInterfaces
  -> filter compatible Binding + protocolVersion
  -> select interface
  -> send A2A-Version on every request
  -> Server applies requested semantics or returns an error
```

Agent Card 的 `supportedInterfaces` 是有序列表，每项将 URL、Binding、租户路由值和协议版本绑定在一起。Client 不能只拿版本号而忽略其所属接口。

## Client 责任

除旧 `0.3` 兼容情形外，Client 必须在每次请求中发送 `A2A-Version`。HTTP 类 Binding 使用 Header 或请求参数，gRPC 使用 metadata。逐请求声明版本可以避免 Server 原地升级后改变旧 Client 的字段默认值、操作和错误解释。

规范规定空版本按 `0.3` 解释，仅用于兼容旧 Client。新 Client 不应依赖这一默认值，否则无法分辨“明确要求 0.3”和“调用代码漏传版本”。

## Server 责任

Server 必须：

- 按请求的 `Major.Minor` 解释所有对象、操作和错误；
- 对不支持的版本返回 `VersionNotSupportedError`；
- 将空版本按 `0.3` 解释；
- 可以为同一传输暴露多个版本，使用相同或不同 URL。

错误在三种标准 Binding 中分别映射为 JSON-RPC `-32009`、gRPC `FAILED_PRECONDITION` 和 HTTP `400 Bad Request`，但语义相同。

## 多版本并存与迁移

```text
supportedInterfaces
  |-- HTTP+JSON / A2A 1.0 / /a2a/v1
  `-- JSON-RPC  / A2A 0.3 / /a2a/v03
```

Server 可以先上线新版接口，通过 `A2A-Version` 流量统计观察旧 Client 使用量，再给出弃用窗口并逐步退役旧版。这样比直接改变一个 Endpoint 的默认语义更可控。

## 自动回退的边界

Binding 或版本回退必须由 Client 策略决定。只依赖基础能力的请求可以考虑回退，但依赖新版字段、安全约束或扩展的调用不应自动回退，否则会出现表面成功、关键语义静默丢失。

```text
required feature available only in 1.0
  -> 1.0 unavailable
  -> fail explicitly
  X do not silently retry as 0.3
```

SDK 应允许 Client 固定最低版本、禁止自动回退，并把“传输故障”和“协议版本不兼容”区分处理。

## 对 Federation Hub 的启发

Hub 不能只存一个 `agent_url`，至少应将 Agent、接口、Binding、协议版本和 tenant 作为一组版本化 Endpoint 记录。路由时应根据调用所需能力选择兼容接口，并保留请求实际使用的版本，便于恢复 Task、审计和升级影响分析。

## QA / 讨论记录

### Q: `AgentCard.version=2.0.0` 是否表示它支持 A2A 2.0？

> **状态**: verified | **来源**: official specification

不表示。Agent 产品版本和 A2A 协议版本是独立维度，必须查看 `supportedInterfaces[].protocolVersion`。

### Q: Server 能否收到 A2A 1.0 请求后自动按 0.3 处理？

> **状态**: verified | **来源**: official specification

不能。Server 不支持请求版本时必须返回 `VersionNotSupportedError`。是否重新选择旧版只能由了解业务需求的 Client 决定。

### Q: 为什么 Patch 版本不参与协商？

> **状态**: verified | **来源**: official specification

Patch 用于不改变线级协议兼容性的规范修订。忽略 Patch 可以避免实现因勘误或文字修订被错误判定为不兼容。

## 相关文档

- [AgentCard：发现、协商与信任边界](agent-card.md)
- [协议 Binding 与互操作](protocol-bindings.md)
- [Extension 扩展与能力协商](extensions-and-negotiation.md)
- [错误、幂等、重试与取消](reliability-errors-and-cancellation.md)
- [A2A 旧版迁移与遗留兼容](migration-and-legacy-compatibility.md)
