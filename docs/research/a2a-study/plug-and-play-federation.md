# A2A 即插即用与通用联邦平台

> **日期**: 2026-08-25 | **状态**: draft | **证据状态**: verified（本地项目能力）/ inference（目标架构） | **涉及版本**: `Agent Stack@79c78604`、`Solace Agent Mesh@2b4ef6ab5`、`Routa@e48861ab`、`Bindu@7b1ff75a`、`agentgateway@0d510695`、`ShrimpCrab@34e9fcd`、`Nacos@812f49f67`

## 一句话结论

A2A 联邦平台的业务价值不应是“再做一个内部多 Agent 编排器”，而应是让不同公司、框架、语言和部署环境中的 Agent，在不修改 Hub 核心代码、不暴露自身内部实现的前提下，通过标准契约进入一个可发现、可治理、可调用、可替换的协作网络。

现实中的“即插即用”不是零配置，而是**零平台核心改造和零 Agent 业务代码侵入**：提交 Agent Card URL 与必要的凭证引用后，其余校验、登记、索引、健康检查、网关接管和生命周期管理由平台完成。

## 为什么不能只说“用 LangGraph 也能做”

如果所有 Agent 都属于同一个团队，能够统一修改代码、共享状态 Schema、共同部署并同步升级，那么使用 LangGraph 等内部编排 Runtime 通常更成熟、简单和可靠。

A2A 联邦平台要解决的是另一类所有权边界：

| 问题 | 内部 LangGraph | A2A Federation |
|---|---|---|
| 节点是谁定义的 | 同一应用开发者 | 不同团队或公司 |
| 是否能修改节点代码 | 通常可以 | 通常不可以 |
| 是否共享 State Schema | 可以统一约定 | 只能依赖公开协议契约 |
| 是否共同部署升级 | 通常是 | 通常不是 |
| 如何发现新节点 | 修改图或配置 | Agent Card、Registry、准入与动态发现 |
| 信任边界 | 一个系统内部 | 跨租户、跨网络、跨组织 |
| 故障与任务恢复 | Runtime 内部 checkpoint | 跨系统 Task、幂等、回调、轮询和补偿 |

因此真正的分界线不是“有没有多个 Agent”，而是：**这些 Agent 是否由同一个所有者控制，接入新 Agent 时是否必须修改内部编排代码。**

## 即插即用的验收标准

理想接入流程是：

```text
Agent Card URL + credential reference
  -> fetch
  -> validate
  -> snapshot and version
  -> index Skills
  -> admission and policy
  -> health and refresh
  -> gateway route
  -> routable Agent
```

接入一个新的标准 A2A Agent 时，以下条件应同时成立：

- 不修改 Hub 核心源码；
- 不要求外部 Agent 暴露 Prompt、Memory、Tool 或内部 Workflow；
- 不为每个 Agent 编写新的协议转换器；
- 允许配置凭证、租户、网络、配额和准入策略；
- Agent Card 或 Endpoint 变化后能够刷新、重新验证或回滚；
- Agent 下线、失信或越权时能够立即隔离和撤销。

“不需要配置认证、不需要审核、不需要健康检查”不是即插即用，而是不受治理。

## 三种接入通道

### Native Registration：Agent 主动报到

Agent 使用 Hub SDK 或 API 主动提交定义、版本和运行时 Endpoint，并持续 heartbeat：

```text
Agent startup
  -> publish definition / Agent Card
  -> register runtime endpoint
  -> heartbeat
  -> deregister on shutdown
```

适合组织内部 Agent 或愿意集成平台 SDK 的合作伙伴。Nacos、Agent Stack 和 Bindu 提供了不同程度的参考。

### Remote A2A Import：平台主动接入

外部 Agent 已经是标准 A2A Server，平台只取得 URL 与凭证：

```text
operator submits URL
  -> Hub fetches Agent Card
  -> Hub creates proxy identity
  -> Hub refreshes card and checks health
  -> other Agents discover and invoke it through Hub
```

这是跨公司接入最关键的路径。Solace A2A Proxy 最接近完整实现，Routa 展示了在业务 Workflow 节点中直接消费远程 URL 的最小实现。

