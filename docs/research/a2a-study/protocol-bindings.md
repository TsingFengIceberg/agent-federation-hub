# A2A 协议 Binding 与互操作

> **日期**: 2026-08-25 | **状态**: draft | **证据状态**: verified | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

A2A 先定义一套与传输无关的 Message、Task、Artifact、操作和错误语义，再将同一语义映射到 JSON-RPC、gRPC 和 HTTP+JSON 三种标准 Binding。Binding 改变字节怎样传输和错误怎样封装，不改变业务对象与生命周期的含义。

## Binding 不是三套 A2A

可以把 A2A 核心语义看成同一项业务制度，把 Binding 看成 App、电话和柜台三个办理渠道。规范要求它们具备：

- 相同功能；
- 语义等价的结果；
- 一致可识别的错误映射；
- 等价的认证与授权效果。

因此 Client 换用另一个 Binding 后，不应得到另一套 Task 状态、权限规则或 Artifact 语义。互操作要求见 [specification.md](../../../submodules/a2a/docs/specification.md#5-protocol-binding-requirements-and-interoperability)。

## Agent Card 如何声明接口

Agent Card 的 `supportedInterfaces` 是有序列表，每项 `AgentInterface` 包含：

- `url`：接口地址；
- `protocolBinding`：JSON-RPC、gRPC、HTTP+JSON 或自定义 Binding 标识；
- `protocolVersion`：该接口支持的 A2A `Major.Minor` 版本；
- 可选 `tenant`：必须原样带入该接口请求的透明路由标识。

第一项是 Server 推荐接口，但 Client 可以选择任一兼容项，并在首选接口不可用时回退。结构定义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L336)。

## 核心操作映射

| 语义操作 | JSON-RPC | gRPC | HTTP+JSON |
|---|---|---|---|
| Send Message | `SendMessage` method | `SendMessage` RPC | `POST /message:send` |
| Send Streaming Message | `SendStreamingMessage` method + SSE | Server-streaming RPC | `POST /message:stream` + SSE |
| Get Task | `GetTask` method | `GetTask` RPC | `GET /tasks/{id}` |
| List Tasks | `ListTasks` method | `ListTasks` RPC | `GET /tasks` |
| Cancel Task | `CancelTask` method | `CancelTask` RPC | `POST /tasks/{id}:cancel` |
| Subscribe To Task | `SubscribeToTask` method + SSE | Server-streaming RPC | `POST /tasks/{id}:subscribe` + SSE |

