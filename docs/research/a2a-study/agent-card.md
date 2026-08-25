# AgentCard：发现、协商与信任边界

> **日期**: 2026-08-25 | **状态**: draft | **证据状态**: verified（协议字段与发现流程）/ inference（Federation Hub 设计） | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

AgentCard 是 Agent 对外发布的机器可读能力清单，告诉其他 Agent：它是谁、能做什么、从哪里访问、支持哪些协议和媒体类型，以及需要怎样认证。

它近似于“服务名片 + 能力目录 + 接口入口 + 安全声明”，但既不是完整的 OpenAPI，也不是 Agent Registry。

## AgentCard 解决的问题

一个 Endpoint 本身无法告诉调用方：

- Agent 是否具备所需业务能力；
- 接受文本、JSON、文件还是多媒体；
- 使用 JSON-RPC、gRPC 还是 HTTP+JSON；
- 支持哪个 A2A 协议版本；
- 是否支持 Streaming、Push Notification 或扩展；
- 使用 API Key、OAuth、OIDC 还是 mTLS；
- 某个具体 Skill 是否需要额外 Scope。

AgentCard 在真正发送 Message 之前提供这些协商信息，让 Client 可以先判断兼容性和认证条件。

## 发现流程

A2A 定义了标准的 well-known 位置：

```text
https://example.com/.well-known/agent-card.json
```

典型流程如下：

```text
获得 Agent 域名
    |
    v
GET /.well-known/agent-card.json
    |
    v
验证并解析公开 AgentCard
    |
    v
检查 Skill、接口、协议、媒体类型和安全要求
    |
    v
可选：认证后获取 Extended AgentCard
    |
    v
选择兼容接口并发送 A2A Message
```

well-known URI 解决“已知域名后去哪里取得卡片”，并不解决“如何从大量 Agent 中发现合适域名”。后者属于 Registry、目录服务或 Federation Hub。

## 字段分组

当前 AgentCard 源码定义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L358)，可以按用途分为六组。

### 身份信息

| 字段 | 作用 |
|---|---|
| `name` / `description` | 面向人和 Agent 的名称与用途描述 |
| `provider` | 提供方组织和相关 URL |
| `version` | Agent 产品或服务版本，不是 A2A 协议版本 |
| `documentation_url` | 更详细的使用文档 |
| `icon_url` | 可选的展示图标 |

身份信息帮助理解和展示 Agent，但不能单独证明 Agent 可信或在线。

### Supported Interfaces

