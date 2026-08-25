# A2A 自定义 Binding 设计

> **日期**: 2026-08-26 | **状态**: draft | **证据状态**: verified | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

自定义 Binding 可以把 A2A 映射到 WebSocket、消息总线等新传输，但不能只转发部分 JSON 就声称兼容。它必须保持核心操作、数据模型、错误、安全和时序语义，并用版本化 URI 在 Agent Card 中唯一声明。

## Binding 与 Extension 的边界

```text
Custom Binding
  = 改变 A2A 数据怎样传输

Extension
  = 增加核心协议之外的业务或领域语义
```

例如通过 Kafka 交付 Task 事件属于自定义 Binding；给 Artifact 增加医疗证据等级属于 Extension。把业务字段写进 Binding 会使相同 A2A 语义无法通过其他 Binding 表达。

## 完整映射责任

规范要求自定义 Binding：

1. 映射全部核心操作，而不是只实现 `SendMessage`；
2. 提供与 Protobuf 模型功能等价的数据结构；
3. 保持 Task、Message、Artifact 和操作语义；
4. 完整记录请求、响应、错误和限制。

如果具体传输或 Agent 不支持 Streaming 等可选能力，必须在 Agent Card 中明确声明限制，并按规范返回不支持错误，不能让 Client 依靠连接超时推断。

## 数据类型映射

Binding 文档必须说明：

- 每个 Protobuf Message 如何表示；
- Timestamp 如何序列化；
- Binary 如何编码；
- Enum 使用字符串还是数值；
- Optional、Required 和 `oneof` 怎样保留；
- 消息大小、字符集和字段限制。

内部传输格式可以不是 JSON，但解码后必须得到等价的 A2A 对象。

## Service Parameters

自定义 Binding 必须说明 `A2A-Version`、`A2A-Extensions` 等 Service Parameters 放在哪里。没有 Header 的消息系统可以定义专用 envelope metadata，但要明确键大小写、字符编码、长度限制、保留字段和缺失时行为。

## 错误映射

每种标准 A2A 错误都要映射到传输原生错误或结构化 Error Envelope，同时保留具体错误身份和 detail。不能只用一个布尔值或字符串表示所有失败，否则 Client 无法区分重试、版本不兼容和 Task 业务失败。

## Streaming 与断线

如果 Binding 支持 Streaming，需要定义：

- Stream 如何建立；
- Task 快照和更新事件的顺序；
- 背压和消息大小；
- 中断后是否以及怎样恢复；
- Stream 完成与 Task 终态怎样表达；
- 重复或重新投递事件怎样处理。

WebSocket、Kafka 和长轮询可以有完全不同的传输行为，但外部观察到的 Task 生命周期必须等价。

## 认证与安全

Binding 必须支持 Agent Card 声明的认证方案，说明 Credential 放在哪里、Challenge 怎样返回，并遵守传输自身的加密、身份验证和授权要求。不能因为传输运行在企业内网就省略 Task 级授权。

## Agent Card 声明与版本

自定义 Binding 应使用全局唯一 URI：

```json
{
  "url": "wss://agent.example.com/a2a",
  "protocolBinding": "https://example.com/bindings/websocket/v1",
  "protocolVersion": "1.0"
}
```

破坏性变更必须更换 URI，例如从 `/v1` 变为 `/v2`。`protocolVersion` 表示承载的 A2A 版本，Binding URI 中的版本表示自定义映射自身版本，二者不能混淆。

## 互操作测试

自定义 Binding 应与至少一种标准 Binding 对照测试同一 Task：相同输入应产生语义等价的状态、Artifacts 和错误；还要覆盖大型 Payload、长期任务、断线、取消和安全失败。要求见 [Custom Binding Guidelines](../../../submodules/a2a/docs/specification.md#12-custom-binding-guidelines)。

## QA / 讨论记录

### Q: 把 A2A JSON 放进 Kafka Message 是否已经完成 Kafka Binding？

> **状态**: verified | **来源**: official specification

没有。还需定义操作寻址、请求响应关联、Service Parameters、错误、Streaming/订阅、认证和事件顺序。

### Q: 自定义 Binding 能否新增 Task 状态？

> **状态**: verified | **来源**: protocol boundary

不能以 Binding 私自改变核心状态模型。领域状态应通过 Extension 表达；核心状态变化需要新的 A2A 协议版本。

### Q: Binding URI 版本和 `protocolVersion` 是否相同？

> **状态**: verified | **来源**: official specification

不同。前者标识自定义传输映射版本，后者标识该接口使用的 A2A 语义版本。

## 相关文档

- [协议 Binding 与互操作](protocol-bindings.md)
- [A2A 线级数据契约与一致性测试](wire-contract-and-conformance.md)
- [Extension 扩展与能力协商](extensions-and-negotiation.md)
- [A2A 版本协商与兼容性](versioning-and-compatibility.md)
