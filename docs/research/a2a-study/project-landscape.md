# A2A 项目景观

> **日期**: 2026-08-21 | **状态**: draft | **证据边界**: 本页汇总前期讨论、公开仓库与官方资料；未逐项完成源码核验的判断明确标为 `to-verify` 或 `inference`。

这份清单回答“哪些项目值得作为 A2A 研究样本”，而不是把所有多 Agent 项目都称为 A2A。标准 A2A 关注跨进程、跨团队或跨组织的 Agent 互操作；LangGraph、狼人杀等项目可以展示编排或消息流，但不因此成为联邦平台。

## 本仓库已纳入的 submodule

| 项目 | 类型 / 研究价值 | 仓库 |
|---|---|---|
| A2A | Google 发起的 Agent2Agent 协议规范、AgentCard、Message、Task、Artifact、流式与 Push | [a2aproject/A2A](https://github.com/a2aproject/A2A) |
| AAMP | LarkSuite 的 Agent Application / 多 Agent 协作协议与实现，重点观察企业协作场景和协议边界（`to-verify`） | [larksuite/aamp](https://github.com/larksuite/aamp) |
| Agent Stack | 通过框架无关 SDK 把 LangGraph、CrewAI 或自定义 Agent 包装成 A2A 服务；开发模式可向平台自注册，重点是 Provider 侧标准化而非任意远程 URL 导入（`verified`） | [i-am-bee/agentstack](https://github.com/i-am-bee/agentstack) |
| Routa | 看板 Workflow 可配置外部 `agentCardUrl`、Skill 和认证，运行时获取 Card、发送任务并轮询终态；尚非全局 Registry（`verified`） | [phodal/routa](https://github.com/phodal/routa) |
| MultiAgent-Werewolf | 小型狼人杀多 Agent Demo，展示角色间消息和回合编排，不是标准 A2A Registry | [kissie-77/MultiAgent-Werewolf](https://github.com/kissie-77/MultiAgent-Werewolf) |
| ShrimpCrab | 产品型多 Agent 平台，围绕内部 manifest、Agent Market、Workspace 和已知 CLI Runner；本轮未发现任意 Agent Card URL 导入（`verified`） | [chenlubenren/ShrimpCrab--multi-agent-platform](https://github.com/chenlubenren/ShrimpCrab--multi-agent-platform) |
| Nacos | 已实现通用 Agent/Version/CallInterface、A2A Adapter、Runtime Endpoint、Search/Discover、heartbeat 和健康生命周期；不代理实际 A2A Task 流量（`verified`） | [alibaba/nacos](https://github.com/alibaba/nacos) |
| agentgateway | A2A Gateway / Data Plane，可代理请求、改写 Agent Card URL 并记录协议级观测字段；不提供 Agent Registry（`verified`） | [agentgateway/agentgateway](https://github.com/agentgateway/agentgateway) |
| Solace Agent Mesh | 原生 Agent 动态发现，并通过 A2A Proxy 无侵入桥接外部 HTTPS Agent，负责 Card 刷新、认证、Artifact 和 Task 生命周期（`verified`） | [SolaceLabs/solace-agent-mesh](https://github.com/SolaceLabs/solace-agent-mesh) |
| Bindu | 用 `bindufy` 或多语言 gRPC SDK 将 handler 包装成带 DID、认证、Skills、支付和生命周期的 A2A 服务；当前不是任意远程 URL 导入平台（`verified`） | [GetBindu/Bindu](https://github.com/GetBindu/Bindu) |
| AgentKit samples | 字节火山 AgentKit 的公开 SDK / A2A 样例；云端 AgentKit 控制面并非开源 | [bytedance/agentkit-samples](https://github.com/bytedance/agentkit-samples) |

## 未作为 submodule 纳入的开源平台与框架

| 项目 | 定位 | 与 A2A 的关系 |
|---|---|---|
| [IBM MCP Context Forge](https://github.com/IBM/mcp-context-forge) | MCP / A2A / REST / gRPC Gateway、Registry、Proxy | 最接近通用 Gateway + Registry 参考；支持 AgentCard、Task、Push、RBAC、OAuth 等（`to-verify`） |
| [Archestra](https://github.com/archestra-ai/archestra) | Open Core Agent Platform（AGPL / Enterprise） | A2A 1.0、AgentCard registry、Durable Task、Streaming、Cancel、Push、HITL、SSO/RBAC（`to-verify`） |
| [kagent](https://github.com/kagent-dev/kagent) | Kubernetes Agent Runtime | 支持远程 A2A subagent、HITL 和用户身份传播；不是通用 Federation Registry（`to-verify`） |
| [OpenAgents](https://github.com/openagents-org/openagents) | Agent Network / Open Collaboration | 适合作为网络层和协作模型的候选样本（`to-verify`） |
| [AgentScope](https://github.com/agentscope-ai/agentscope) / [AgentScope Java](https://github.com/agentscope-ai/agentscope-java) | 多 Agent 框架与 Java Runtime | 适合作为企业内部编排、Message Hub 和 A2A adapter 参考，不等于联邦平台 |
| [CrewAI](https://github.com/crewAIInc/crewAI)、[Agno](https://github.com/agno-agi/agno)、[Mastra](https://github.com/mastra-ai/mastra) | Agent Framework | 可构建 A2A adapter 或远程 Agent，但主要解决应用内编排 |
| [Pydantic AI](https://github.com/pydantic/pydantic-ai)、[LangChain4j](https://github.com/langchain4j/langchain4j) | 类型安全 / Java Agent Framework | 适合作为 SDK、协议适配和工具调用的参考 |

## 商业平台与公开案例

这些项目或能力公开可用，但平台主体不是本仓库可以拉取的开源 submodule：

- **AWS AgentCore Runtime A2A**：托管 Agent Runtime 的 A2A endpoint，见 [官方文档](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-a2a.html)。
- **Microsoft Foundry / Copilot Studio**：提供入站或外部 A2A Agent 接入能力，见 [Foundry 文档](https://learn.microsoft.com/en-us/azure/foundry/agents/how-to/enable-agent-to-agent-endpoint)；平台本身闭源。
- **Oracle Autonomous Database A2A**：数据库 Agent 的 A2A 接入案例，见 [官方概念页](https://docs.oracle.com/en-us/iaas/autonomous-database-serverless/doc/a2a-concepts.html)。
- **Salesforce Agentforce / SOMA**：企业 Agent 协作与身份治理方向，协议和平台实现以商业产品为主（`to-verify`）。
- **Google Purchasing Concierge**：A2A 购物协作示例，见 [Google Codelab](https://codelabs.developers.google.com/intro-a2a-purchasing-concierge)。
- **Alibaba AgentRun / ACK Service Gateway**：函数计算和 ACK 上的 A2A 多 Agent 案例，见 [AgentRun Coffee Shop](https://www.alibabacloud.com/help/en/functioncompute/cech-coffee-shop-agenrun-a2a-protocol-multi-agent-case) 与 [ACK A2A Gateway](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/user-guide/build-an-a2a-service-gateway)。
- **火山 AgentKit A2A Center**：控制面闭源，但 SDK 与样例开源；可与本目录的 `agentkit-samples` 对照。
- **飞书 Aily / Agent Chat / OpenAPI MCP**：企业协作和能力接入参考，当前没有确认其作为通用标准 A2A Federation 平台（`to-verify`）。
- **腾讯、百度、华为**：前期调研尚未找到可确认的标准 A2A Registry / Gateway 主线开源项目；应保持“待核验”，不要据此断言不存在。

## 相邻项目：值得研究，但不要误分类

- [OpenHands](https://github.com/OpenHands/OpenHands) 是控制面 / 执行面 / Workspace / 事件流参考，不是标准 A2A Federation Platform。
- [LangGraph](https://github.com/langchain-ai/langgraph) 是有状态 Agent 编排 runtime，适合单一垂直领域内部多 Agent 流转。
- [OpenClaw](https://github.com/openclaw/openclaw)、[Dify](https://github.com/langgenius/dify) 和 [Coze Studio](https://github.com/coze-dev/coze-studio) 是 Harness 或应用平台，可作为 Agent 生命周期、工具和交付层参考。

## 研究启发

当前开源生态更像分层组合，而不是一个成熟的“万能 A2A 平台”：A2A 定义互操作语义，Nacos 解决定义、版本、运行时 Endpoint 与发现，agentgateway 解决跨边界流量和策略，Solace Proxy 解决外部 Agent 桥接与异步回流，Agent Stack / Bindu 解决 Provider 侧标准化，Routa 展示业务 Workflow 如何消费远程 Agent。自研通用平台时应优先完成 URL 接入、准入、版本、能力索引、健康、Gateway 和 Task 回流的最小闭环，而不是把某个 Demo 的多 Agent 调度直接扩展成联邦协议。

## 证据状态

- `verified`：链接和项目存在已核对；具体能力仍应回到源码或官方文档。
- `to-verify`：前期公开资料或讨论得到的候选判断，下一步需要源码 / 官方文档核验。
- `inference`：根据项目定位推断出的架构启发，不应当作项目承诺。

## 相关文档

- [A2A 研究入口](README.md)
- [A2A 即插即用与通用联邦平台](plug-and-play-federation.md)
- Registry / Discovery 研究槽位（planned）
- Gateway / Federation 研究槽位（planned）
- Runtime / Task Lifecycle 研究槽位（planned）