`supported_interfaces` 是有顺序的接口列表，第一项是服务端偏好的接口。每个 [AgentInterface](../../../submodules/a2a/specification/a2a.proto#L334) 包含：

- `url`：具体访问地址；
- `protocol_binding`：当前核心 binding 包括 `JSONRPC`、`GRPC` 和 `HTTP+JSON`；
- `protocol_version`：该接口暴露的 A2A 版本；
- `tenant`：共享 Endpoint 后可选的不透明路由标识。

同一个 Agent 可以通过多个 binding 暴露相同能力。Client 应根据自身支持范围选择兼容接口，而不是假定所有 A2A Agent 都只有一个 JSON-RPC 地址。

Agent 的 `version` 与接口的 `protocol_version` 必须区分：前者表示服务实现版本，后者表示网络协议兼容版本。

### Capabilities

当前 [AgentCapabilities](../../../submodules/a2a/specification/a2a.proto#L411) 包含：

- `streaming`：是否支持流式响应和订阅；
- `push_notifications`：是否支持 Task Push 配置；
- `extended_agent_card`：是否支持认证后取得扩展卡片；
- `extensions`：支持的协议扩展及其配置。

Capability 是协议能力承诺，不只是 UI 展示字段。Client 不应调用未声明支持的可选操作；服务端对未声明能力也应返回相应的不支持错误。

### Skills

Skill 描述 Agent 较明确、较可能成功完成的一类能力。当前 [AgentSkill](../../../submodules/a2a/specification/a2a.proto#L435) 包含：

- 唯一 `id`、名称和详细描述；
- 便于搜索的 tags；
- 示例 Prompt 或场景；
- 可覆盖全局默认值的 input/output media types；
- Skill 级安全要求。

Skill 偏向语义发现，而 MCP Tool 更接近具有明确输入 Schema 的具体操作：

| Agent Skill | MCP Tool |
|---|---|
| 描述 Agent 能完成的一类业务能力 | 描述可直接调用的具体操作 |
| 可能包含自主规划和多步执行 | 通常有明确参数 Schema |
| 可能形成长期 Task | 通常接近一次工具调用 |
| 结果可能包含多个 Artifact | 结果通常是 Tool Result |
| 用于能力发现和候选匹配 | 用于确定性能力调用 |

因此 Skill 描述不能替代质量评测，也不能因为自然语言宣称“擅长某任务”就直接建立信任。

### 输入输出模式

AgentCard 通过 `default_input_modes` 和 `default_output_modes` 声明所有 Skills 的默认媒体类型；具体 Skill 可以用自己的 `input_modes` 和 `output_modes` 覆盖默认值。

例如 Agent 默认接收文本，但财务分析 Skill 可以单独接收 JSON、CSV 或 Excel。Client 应同时检查任务需要的媒体类型与目标 Skill 的有效媒体类型。

### 安全声明

AgentCard 将安全分为两个层次：

```text
security_schemes
= 支持哪些认证机制及其定义

security_requirements
= 调用 Agent 或 Skill 时需要哪些机制和 Scope
```

当前 [SecurityScheme](../../../submodules/a2a/specification/a2a.proto#L501) 支持 API Key、HTTP Basic/Bearer、OAuth 2.0、OpenID Connect 和 Mutual TLS。AgentCard 只能声明取得或提交凭据的方法，不得把 Token、密码或私钥写入卡片。

## Public 与 Extended AgentCard

企业 Agent 不宜向匿名访问者公开所有 Skills。A2A 支持两层披露：

```text
Public AgentCard
  -> 可公开发现的基础能力和认证方式

完成认证
  -> GetExtendedAgentCard

Extended AgentCard
  -> 合作伙伴、租户或权限相关的额外能力
```

只有公开卡片声明 `capabilities.extended_agent_card = true` 时，Client 才能调用扩展卡片操作；该操作必须按公开卡片声明的安全方案认证。扩展卡片可以包含公开卡片没有展示的详细信息或 Skills，规范说明见 [specification.md](../../../submodules/a2a/docs/specification.md#311-get-extended-agent-card)。

这种设计将公开可发现性和授权后能力披露分开，避免把内部接口、折扣能力、管理操作或组织结构暴露到公网。

## AgentCard 签名的边界

AgentCard 可以携带符合 JWS 形式的签名，结构见 [AgentCardSignature](../../../submodules/a2a/specification/a2a.proto#L455)。签名可以帮助判断：

- 卡片内容自签发后是否被修改；
- 卡片是否由预期主体的密钥签发。

签名不能证明：

- Agent 输出一定正确；
- Skill 描述一定真实；
- Agent 没有恶意行为；
- 当前实例在线、低负载或满足 SLA。

签名解决来源和完整性，不等于业务信任。信任链、证书和密钥吊销、准入、健康、历史表现与评测仍属于平台治理。

## AgentCard 与 Registry

| AgentCard | Registry |
|---|---|
| 描述单个 Agent 的公开契约 | 管理和搜索多个 Agent |
| 由 Agent 提供方发布 | 由平台或组织运营 |
| 提供接口、Skill、媒体和安全声明 | 提供索引、健康、权限、质量和治理 |
| 主要是静态或半静态元数据 | 同时维护动态运行状态 |
| 可以携带签名 | 验证签名、执行准入与吊销 |
| 不负责实例选择和负载均衡 | 可以管理多个实例与 Endpoint |

一个生产 Registry 记录通常需要在原始 AgentCard 之外补充：

```text
AgentRecord
├── raw AgentCard
├── digest / signatures / fetched_at
├── validation and admission status
├── organization and tenant ownership
├── health and endpoint instances
├── allowed callers and risk level
├── historical quality / latency / success rate
└── cost, quota and routing metadata
```

因此把 AgentCard JSON 简单保存到服务注册中心，只完成了存储和部分发现，不等于完成 Agent Registry。

## 选择远程 Agent 的流程

以寻找竞品分析 Agent 为例，Federation Hub 可以依次判断：

1. Skill、tags 和 examples 是否匹配任务意图；
2. 输入和期望输出媒体类型是否兼容；
3. A2A 协议版本与 binding 是否兼容；
4. 是否支持任务需要的 Streaming、Push 或扩展；
5. 当前调用者能否满足 Agent/Skill 的认证要求；
6. 卡片及其签名是否通过验证和准入；
7. Endpoint 是否健康，历史质量、成本和延迟是否合适；
8. 租户、数据驻留和风险策略是否允许调用。

前五项主要来自 AgentCard，后三项主要来自 Registry、Gateway 和 Governance。

## 常见误区

- **把 Skill 当函数签名**：Skill 是语义能力描述，通常没有 Tool 那样确定的参数契约。
- **把 AgentCard 当实时状态**：声明支持 Streaming 不代表实例此刻健康或负载正常。
- **公开敏感 Skill**：应使用 Extended AgentCard 和授权后披露。
- **只做自然语言匹配**：还需校验 binding、协议版本、媒体类型、权限和健康状态。
- **认为签名等于可信**：签名不能替代质量、安全和行为评测。
- **永久缓存卡片**：平台应记录版本、摘要、获取时间和刷新策略。

## 对 Agent Federation Hub 的启发

> **状态**: inference

AgentCard 子系统至少可以拆成：

```text
Card Fetcher
  -> 获取 public / extended AgentCard

Card Validator
  -> 校验 schema、协议版本、签名与安全声明

Capability Index
  -> 按 Skill、tag、媒体类型、组织和租户建立索引

Selection Policy
  -> 联合健康、权限、质量、成本、延迟和风险选择 Agent
```

精髓是：**AgentCard 表示 Agent 自己声明了什么；Federation Hub 还要记录平台验证了什么、谁有权使用，以及此刻应该选择谁。**

## QA / 讨论记录

### Q: AgentCard 是否等于 Agent Registry？

> **状态**: verified / inference | **来源**: source-code / discussion

不等于。AgentCard 是单个 Agent 的自描述契约；Registry 负责跨 Agent 搜索、索引、健康检查、准入、权限、质量和路由。

### Q: 为什么 AgentCard 不能完全等同于 OpenAPI？

> **状态**: verified | **来源**: source-code / official-docs

OpenAPI 主要精确描述 HTTP API 操作和参数；AgentCard 主要描述 Agent 的语义 Skills、A2A 接口、媒体协商和长期任务能力。具体业务输入可以通过 Message 的结构化 Part 或应用扩展进一步约束。

### Q: JWS 签名是否足以判断 Agent 可信？

> **状态**: verified | **来源**: source-code / security reasoning

不足。签名验证来源和完整性，不能证明能力质量、在线状态、行为安全或调用结果正确。

## 相关文档

- [A2A 研究入口](README.md)
- [A2A 协议原理与互操作模型](protocol-and-interop.md)
- [Context、History、Message 与编排拓扑](context-and-orchestration.md)
- [认证、授权与任务中授权](authentication-and-authorization.md)
- [A2A 操作集与 Task 管理边界](operations-and-task-management.md)
- [协议 Binding 与互操作](protocol-bindings.md)
- [A2A 版本协商与兼容性](versioning-and-compatibility.md)
- [Extension 扩展与能力协商](extensions-and-negotiation.md)
- [Part、媒体类型与业务数据交换](content-and-media-exchange.md)
- [Agent Card 签名、规范化与缓存](agent-card-signing-and-caching.md)
- [A2A 即插即用与通用联邦平台](plug-and-play-federation.md)
- [A2A 项目景观](project-landscape.md)
- [来源仓库 MCP 概念底座](https://github.com/TsingFengIceberg/agent-systems-study/blob/98b4cbbba4877a8f40c52c5595f97a78bfaf1a07/DOCS/concepts/mcp.md)
