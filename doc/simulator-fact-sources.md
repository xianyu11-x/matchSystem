# Simulator Fact 数据来源

模拟器中的 Fact 现在明确分成三层，三层不会互相推导：

1. `rule.contract.facts`：规则侧 Contract，定义名称、类型、scope、上限和
   `description`。这是表达式和编辑器使用的文档元数据。
2. `factProviderDescriptor`、`objectFactProviderDescriptor`、
   `matchFactProviderDescriptor`：Provider 侧启动握手声明，分别对应 Tick、
   Object、Match。每个声明包含独立的 `id`、`version` 和 `facts`。
3. `tickFacts`、Ticket 的 `objectFacts` 以及 Match 的 `facts`：模拟器运行时值。
   这些值只用于模拟和观测，永远不会反向生成 Provider Descriptor。

## Scenario JSON

有 Fact 的 scope 必须显式提供对应 Descriptor。下面的示例展示 Tick 的静态运行时
值和独立的握手声明：

```json
{
  "schemaVersion": "simulator-scenario/v1",
  "physicalNodes": [{"id": "sim-1", "endpoint": "inproc://sim-1", "enabled": true}],
  "rules": [{
    "logicalNode": {"rule": {"namespace": "demo", "ruleId": 1}, "placementId": "default"},
    "physicalNodeId": "sim-1",
    "weight": 1,
    "enabled": true,
    "rule": "<match-rule/v1>",
    "factProviderDescriptor": {
      "id": "queue-provider",
      "version": "v2",
      "facts": [{"name": "queueDepth", "type": "int64", "scope": "tick"}]
    },
    "tickFacts": {"int64s": {"queueDepth": 42}}
  }]
}
```

`description` 只应写在 `rule.contract.facts` 中。握手比较只检查 Fact 名称、类型、
scope 和 `maxValues`，不会要求 Provider Descriptor 携带或匹配描述文本。

如果 Contract 声明了某个 scope 的 Fact，却缺少对应 Descriptor，
`NewSimulator`、`ReplaceScenario` 和 `ValidateScenario` 都会失败，并返回
`MISSING_DESCRIPTOR`。模拟器不再从 Contract 或运行时值自动补齐 Descriptor。

## HTTP 查询

`GET /api/v1/logical-nodes/facts` 返回四个并列字段：

- `contractFacts`：规则侧 Contract（`facts` 是兼容旧客户端的别名）；
- `providerDescriptors.tick/object/match`：三类握手声明；
- `runtimeFacts.tick`：模拟器的 Tick 运行时值。

Object 运行时值仍随 Ticket/Match member 返回，Match 运行时值仍随 Match 返回。这样
查看或编辑模拟数据时不会误把一个运行时样本当成 Provider 的能力声明。
