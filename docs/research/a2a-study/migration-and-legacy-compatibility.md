# A2A 旧版迁移与遗留兼容

> **日期**: 2026-08-26 | **状态**: draft | **证据状态**: verified（规范迁移项）/ inference（Hub 迁移策略） | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

A2A 迁移应先按 Agent Card 和 `A2A-Version` 选择明确协议语义，再在过渡期宽容读取旧格式、严格输出所选版本，并通过指标和期限退役兼容分支。不能把新旧字段混在同一响应中，也不能靠猜测永久维持双重语义。

## 对象和操作重命名

规范迁移附录记录了若干名称变化，例如：

| Legacy | Current |
|---|---|
| `MessageSendParams` | `SendMessageRequest` |
| `SendStreamingMessageSuccessResponse` | `StreamResponse` |
| `SetTaskPushNotificationConfigRequest` | `CreateTaskPushNotificationConfigRequest` |
| `GetAuthenticatedExtendedCardRequest` | `GetExtendedAgentCardRequest` |

SDK 可以在过渡期提供 deprecated alias，但新集成应立即使用当前名称。Server 可以暂时接受两种请求形式，响应则应只输出协商版本的当前格式。

## `kind` 判别字段移除

A2A 1.0 对 Part 和 Streaming Event 的多态表示做了破坏性调整。旧版使用 `kind`：

```json
{"kind": "text", "text": "hello"}
```

当前使用成员名本身作为判别：

```json
{"text": "hello"}
```

旧文件 Part 将内容嵌套在 `file` 中，当前则直接使用 `raw` 或 `url`，并平铺 `filename` 和 `mediaType`。旧 Streaming Event 的 `kind: status-update` / `artifact-update` 也改为 `statusUpdate` / `artifactUpdate` wrapper。

这一变化与 Protobuf `oneof` 对齐，减少重复判别字段并改善代码生成，但旧 Parser 不能在未升级时直接读取新格式。

## Extended Agent Card 能力迁移

旧版能力位于 Agent Card 顶层：

```json
{"supportsExtendedAgentCard": true}
```

当前统一放进 Capabilities：

```json
{"capabilities": {"extendedAgentCard": true}}
```

过渡期 Client 可以先读新字段，再回退旧字段；新 Server 和 SDK 只应生成当前结构。迁移项见 [Migration & Legacy Compatibility](../../../submodules/a2a/docs/specification.md#appendix-a-migration--legacy-compatibility)。

## 正确迁移流程

```text
fetch Agent Card
  -> choose explicit interface and protocolVersion
  -> decode according to selected version
  -> optionally accept documented legacy aliases during overlap
  -> encode only the selected version
  -> emit deprecation metrics and warnings
  -> remove compatibility path after published deadline
```

版本字段是主要判断依据。观察到 `kind` 可以用于兼容解析，但不应代替版本协商，否则格式歧义会随着迁移长期存在。

## 长期 Task 的版本固定

> **状态**: inference

Hub 创建远端 Task 时应保存 Agent 身份、Endpoint、Binding、tenant 和协议版本快照。Agent Card 后来升级，不代表恢复旧 Task 时可以直接切换新接口或新序列化语义。

```text
RemoteTaskBinding
|-- remoteTaskId
|-- agentCard digest / agent version
|-- interface URL / Binding / A2A version
`-- credential and tenant reference
```

只有 Server 明确承诺跨接口兼容同一 Task，Hub 才应迁移查询和订阅路径；否则应继续使用原接口直到任务终止。

## 兼容层治理

兼容代码必须有可观察性和退出条件：

- 统计每个版本、旧字段和旧 Endpoint 的调用量；
- 对仍使用旧格式的 Client 发出弃用警告；
- 发布最早移除版本和时间窗口；
- 为旧版安全缺陷设置更快的强制退役策略；
- 删除无流量、已过窗口的双解析分支和测试。

> **精髓：兼容层应宽容读取、严格输出，并且有明确退役期限。**

## QA / 讨论记录

### Q: 能否在一个响应里同时输出 `kind` 和当前字段？

> **状态**: verified / inference | **来源**: migration guidance

不应这样做。混合格式会产生新的非标准方言；Server 应按协商版本输出唯一合法表示。

### Q: Agent Card 更新到 1.0 后，旧 Task 是否自动变成 1.0 Task？

> **状态**: inference | **来源**: protocol versioning / durable task reasoning

不能这样假定。Hub 应保存创建 Task 时的接口和版本，除非 Remote Agent 明确提供跨版本迁移保证。

### Q: 为什么兼容 Reader 可以更宽容，而 Writer 要严格？

> **状态**: verified / inference | **来源**: migration strategy

宽容 Reader 帮助平滑接收旧调用；严格 Writer 防止继续扩散旧格式和混合方言，使生态最终能够收敛。

## 相关文档

- [A2A 版本协商与兼容性](versioning-and-compatibility.md)
- [协议 Binding 与互操作](protocol-bindings.md)
- [A2A 线级数据契约与一致性测试](wire-contract-and-conformance.md)
- [Part、媒体类型与业务数据交换](content-and-media-exchange.md)
