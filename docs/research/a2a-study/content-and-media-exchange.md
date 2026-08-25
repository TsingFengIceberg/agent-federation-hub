# A2A Part、媒体类型与业务数据交换

> **日期**: 2026-08-26 | **状态**: draft | **证据状态**: verified（协议模型）/ inference（生产安全措施） | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

Message 和 Artifact 通过一个或多个 Part 承载真实业务内容。每个 Part 在 `text`、`raw`、`url` 和 `data` 中选择一种内容形态，并可携带媒体类型、文件名和 metadata；Agent Card、Skill 和本次请求共同完成输入输出媒体协商。

## Part 是最小内容单元

`Message.parts` 和 `Artifact.parts` 都是非空 Part 列表。一条 Message 可以同时包含自然语言说明、文件和结构化参数：

```text
Message
|-- Part(text): 请审查这份合同
|-- Part(raw): contract.pdf
`-- Part(data): 地区、合同类型、风险偏好
```

当前 [Part](../../../submodules/a2a/specification/a2a.proto#L223) 使用 Protobuf `oneof content`，单个 Part 只能选择一种内容：

| 字段 | 内容 | 常见用途 |
|---|---|---|
| `text` | 字符串 | 指令、解释、Markdown |
| `raw` | 原始字节；JSON 中为 Base64 | 直接上传图片、PDF、音频 |
| `url` | 文件内容地址 | 大文件、对象存储、临时下载链接 |
| `data` | 任意 JSON 值 | 订单、表单、报价、结构化结果 |

所有形态还可以使用 `mediaType`、`filename` 和 `metadata`。`mediaType` 是 MIME 类型，不应仅凭文件扩展名推断内容。

## Raw 与 URL 文件交换

`raw` 使请求自包含，但 JSON Base64 会增加传输体积和内存压力，适合受控大小的文件。`url` 适合大文件和对象存储，但引入地址有效期、网络可达性、认证和 SSRF 风险。

生产实现至少需要校验：

- URL scheme、域名、解析后的 IP 和重定向目标；
- 下载超时、文件大小、压缩比例和并发限制；
- 声明 MIME、文件签名和实际内容是否一致；
- 临时 URL 与凭证的有效期、audience 和最小权限；
- 病毒、恶意文档、解析器漏洞和数据驻留要求。

A2A 只定义文件如何表达，不提供对象存储、内容扫描和数据驻留治理。规范示例见 [File Exchange](../../../submodules/a2a/docs/specification.md#67-file-exchange-upload-and-download)。

## 结构化数据

机器可消费的订单或工单应优先放在 `data` 中，而不是把 JSON 再编码成 `text`：

```json
{
  "data": {
    "orderId": "O-100",
    "currency": "CNY",
    "amount": 1999
  },
  "mediaType": "application/json"
}
```

`data` 只保证 JSON 值的线级表达，不自动提供业务 Schema。字段约束可以由文档、JSON Schema、协议 Extension 或双方带外契约定义；需要跨组织稳定复用时，不应只依靠未经声明的 metadata。

## 三层媒体协商

```text
AgentCard.defaultInputModes / defaultOutputModes
  -> Agent 默认媒体能力

AgentSkill.inputModes / outputModes
  -> 特定 Skill 覆盖默认值

SendMessageConfiguration.acceptedOutputModes
  -> Client 本次准备接收的输出类型
```

Client 发送前应先按目标 Skill 的有效媒体类型检查输入，并声明本次可接收的输出。Server 收到不支持的 Message Part 媒体类型，或无法提供可接受输出时，应以 `ContentTypeNotSupportedError` 明确失败，而不是错误解析或悄悄返回完全不同的格式。

`acceptedOutputModes` 是 Client 的本次输出约束和偏好，Agent 应据此调整 Artifact Part。它不改变 Agent Card 的长期能力声明。

## Message Part 与 Artifact Part

两者复用相同 Part 模型，但业务角色不同：

- Message Part 用于请求、补充输入、澄清、状态解释和协作交流；
- Artifact Part 用于 Task 的正式输出，如报告、补丁、表格和生成媒体。

“报告生成完成”可以是状态 Message，真正的报告 PDF 和风险 JSON 应成为 Artifact。关键结果不能只依赖可能未持久化或断线时错过的状态 Message。

## 多 Part 与多 Artifact

一个 Message 或 Artifact 可以包含多个互补 Part，一个 Task 也可以产生多个 Artifact：

```text
Task
|-- Artifact: risk-report
|   |-- Part(text/markdown): 摘要
|   `-- Part(data/json): 风险明细
`-- Artifact: annotated-contract
    `-- Part(url/application/pdf): 带批注合同
```

流式 Artifact 仍按 `(taskId, artifactId)` 和 `append/lastChunk` 聚合；Part 类型不会改变 Artifact 的身份和增量规则。

## QA / 讨论记录

### Q: 为什么已经有 `mediaType=application/json`，还要区分 `text` 和 `data`？

> **状态**: verified | **来源**: protocol data model

`text` 在线上仍是字符串，调用方需要再次解析；`data` 是原生结构化 JSON 值。`mediaType` 描述内容类型，不能替代实际承载形态。

### Q: URL Part 是否表示 Remote Agent 一定能访问该 URL？

> **状态**: verified / inference | **来源**: protocol boundary / security reasoning

不表示。A2A 只表达引用，网络可达性、临时授权、下载失败和安全校验由双方运行环境负责。

### Q: AgentCard 声明支持 PDF，是否表示所有 Skill 都支持 PDF？

> **状态**: verified | **来源**: official specification

不一定。Skill 的 `inputModes` / `outputModes` 可以覆盖 Agent 默认值，调用前应以目标 Skill 的有效媒体范围为准。

## 相关文档

- [从 Message 到 Task 与 Artifact](message-task-artifact.md)
- [AgentCard：发现、协商与信任边界](agent-card.md)
- [Extension 扩展与能力协商](extensions-and-negotiation.md)
- [Task 更新交付与断线恢复](task-delivery-and-recovery.md)
