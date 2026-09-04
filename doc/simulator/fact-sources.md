# Simulator Fact 数据来源

模拟器中的 Fact 现在明确分成三层，三层不会互相推导：

1. `rule.contract.facts`：规则侧 Contract，定义名称、类型、scope、上限和
   `description`。这是表达式和编辑器使用的文档元数据。
2. `factProviderDescriptor`、`objectFactProviderDescriptor`、
   `matchFactProviderDescriptor`：Provider 侧启动握手声明，分别对应 Tick、
   Object、Match。每个声明包含独立的 `id`、`version` 和 `facts`。
3. `tickFacts`、Ticket 的 `objectFacts` 以及 Match 的 `facts`：模拟器运行时值。
   这些值只用于模拟和观测，永远不会反向生成 Provider Descriptor。默认模拟器
   Provider 会在 Descriptor 中预先声明标准内置 Fact 能力；这是 Provider 能力超集，
   不是从某条规则的运行时值推导 Descriptor。

## Scenario JSON

Contract 声明了 Fact 的 scope 必须显式提供对应 Descriptor。下面的示例展示 Tick 的
静态运行时值和独立的握手声明：

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
      "facts": [{"name": "waitingCount", "type": "int64", "scope": "tick"}]
    },
    "tickFacts": {"int64s": {"waitingCount": 42}}
  }]
}
```

`description` 只应写在 `rule.contract.facts` 中。握手比较只检查 Fact 名称、类型、
scope 和 `maxValues`，不会要求 Provider Descriptor 携带或匹配描述文本。

Provider Descriptor 可以声明多于 Contract 的 Facts；Contract 只需选用其中的子集，
因此共享 Provider 的规则只修改 `rule.contract.facts` 就可以引用已公开的能力，不必
为每条规则重复编辑 Descriptor。默认模拟器场景和 Web mock 都公开下面三个标准名称，
兼容别名只由运行时识别，不重复写入 Descriptor：

| scope | 标准 Fact | 类型 | 单位与计算口径 |
| --- | --- | --- | --- |
| Tick | `waitingCount` | `int64` | 人数；匹配尝试开始时该 LogicalNode 中 active Ticket 数量 |
| Object | `waitingTime` | `int64` | 与 `Tick.Now`/`Ticket.CreatedAt` 相同的时间单位；`max(0, now-CreatedAt)`，HTTP 通常为 Unix 毫秒 |
| Match | `memberCount` | `int64` | 人数；本次 Match 实际接受的 Ticket 数，包含 seed |

`queueDepth`、`waiting-count` 是 `waitingCount` 的运行时兼容名称；`waitTime`、
`waiting-time`、`wait-time` 是 `waitingTime` 的运行时兼容名称。它们不会作为默认
Provider Descriptor 的重复广告。

如果 Contract 声明了某个 scope 的 Fact，却缺少对应 Descriptor，
`NewSimulator`、`ReplaceScenario` 和 `ValidateScenario` 都会失败，并返回
`MISSING_DESCRIPTOR`。自定义 Scenario 仍需显式提供 Descriptor；默认场景的标准内置
能力则已经在其 Descriptor 中公开，不需要再手工补齐。

## 模拟器内置 Match Fact

当 Contract 声明标准 `memberCount`（`int64`、`scope: "match"`）时，模拟器默认的 Match
Fact Provider 会计算它。其统计口径是本次 Match 中实际接受的 Ticket 数量，包含 seed
Ticket；每次候选通过 `CanJoin` 并成功加入后加一，未加入或被拒绝的候选不计入。Match
完成时该值应与 `len(Match.Tickets)` 以及 HTTP `memberCount` 一致。

规则如果需要在表达式中读取它，应在 Contract 中声明。默认模拟器的
`matchFactProviderDescriptor.facts` 已经公开该标准能力；自定义 Provider/Scenario 仍
应在自己的 Descriptor 中声明：

```json
{
  "name": "memberCount",
  "type": "int64",
  "scope": "match",
  "description": "本次 Match 中的 Ticket 成员数量（包含 seed）"
}
```

Provider Descriptor 可以同时声明该 Provider 能计算但规则不使用的其他 Facts；这类额外
声明不会自动写入 Contract。自定义 `MatchFactProvider` 时，Provider 仍负责返回完整的
Match Fact snapshot。

## HTTP 查询

`GET /api/v1/logical-nodes/facts` 返回四个并列字段：

- `contractFacts`：规则侧 Contract（`facts` 是兼容旧客户端的别名）；
- `providerDescriptors.tick/object/match`：三类握手声明；
- `runtimeFacts.tick`：模拟器的 Tick 运行时值。

Object 运行时值仍随 Ticket/Match member 返回，Match 运行时值仍随 Match 返回。这样
查看或编辑模拟数据时不会误把一个运行时样本当成 Provider 的能力声明。