### Legacy Adapter：把非 A2A Agent 标准化

已有 Agent 只有 Python handler、LangGraph、CrewAI 或本地 CLI，需要在提供方一侧增加通用 Adapter：

```text
existing handler / framework
  -> Agent Stack or Bindu wrapper
  -> standard Agent Card + A2A server
  -> Native Registration or Remote Import
```

这条路径不是完全无侵入，但修改的是通用接入壳，不是 Hub 为每个 Agent 增加专用业务代码。

## 用“跨公司专业服务大厅”理解字段和组件

把每个 Agent 看成一家独立专业机构，把 Federation Hub 看成跨公司的服务大厅。

| 技术概念 | 服务大厅比喻 | 实际含义 |
|---|---|---|
| Agent Card | 服务名片和营业说明书 | 身份、能力、接口、媒体、安全和协议声明 |
| `agentCardUrl` | 领取说明书的地址 | 获取 Agent Card 的 URL |
| Skill / `skillId` | 服务菜单 / 服务项目编号 | Agent 宣称能够完成的一类工作 |
| Endpoint | 办理业务的柜台 | 真正接收 A2A 请求的地址 |
| Declared Endpoint | 营业执照上的长期地址 | Agent 定义中声明的静态地址 |
| Runtime Endpoint | 今天实际开门的柜台 | 当前实例发布的动态地址 |
| `authConfigId` | 密钥柜编号 | 指向凭证系统中的配置，不保存明文秘密 |
| Registry | 工商登记和营业名录 | 保存身份、版本、地址、健康与检索信息 |
| Gateway | 园区总门卫 | 认证、策略、限流、路由和审计 |
| Proxy | 外部商户接待专员 | 代表不能加入内部网络的远程 Agent 接收和转发任务 |
| heartbeat | 柜台定期打卡 | 证明 Runtime publisher 仍然活跃 |
| `contextId` | 案件号 | 关联一组协作任务和消息 |
| `taskId` | 工单号 | 标识一项可追踪工作 |
| `messageId` | 来函编号 | 标识一次协议消息，支持追踪和去重 |
| Artifact | 正式交付件 | 报告、JSON、文件、代码或其他任务输出 |

## 一次完整的外部 Agent 接入

以合同审查 Agent 为例，管理员提交：

```text
Agent Card URL = https://legal.example.com/.well-known/agent-card.json
credentialRef  = legal-company-oauth
```

### 获取公开说明书

Card Fetcher 取得公开 Agent Card。公开 Card 告诉平台“对方宣称什么”，不代表这些声明已经被平台验证。

### 校验兼容性和风险

Card Validator 检查 Schema、协议版本、接口 URL、媒体类型、安全声明、签名和扩展。平台还要防止 SSRF、私网探测、恶意重定向、过大响应和不允许的域名。

### 建立不可混淆的登记和版本

Registry 保存原始 Card、摘要、签名、获取时间和版本快照。新的 Card 不应静默覆盖旧内容；协议、Skill、安全或 Endpoint 的重大变化需要重新准入并可回滚。

### 建立能力索引

Capability Index 按 Skill、tag、输入输出媒体、组织、租户和协议能力建立候选索引。Skill 是语义能力声明，不是质量证明；平台仍需评测和历史表现。

### 绑定凭证和准入策略

凭证只保存引用，运行时从 Secret Manager 取得。Admission Policy 决定哪些租户、用户、数据等级和网络区域可以调用该 Agent。

### 建立代理地址

Gateway 为 Agent 分配平台地址，例如：

```text
https://hub.example.com/agents/legal-review
```

向调用者返回的 Agent Card 应指向 Gateway，避免绕过平台的身份、限流和审计。

### 刷新和健康检查

平台周期刷新 Agent Card、探测 Endpoint，并结合主动 heartbeat、被动调用结果和熔断状态判断是否可路由。声明地址和当前运行实例需要分别保存。

### 执行并跟踪任务

Router 选择候选 Agent；Task Runtime 发送 Message，保存外部 `taskId`、`contextId` 和幂等关联，处理 Streaming、轮询、Push、取消、输入补充、认证恢复和 Artifact 回流。

