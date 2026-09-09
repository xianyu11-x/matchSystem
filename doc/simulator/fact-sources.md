# Simulator Fact 数据来源

模拟器中的 Fact 现在明确分成三层，三层不会互相推导；Provider 的实现绑定是场景配置，
不属于 Fact 值层：

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

## Python Provider 绑定

`rules[*].pythonProvider` 是可选的服务端实现绑定。它只决定该 LogicalNode 的三类 Provider
由一个进程内串行的 Python Worker 适配，不能替代 `factProviderDescriptor`、
`objectFactProviderDescriptor` 或 `matchFactProviderDescriptor`；Contract 使用的每个 Fact
仍必须在对应 Descriptor 中声明。一个 LogicalNode 一个 Worker，即使两个节点使用相同脚本
路径也不共享 Python 模块状态。

绑定字段为 `script`（必填，宿主文件路径）、`python`（可选解释器）、`runner`（可选宿主
JSON-lines runner）和 `timeoutMs`（每次调用毫秒数，0/省略为 2 秒默认值）。`python` 和
`runner` 的路径由模拟器宿主解析；路径字符串按 API 服务器文件系统解释，远程服务不能读取浏览器本机文件。启动时会加载脚本并完成
`handshake`；脚本必须提供 `provide(method, data)`。`tick`、`object`、`initialize`、`join`
的响应统一按 Contract 校验，协议错误、超时、异常或非法 Fact 会关闭 Worker，不自动回退
或重放。字段示例、协议帧和脚本见 [Python Provider 接入](python-provider.md)。

场景替换会在发布新 runtime 前启动并握手全部 Worker；任一启动或握手失败，旧 runtime
继续提供服务。成功替换和模拟器关闭会关闭旧 Worker。HTTP 场景校验对空脚本和负超时返回
`422 VALIDATION_FAILED`；宿主文件/解释器不可用或运行时 Python 错误目前由 HTTP 层返回
`500 PYTHON_PROVIDER_FAILED`，并关闭发生错误的 Worker。

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
Python 绑定本身随 `GET/PUT /api/v1/scenario` 的完整场景文档往返；Fact 元数据查询只
报告 Contract、Descriptor 和运行时值，不把脚本路径混入 Fact 能力目录。

## Web 表单入口

Rules → 全部 Facts 分别提供 Provider Descriptor 与 Tick 值表单；场景设置还可以编辑
LogicalNode 的 Python Provider 脚本、解释器、runner 和超时。空 Contract/Descriptor
也可启用提供器并添加声明，但有 Contract Fact 时仍须补齐对应 Descriptor。Contract Fact
的 description 在字段契约表单编辑。
声明不会反向生成运行值，运行值也不会生成声明。详见[规则与场景表单](rule-forms.md)。
