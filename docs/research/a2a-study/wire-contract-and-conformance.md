# A2A 线级数据契约与一致性测试

> **日期**: 2026-08-26 | **状态**: draft | **证据状态**: verified（线级规则）/ to-verify（本地 TCK 实测） | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

互操作不只是双方都能解析 JSON，而是 Schema、业务语义和时序行为都一致。A2A 以 Protobuf 数据模型为规范基准，所有 Binding 必须对字段、枚举、时间、二进制、错误、状态和 Streaming 顺序提供功能等价表示，并通过跨实现测试验证。

## 三层互操作

| 层次 | 需要一致的内容 | 典型错误 |
|---|---|---|
| Schema | 字段名、类型、required、枚举、时间、Base64 | `context_id` 与 `contextId` 混用 |
| Semantic | 操作、Task 状态、错误、权限和默认值 | 把 `AUTH_REQUIRED` 当 401 |
| Temporal | Stream 首事件、事件顺序、终态关闭和恢复 | Artifact 事件先于初始 Task |

只有三层都满足，Client 才能在替换语言、SDK 或 Binding 后保持行为不变。

## JSON 表示规则

规范要求所有 JSON Binding：

- 字段名使用 `camelCase`，即 `contextId`、`protocolVersion`；
- 枚举使用 Protobuf 中定义的字符串名，如 `TASK_STATE_WORKING`；
- `google.protobuf.Timestamp` 使用 UTC ISO 8601 和 `Z` 后缀；
- `bytes` 在 JSON 中按 ProtoJSON 使用 Base64；
- `oneof` 由当前成员名表达，不能同时出现多个内容成员。

规则见 [JSON Field Naming Convention](../../../submodules/a2a/docs/specification.md#55-json-field-naming-convention) 和 [Data Type Conventions](../../../submodules/a2a/docs/specification.md#56-data-type-conventions)。

## Required、Optional 与 Presence

带 `REQUIRED` annotation 的字段必须出现，Required 数组至少有一个元素。`optional` 用于区分字段未设置和显式设置为默认值，这会影响协议默认行为和 Agent Card 签名。

实现应忽略未识别字段，以允许新版本增加可选信息；但这不表示可以忽略未知的必需业务语义。破坏性必需变化需要新的协议版本或 Extension URI，而不是依赖旧 Client 猜测。

## 错误等价也是契约

不同 A2A 错误可能共享同一个 HTTP 或 gRPC 大类状态。Binding 必须保留可识别的错误类型和 detail，例如 `ContentTypeNotSupportedError`、`VersionNotSupportedError` 和 `ExtensionSupportRequiredError`，不能全部压缩成 `bad request`。

同样要区分：

- Transport / Binding 失败；
- A2A 操作失败；
- Task 进入 `FAILED`；
- Task 进入可恢复的 `INPUT_REQUIRED` 或 `AUTH_REQUIRED`。

## 时序一致性

Streaming 测试不能只检查“最终收到了完成状态”，还要验证：

- Message-only Stream 恰好返回一个 Message 后关闭；
- Task Stream 首个对象是完整 Task；
- Subscribe 首个对象是订阅时 Task 快照；
- 同一 Task 的多个连接观察到相同顺序；
- Stream 在终态结束，连接断开不自动取消 Task；
- Artifact chunk 按相同 `artifactId` 和 append 语义聚合。

这些行为是协议契约，不是 SDK 的 UI 选择。

## 一致性测试矩阵

```text
Binding: JSON-RPC / gRPC / HTTP+JSON
Version: supported / unsupported / fallback disabled
Response: direct Message / Task / interrupted / terminal
Delivery: polling / streaming / push / reconnect
Input: text / raw / url / data / unsupported media
Security: unauthenticated / unauthorized / authorized
Errors: every standard A2A-specific error
```

还应覆盖重复请求、取消竞态、终态限制、History 长度、ListTasks 授权分页、Extension 必需性和未知字段。

## Inspector 与 TCK

官方 Roadmap 将 A2A Inspector 和 A2A Protocol Technology Compatibility Kit（TCK）定位为实现验证工具，见 [roadmap.md](../../../submodules/a2a/docs/roadmap.md)。Inspector 适合人工查看 Agent Card、请求和事件；TCK 适合自动检查规范行为。

本仓库尚未把 Inspector 或 TCK 作为 submodule 接入，也没有对本地实现运行测试，因此此处只记录测试目标，实测状态为 `to-verify`。

> **精髓：单元测试证明一个实现内部自洽，TCK 与跨 SDK 测试才验证它能否和陌生实现互操作。**

## QA / 讨论记录

### Q: 两个实现最终都返回 COMPLETED，是否就算兼容？

> **状态**: verified | **来源**: protocol requirements

不算。字段、错误、权限、初始响应和事件顺序也必须符合规范，否则 Client 在异常、重连或跨 Binding 时仍会失败。

### Q: 为什么要忽略未知字段？

> **状态**: verified | **来源**: official specification

它允许新版本增加旧 Client 不关心的可选字段，提供向前兼容；这不允许 Server 偷偷改变旧字段语义。

### Q: 通过官方 SDK 的类型检查是否等于通过 TCK？

> **状态**: verified / to-verify | **来源**: protocol boundary / tooling roadmap

不等于。生成类型主要覆盖 Schema，TCK 还需要核验操作、错误和时序行为。

## 相关文档

- [协议 Binding 与互操作](protocol-bindings.md)
- [A2A 版本协商与兼容性](versioning-and-compatibility.md)
- [Extension 扩展与能力协商](extensions-and-negotiation.md)
- [Agent Card 签名、规范化与缓存](agent-card-signing-and-caching.md)
