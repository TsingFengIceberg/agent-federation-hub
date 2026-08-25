# Agent Card 签名、规范化与缓存

> **日期**: 2026-08-26 | **状态**: draft | **证据状态**: verified（签名和缓存要求）/ inference（Registry 治理） | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

Agent Card 可使用 JWS 证明来源和内容完整性，但必须先按照字段存在性规则和 JCS 生成唯一 JSON 表示。HTTP 缓存降低重复发现成本，Registry 的信任、撤销、健康和质量治理则决定签名有效的卡片是否值得使用。

## 为什么签名前需要规范化

两个 JSON 对象即使语义相同，也可能因字段顺序、空格或数字表示不同而产生不同字节。若直接签原始文本，另一种语言重新序列化后就无法验证。

A2A 因此要求在签名前使用 RFC 8785 JSON Canonicalization Scheme（JCS）：对象属性按规则排序，字符串和数字使用一致表示，并移除无意义空白。规范见 [Agent Card Signing](../../../submodules/a2a/docs/specification.md#84-agent-card-signing)。

## 字段存在性规则

JCS 之前还要按 Protobuf presence 处理字段：

- Optional 字段未显式设置：省略；
- Optional 字段显式设置为默认值：保留；
- Required 字段：即使等于默认值也必须保留；
- 非 Required 的默认值或空 repeated 字段：按规范省略；
- `signatures` 字段本身必须排除，避免循环签名。

因此 `{}` 与 `{"streaming": false}` 在业务能力判断上可能接近，但签名 Payload 不一定相同。验证方不能先随意补默认字段再验签。

## JWS 签名结构

`AgentCardSignature` 使用 JWS JSON 形式：

| 字段 | 作用 |
|---|---|
| `protected` | Base64url 编码的 JWS Protected Header |
| `signature` | Base64url 编码的签名字节 |
| `header` | 可选、未保护的 JWS Header |

Protected Header 必须包含 `alg` 和 `kid`，`typ` 应为 `JOSE`，还可以用 `jku` 指向 JWKS。签名流程为：排除签名字段并规范化卡片，构造 Protected Header，组成 JWS Signing Input，再用提供方私钥签名。

## Client 验证流程

```text
receive Agent Card
  -> extract protected / signature
  -> resolve public key by kid from trusted store or secure JWKS
  -> reject expired or revoked key
  -> remove signatures and apply presence rules
  -> canonicalize with JCS
  -> verify JWS
  -> continue trust and admission evaluation
```

Client 应至少验证一个可接受签名后再信任卡片。卡片可以带多个签名，用于密钥轮换、多个信任主体或过渡期验证。

## 签名的信任边界

有效签名只证明卡片由对应私钥持有者签发且内容未被修改，不能证明：

- Skill 描述和输出质量真实；
- Endpoint 当前健康或满足 SLA；
- Agent 行为安全、没有恶意；
- 签名主体本身属于可信组织；
- 当前调用符合租户、数据和合规策略。

生产 Registry 仍需维护提供方身份、受信密钥、撤销状态、准入、健康、历史质量和风险策略。签名验证是信任链的一步，不是最终结论。

## HTTP 缓存

Agent Card 变化通常比调用频率低，Server 应使用标准 HTTP 缓存：

- `Cache-Control: max-age=...` 表示有效期；
- `ETag` 可以来自 Card 版本或内容 Hash；
- 可选 `Last-Modified` 表示最后修改时间。

缓存过期后，Client 应使用 `If-None-Match` 或 `If-Modified-Since` 条件请求。未变化时 Server 返回 `304 Not Modified`，避免重复下载。

## 缓存失效与隔离

卡片变化可能意味着 Endpoint、协议版本、Skill、扩展、媒体能力、安全方案或签名密钥变化，因此 Client 不应永久缓存。Registry 应保存卡片摘要、ETag、获取时间、签名验证结果和版本快照，并在重新路由新任务前刷新过期卡片。

Extended Agent Card 可能按身份、权限或租户返回不同内容，缓存必须至少按认证主体和权限范围隔离。认证会话中的扩展卡片不能作为公共卡片跨用户复用。

## QA / 讨论记录

### Q: HTTPS 已经保护传输，为什么还需要 Agent Card 签名？

> **状态**: verified | **来源**: security model

HTTPS 保护一次连接和 Server 身份；Card 签名可以在卡片被 Registry 缓存、转发或离线检查时继续验证签发来源和内容完整性。两者作用范围不同。

### Q: `ETag` 是否等于 AgentCard.version？

> **状态**: verified | **来源**: official specification

不一定。Server 可以从 Card 的 `version` 生成，也可以使用完整内容 Hash。内容 Hash 对未同步更新产品版本的卡片变化更敏感。

### Q: 签名验证成功后能否直接自动调用 Agent？

> **状态**: verified / inference | **来源**: protocol / platform reasoning

不能仅凭签名自动准入。还需验证签名主体是否可信、密钥是否撤销，以及权限、健康、质量和数据策略是否允许调用。

## 相关文档

- [AgentCard：发现、协商与信任边界](agent-card.md)
- [认证、授权与任务中授权](authentication-and-authorization.md)
- [A2A 版本协商与兼容性](versioning-and-compatibility.md)
- [A2A 即插即用与通用联邦平台](plug-and-play-federation.md)
