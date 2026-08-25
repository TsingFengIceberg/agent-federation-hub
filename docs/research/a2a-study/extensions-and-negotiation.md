# A2A Extension 扩展与能力协商

> **日期**: 2026-08-26 | **状态**: draft | **证据状态**: verified（扩展机制）/ inference（平台治理） | **涉及版本**: `a2aproject/A2A@16ba526`

## 一句话结论

Extension 是 A2A 核心协议之外、由 URI 唯一标识的附加契约。Agent 在 Agent Card 中声明支持或要求哪些扩展，Client 在请求中明确选择，Message 或 Artifact 再用扩展 URI 和命名空间 metadata 携带具体数据；破坏性变更必须更换 URI，不能静默回退。

## 为什么需要 Extension

A2A 核心协议需要保持跨行业稳定，不可能预先定义医疗授权、科研引用、办公审批和代码补丁证明的全部字段。Extension 允许行业或厂商在不修改核心 Message、Task 和 Artifact 模型的情况下增加可协商语义，并为成熟能力进入未来核心规范提供试验路径。

可以将核心 A2A 看作标准合同，将 Extension 看作双方事先理解的附加条款。

## Agent Card 中的声明

Agent 通过 `capabilities.extensions` 发布 `AgentExtension`：

| 字段 | 作用 |
|---|---|
| `uri` | 全局唯一扩展标识及契约身份 |
| `description` | Agent 怎样使用该扩展 |
| `required` | Client 是否必须理解并遵守它 |
| `params` | Agent 对该扩展的配置参数 |

结构定义见 [a2a.proto](../../../submodules/a2a/specification/a2a.proto#L424)。URI 标识扩展契约，不表示 Client 可以从该地址自动下载并安全执行代码。

## 两层启用信息

Client 首先使用 `A2A-Extensions` Service Parameter 声明本次请求选择的扩展：

```http
A2A-Extensions: https://example.com/extensions/citations/v1
```

随后，具体 Message 或 Artifact 可以使用 `extensions` 列表标出参与解释的扩展，并以 URI 作为 metadata 命名空间：

```json
{
  "extensions": ["https://example.com/extensions/geolocation/v1"],
  "metadata": {
    "https://example.com/extensions/geolocation/v1": {
      "latitude": 31.2304,
      "longitude": 121.4737
    }
  }
}
```

两层不能混淆：Service Parameter 表示本次调用启用了哪些附加契约；对象上的 `extensions` 和 metadata 表示该对象具体携带了哪项扩展数据。

## Optional 与 Required

| Agent Card 声明 | Client 行为 | Server 行为 |
|---|---|---|
| `required: false` | 不声明支持 | 可以忽略扩展并继续核心 A2A |
| `required: false` | 声明兼容版本 | 双方按扩展契约处理 |
| `required: true` | 未声明支持 | 必须返回 `ExtensionSupportRequiredError` |
| `required: true` | 声明支持 | 才能继续处理请求 |

错误在 JSON-RPC、gRPC 和 HTTP+JSON 中分别映射为 `-32008`、`FAILED_PRECONDITION` 和 `400 Bad Request`。必需扩展不能被忽略，因为忽略后双方可能对业务含义产生不同理解。

## 扩展点

当前规范明确支持在 Message 与 Artifact 上标注扩展：

- Client Message 可以携带领域输入、位置或审批上下文；
- TaskStatus 中的 Message 可以携带领域进度信息；
- Artifact 可以携带引用、证据、内容分类或业务交付说明；
- metadata 保存扩展定义的数据，URI 防止不同扩展字段撞名。

扩展不应篡改核心 Task 状态的既有含义。例如自定义审批状态应放在扩展 metadata 中，而不能把 `COMPLETED` 重新解释为“等待审批”。

## 版本与兼容性

扩展 URI 应包含版本：

```text
https://example.com/extensions/citations/v1
https://example.com/extensions/citations/v2
```

破坏性变更必须创建新 URI。如果 Client 请求 `v2` 而 Server 只支持 `v1`，Server 不得自动按 `v1` 解释；可选扩展通常被忽略，必需扩展则返回错误。扩展规则见 [Extensions](../../../submodules/a2a/docs/specification.md#46-extensions)。

## Extension、metadata 与核心字段怎样选择

```text
所有 A2A 实现都需要的稳定语义
  -> 候选核心协议字段

一个行业或多家实现共同需要的契约
  -> 标准化 Extension

单个组织的临时附加信息
  -> 私有 Extension 或普通 metadata

Remote Agent 的内部 Prompt、节点和 Tool 状态
  -> 保留在内部，不应因方便而暴露
```

普通 metadata 只能传递双方带外约定的数据；Extension 额外提供可发现的 URI、能力声明、选择过程、必需性和版本边界。

## 安全与治理边界

支持扩展不等于信任扩展内容。实现仍应校验 schema、大小、URI allowlist、敏感字段、权限和日志脱敏。Hub 不应根据陌生 URI 自动加载插件或执行远程代码，也不应在代理转发时丢失 `A2A-Extensions`、对象扩展列表或命名空间 metadata。

扩展过多会导致生态碎片化。生产 Hub 应建立扩展目录，记录版本、Schema、所有者、安全等级、兼容矩阵和允许使用的租户，而不是把任意 URI 都当成可信标准。

## QA / 讨论记录

### Q: metadata 中放一个自定义字段是否就等于 Extension？

> **状态**: verified | **来源**: official specification / discussion

不等于。metadata 可以是双方私下约定的数据；Extension 还包含 Agent Card 声明、URI 身份、请求级选择、必需性和版本兼容契约。

### Q: `required: true` 是否表示 Client 必须安装 Server 提供的代码？

> **状态**: verified | **来源**: protocol boundary

不表示。它表示 Client 必须已经理解并遵守该 URI 标识的语义；A2A 不定义远程代码下载或安装机制。

### Q: Server 能否把 citations/v2 自动降为 citations/v1？

> **状态**: verified | **来源**: official specification

不能。破坏性版本使用不同 URI，Server 不得自动回退到旧扩展版本。

## 相关文档

- [AgentCard：发现、协商与信任边界](agent-card.md)
- [A2A 版本协商与兼容性](versioning-and-compatibility.md)
- [协议 Binding 与互操作](protocol-bindings.md)
- [认证、授权与任务中授权](authentication-and-authorization.md)