Push 配置 CRUD 和 `GetExtendedAgentCard` 也必须在三种 Binding 中提供等价映射。完整表见 [specification.md](../../../submodules/a2a/docs/specification.md#53-method-mapping-reference)。

## JSON-RPC Binding

JSON-RPC 2.0 通常通过一个 HTTP(S) RPC Endpoint，以请求体中的 `method` 选择操作。它便于通用 RPC Client 接入，Streaming 使用 SSE。

JSON-RPC 的应用错误可能随 HTTP `200` 返回，因此 Client 必须解析响应体中的 `result` 或 `error`，不能只看 HTTP 状态。A2A 专属错误使用保留错误码范围并携带结构化信息，详见 [JSON-RPC Binding](../../../submodules/a2a/docs/specification.md#9-json-rpc-protocol-binding)。

## gRPC Binding

gRPC 根据 Protobuf 生成强类型 RPC 方法和消息，Streaming 使用原生 Server Streaming。它适合内部服务、高吞吐和已有 gRPC 基础设施的环境。

版本、扩展等 Service Parameters 通过 gRPC metadata 传递；错误使用 `google.rpc.Status`，A2A 错误类型通过 `google.rpc.ErrorInfo` 进一步区分。定义见 [gRPC Binding](../../../submodules/a2a/docs/specification.md#10-grpc-protocol-binding)。

## HTTP+JSON Binding

HTTP+JSON 使用资源化路径和 HTTP 动词，并以 `application/a2a+json` 承载数据；GET 和 DELETE 操作通过 path/query 传参，Streaming 使用 SSE。它最容易被普通 API Gateway、Ingress、浏览器调试工具和 HTTP 监控理解。

多个 A2A 错误可能映射到同一个 HTTP 状态，因此 Client 除了检查状态码，还应读取 `google.rpc.ErrorInfo` 识别具体错误类型。定义见 [HTTP+JSON Binding](../../../submodules/a2a/docs/specification.md#11-httpjsonrest-protocol-binding)。

## Service Parameters

协议当前定义两个横切所有操作的标准参数：

| 参数 | 作用 |
|---|---|
| `A2A-Version` | 声明本次请求采用的 A2A `Major.Minor` 版本 |
| `A2A-Extensions` | 声明本次请求希望启用的扩展 URI |

HTTP 类 Binding 通过 Header 传递，gRPC 通过 metadata 传递。自定义 Binding 必须说明这些 Service Parameters 如何承载。

## 数据表示仍需保持一致

不同 Binding 还必须统一容易产生细微差异的数据规则：

- JSON 字段使用 `lowerCamelCase`；
- JSON 中的枚举使用 Protobuf 定义名；
- 时间戳序列化为 UTC ISO 8601；
- required 字段必须存在，required 数组至少有一个元素；
- 实现应忽略未知字段，以支持协议向前兼容；
- “字段未设置”和“显式设置为默认值”在需要签名或应用默认值时不能混淆。

这说明互操作不只是 Endpoint 名称对得上，还包括字段存在性、枚举、时间和错误的完整线级契约。

## Binding 选择与回退

```text
fetch Agent Card
  -> read ordered supportedInterfaces
  -> filter by supported Binding and protocol version
  -> select preferred compatible interface
  -> attach tenant, A2A-Version, extensions and credentials
  -> invoke
  -> on transport/interface failure, consider another declared interface
```

回退不能悄悄改变业务语义或丢失必需能力。例如 Client 必须使用某项新版本特性时，不应自动回退到不支持该特性的旧协议版本；Binding 可替换不等于能力可以静默降级。

## 自定义 Binding

A2A 允许自定义 Binding，但它必须提供完整操作、数据类型、Service Parameters、错误、Streaming、认证授权和 Agent Card 声明规则。自定义 Binding 应使用全局唯一 URI 标识；发生破坏性变更时必须换新 URI。

例如：

```text
https://example.com/bindings/websocket/v1
  -> breaking change
https://example.com/bindings/websocket/v2
```

这防止两个实现都声称支持“websocket”，实际却采用不兼容语义。要求见 [Custom Binding Guidelines](../../../submodules/a2a/docs/specification.md#12-custom-binding-guidelines)。

## QA / 讨论记录

### Q: 支持 HTTP+JSON 是否就算完整支持 A2A？

> **状态**: verified | **来源**: official specification

只有同时实现 A2A 的核心对象、操作、状态、错误、安全和数据表示语义，才是 A2A HTTP+JSON Binding。仅提供一个能收发 JSON 的 REST API 不等于支持 A2A。

### Q: JSON-RPC 返回 HTTP 200 是否表示任务成功？

> **状态**: verified | **来源**: official specification

不表示。HTTP 200 可能只说明 JSON-RPC 响应成功传回，响应体仍可能包含 RPC 或 A2A 错误；Task 本身也可能处于 `FAILED` 等状态。

### Q: 三种 Binding 是否必须部署在同一个 URL？

> **状态**: verified | **来源**: official specification

不必须。Agent Card 可以为每种 Binding 和协议版本声明不同 URL，也允许同一传输的多个版本使用相同或不同 URL。

## 相关文档

- [A2A 协议原理与互操作模型](protocol-and-interop.md)
- [AgentCard：发现、协商与信任边界](agent-card.md)
- [A2A 操作集与 Task 管理边界](operations-and-task-management.md)
- [错误、幂等、重试与取消](reliability-errors-and-cancellation.md)
- [认证、授权与任务中授权](authentication-and-authorization.md)
- [A2A 版本协商与兼容性](versioning-and-compatibility.md)
- [Extension 扩展与能力协商](extensions-and-negotiation.md)
- [A2A 线级数据契约与一致性测试](wire-contract-and-conformance.md)
- [A2A 自定义 Binding 设计](custom-bindings.md)
- [A2A 旧版迁移与遗留兼容](migration-and-legacy-compatibility.md)