## 即插即用成熟度

| 级别 | 能力 |
|---|---|
| L0 | 平台源码内硬编码 Agent 类型或业务适配 |
| L1 | 通过 SDK / Wrapper 把已有 Agent 暴露为 A2A |
| L2 | 只配置远程 Agent URL 和凭证即可调用，不修改双方核心源码 |
| L3 | 运行时注册、发现、版本、刷新、健康、注销和动态路由 |
| L4 | 信任、策略、租户、审计、灰度、撤销、质量和成本治理 |

没有一个当前本地项目独立覆盖完整 L4。开源生态的现实形态是分层组合。

## 本地项目源码核验

### Solace Agent Mesh：外部 Agent Proxy

Solace 的 A2A Proxy 是当前最清晰的外部接入参考。一个 Proxy 可以配置多个外部 A2A-over-HTTPS Agent，分别使用 URL、认证和超时。它负责协议桥接、获取并周期刷新 Agent Card、向 Mesh 发布发现信息、Artifact 转换、Task 取消和清理，以及 OAuth 失败后的刷新重试。

- 组件定位和外部 Agent 无修改接入见 [proxies.md](../../../submodules/solace-agent-mesh/docs/docs/documentation/components/proxies.md#key-functions)；
- `proxied_agents`、刷新周期和认证配置见 [proxies.md](../../../submodules/solace-agent-mesh/docs/docs/documentation/components/proxies.md#basic-configuration)；
- 示例见 [a2a_proxy_example.yaml](../../../submodules/solace-agent-mesh/examples/a2a_proxy_example.yaml)。

边界：开源路径仍以 YAML 配置为主；企业版文档提供向导。外部 Proxy Agent 主要从 Mesh 接收任务，不具备 Native Agent 的全部主动发起能力。因此外部接入约为 L2，Mesh 内原生发现接近 L3。

### Nacos：Agent Registry 与 Runtime Endpoint

当前本地 Nacos 已包含通用 Agent、AgentVersion、AgentCallInterface、Declared Endpoint 和 Runtime Endpoint 模型，并以 A2A 作为首个协议 Adapter。Client API 提供 Search、Discover、轮询订阅、定义发布、Endpoint 注册/注销和 heartbeat。

- 对象和生命周期边界见 [agent-management-spec.md](../../../submodules/nacos/specs/zh-cn/ai/agent-management-spec.md#1-范围与边界)；
- SDK 契约见 [agent-api-spec.md](../../../submodules/nacos/specs/zh-cn/ai/agent-api-spec.md#21-java-sdk-契约)；
- HTTP Endpoint 与活性见 [agent-api-spec.md](../../../submodules/nacos/specs/zh-cn/ai/agent-api-spec.md#23-client-http-路径)；
- 旧 A2A Agent Card 到通用模型的 Adapter 见 [A2aServerOperationService.java](../../../submodules/nacos/ai/src/main/java/com/alibaba/nacos/ai/service/a2a/A2aServerOperationService.java)。

Nacos 明确不代理 Agent message、task、stream、retry 或 credential，因此它是 L3 控制面参考，不是完整 Hub。A2A Binding 当前规范还标记为实验性兼容契约，生产成熟度需继续跟踪。

### Routa：Workflow 中按 Agent Card URL 调用

Routa 的看板自动化步骤包含 `transport`、`agentCardUrl`、`skillId` 和 `authConfigId`。任务进入相应列后，后端获取 Card、解析 RPC URL、附加认证头、发送 `SendMessage`、保存外部 `taskId/contextId`，并异步轮询终态回写看板任务。

- 自动化步骤模型见 [kanban.rs](../../../submodules/routa/crates/routa-core/src/models/kanban.rs#L49)；
- 发送和保存外部任务关联见 [a2a.rs](../../../submodules/routa/crates/routa-core/src/rpc/methods/kanban/automation/a2a.rs#L30)；
- Card URL 解析和认证头见 [a2a.rs](../../../submodules/routa/crates/routa-core/src/rpc/methods/kanban/automation/a2a.rs#L474)。

它证明了业务流程节点可以实现 L2 接入，但尚未形成全局 Agent Registry、Skill 索引、持续 Card 刷新和通用路由。

### Agent Stack：Provider 侧 Wrapper 和自注册

Agent Stack SDK 将 LangGraph、CrewAI 或自定义 Agent 包装成运行服务，并自动暴露 A2A 接口。开发模式下 Server 根据自身 Agent Card 向 Agent Stack 平台创建或更新 Provider。

- 框架无关包装定位见 [README.md](../../../submodules/agent-stack/README.md#core-capabilities)；
- 自动注册逻辑见 [server.py](../../../submodules/agent-stack/apps/agentstack-sdk-py/src/agentstack_sdk/server/server.py#L338)；
- Provider discovery 当前接收 Docker image，而不是任意远程 URL，见 [provider_discovery.py](../../../submodules/agent-stack/apps/agentstack-sdk-py/src/agentstack_sdk/platform/provider_discovery.py)。

生产模式会关闭自动注册，因此它主要是 L1 Provider SDK / Runtime 参考，不能直接视为通用远程 Agent 导入平台。

### Bindu：Wrapper、身份和安全

Bindu 通过 `bindufy(config, handler)` 将现有 handler 暴露为 A2A 服务，并加入 DID、认证、Skills、Streaming、存储和支付。TypeScript、Kotlin 等 SDK 通过 gRPC 向 Core 注册，Core 为其创建相同的 A2A HTTP 服务，并处理 heartbeat 和 unregister。

- 一函数包装见 [README.md](../../../submodules/bindu/README.md#quickstart)；
- gRPC 注册和 A2A Server 创建见 [service.py](../../../submodules/bindu/bindu/grpc/service.py#L80)；
- 当前注册表为进程内存结构，见 [registry.py](../../../submodules/bindu/bindu/grpc/registry.py#L46)。

它是较强的 L1 参考，尤其适合研究 Provider SDK、身份、私有 Skill 和凭证边界，但未证明可以直接导入任意第三方 A2A URL。

### agentgateway：A2A Data Plane

agentgateway 把普通 HTTP backend 标记为 A2A 流量后，可以代理请求、识别 A2A method 和 Task 状态，并重写 Agent Card 中的公开 URL，使后续请求继续经过 Gateway。

- 配置与行为见 [traffic-a2a/README.md](../../../submodules/agentgateway/examples/traffic-a2a/README.md)；
- Agent Card URL 改写见 [mod.rs](../../../submodules/agentgateway/crates/agentgateway/src/a2a/mod.rs#L157)。

它不主动抓取和索引 Agent Card，也不提供 Agent Registry。因此它是 L2 数据面组件，不是独立的即插即用控制面。

### ShrimpCrab：产品内 Agent 导入，不是标准远程 A2A 导入

ShrimpCrab README 把 A2A Wrapper、Workflow Executor 和 Agent Runner 列为编排层，但当前 Agent Runner 实际主要适配 Claude Code、OpenClaw、Codex、Hermes 和 OpenCode 等已知 CLI Runtime。其 Agent Market 和本地导入围绕平台 manifest、Workspace 和 Runner 展开。

- 产品和编排分层见 [README.md](../../../submodules/shrimpcrab/README.md#技术架构)；
- Runner 的固定平台类型见 [agent-runner.service.ts](../../../submodules/shrimpcrab/backend/src/services/agent-runner.service.ts#L17)；
- Agent 创建接收内部 manifest，见 [agents.routes.ts](../../../submodules/shrimpcrab/backend/src/routes/agents.routes.ts#L1367)。

本轮没有找到“输入任意 Agent Card URL并自动形成标准远程调用”的实现，因此它更接近 L0 产品平台参考，不应因 README 中出现 A2A Wrapper 就归类为协议级 Federation Hub。

## 组合后的目标架构

现有项目更适合按层组合：

```text
Provider Adapter
  Agent Stack / Bindu 式 Wrapper

Onboarding Control Plane
  URL import / validation / admission / version

Registry and Discovery
  Nacos 式 definition + runtime endpoint + health

Federation Proxy
  Solace 式 external A2A bridge + artifact/task lifecycle

Data Plane
  agentgateway 式 auth / policy / rate limit / URL rewrite / trace

Workflow Consumer
  Routa 式 agentCardUrl node + external task reconciliation
```

第一版最小闭环不需要同时实现所有企业能力，但至少应完成：

1. 通过 API 动态提交 Agent Card URL 和 `credentialRef`；
2. 主动获取并校验 Card，保存原始快照和摘要；
3. 建立 Skill 索引和人工/策略准入状态；
4. 分配 Gateway URL，并保证调用不能绕过策略；
5. 刷新 Card、探测健康、支持禁用和撤销；
6. 完成 Message、Task、Streaming/轮询和 Artifact 的最小调用闭环；
7. 提供审计记录，说明谁在何时通过什么策略调用了哪个版本。

## 业务通用性应该体现在哪里

通用性不是在核心代码里预置尽可能多的场景，而是让核心只依赖稳定抽象：

- Agent 身份和版本；
- Skill 和输入输出媒体；
- Message、Task、状态和 Artifact；
- 参与者、租户和权限；
- Workflow trigger 和回调；
- 策略、预算、质量和审计扩展点。

领域差异放在 Agent、Skill、Workflow 模板和扩展 metadata 中。这样同一个平台可以承载：

| 场景 | Agent 协作方式 |
|---|---|
| 跨公司采购 | 采购、供应商、法务、财务和物流 Agent 协作 |
| 办公流程 | 日程、文档、审批、差旅、财务 Agent 协作 |
| 软件研发 | 需求、编码、测试、安全审计和发布 Agent 协作 |
| 事故响应 | 监控、日志、云资源、通知和复盘 Agent 协作 |
| 科研 | 文献、实验、数据分析、复现和写作 Agent 协作 |
| 内容生产 | 研究、写作、审校、视觉和发布 Agent 协作 |
| 个人服务 | 用户的私人 Agent 与银行、医疗、出行、购物 Agent 协作 |
| 狼人杀等游戏 | 每个玩家 Agent 独立部署，通过 Message、Task、规则和权限参与房间 |

狼人杀可以用来检验角色隔离、广播/私聊、回合状态、超时和不可信参与者，但平台核心不应出现 `werewolf`、`seer` 等领域对象。它们属于上层 Game Workflow 和业务 metadata。

## QA / 讨论记录

### Q: 即插即用是否意味着完全不做配置？

> **状态**: verified / inference | **来源**: project source-code / architecture reasoning

不是。跨组织调用必然需要地址、凭证、租户、网络和准入信息。即插即用的关键是这些信息进入通用接入模型，而不是每加入一个 Agent 就修改 Hub 核心代码或外部 Agent 的业务实现。

### Q: 如果还要配置 Agent URL，为什么不直接把 URL 写进 LangGraph？

> **状态**: inference | **来源**: architecture reasoning

固定 Workflow 可以直接配置 URL；联邦平台的增量价值在于统一验证、版本、发现、健康、信任、权限、网关、审计和替换。Workflow 依赖一个受治理的逻辑 Agent/Skill，而不是永久绑定某个未经验证的物理地址。

### Q: 哪个本地项目最接近完整即插即用平台？

> **状态**: verified | **来源**: source-code / project docs

没有单一完整实现。Solace 最接近外部 A2A 无侵入代理；Nacos 最接近动态 Registry 控制面；Routa 展示 Workflow URL 接入；Agent Stack 和 Bindu解决 Provider 侧标准化；agentgateway 解决统一数据面。

### Q: 去中心化是否意味着不需要平台？

> **状态**: inference | **来源**: architecture reasoning

不意味着。去中心化可以减少中央业务协调者，但身份、发现、权限、预算、循环检测、健康、撤销和审计仍需要协议或基础设施支撑。平台可以管理协作规则，而不掌握所有业务正文。

## 相关文档

- [A2A 协议原理与互操作模型](protocol-and-interop.md)
- [AgentCard：发现、协商与信任边界](agent-card.md)
- [Context、History、Message 与编排拓扑](context-and-orchestration.md)
- [A2A 项目景观](project-landscape.md)
