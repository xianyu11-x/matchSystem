# Simulator Fact 数据来源

模拟器中的 Fact 现在明确分成三层，三层不会互相推导；Provider 的实现绑定是场景配置，
不属于 Fact 值层。所有 Provider 的能力描述都来自启动阶段的自描述，并进入同一套现有
Contract 覆盖校验：Go Provider 在进程内调用 `Describe()`，Python Provider 通过 JSON-lines
`handshake` 返回 `Descriptors`。

1. `rule.contract.facts`：规则侧 Contract，定义名称、类型、scope、上限和
   `description`。这是表达式和编辑器使用的文档元数据。
2. `ProviderDescriptor`：Provider 侧启动自描述，分别对应 Tick、Object、Match。
   每个声明包含独立的 `id`、`version` 和 `facts`。Go Provider 的 `Describe()` 返回描述；
   Python Provider 的 JSON-lines `describe()` 返回描述。它们只保存在运行态，不写回场景 JSON。
3. `tickFacts`、Ticket 的 `objectFacts` 以及 Match 的 `facts`：模拟器运行时值。
   这些值只用于模拟和观测，永远不会反向生成 Provider Descriptor。默认模拟器
   Provider 会在握手 Descriptor 中声明标准内置 Fact 能力；这是 Provider 能力超集，
   不是从某条规则的 Contract 或运行时值推导 Descriptor。

在 Go 模拟器中，`RuleSpec.FactProvider`、`RuleSpec.ObjectFactProvider` 和
`RuleSpec.MatchFactProvider` 也分别使用 `matchsystem.TickProvider`、
`matchsystem.ObjectProvider` 和 `matchsystem.MatchProvider`。它们不是 `any` 动态入口；
旧的函数回调必须由宿主包装为同时实现业务方法和 `Describe()` 的明确类型，才能交给
`RuleSpec`。Python 绑定在运行时生成同样的三种强类型 Provider。

## Scenario JSON

Contract 声明的每个 Fact scope 都必须在最终有效 Descriptor 中有对应声明。场景只保存
Provider 实现绑定与静态运行时值，不保存能力 Descriptor：

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
    "pythonProvider": {
      "script": "C:\\matchsystem\\providers\\queue.py",
      "timeoutMs": 2000
    },
    "tickFacts": {"int64s": {"waitingCount": 42}}
  }]
}
```

规则文档的 `description` 应写在 `rule.contract.facts` 中；Provider Descriptor 也可以附带
独立的 `description` 作为提供方元数据。握手比较只检查 Fact 名称、类型、scope 和
`maxValues`，不会要求两者的描述文本一致。旧 JSON 中的三个 `*FactProviderDescriptor` 字段
属于未知属性，应被 Schema/API 拒绝，不能静默继续使用。

## Python Provider 绑定

`rules[*].pythonProvider` 是可选的服务端实现绑定。它只决定该 LogicalNode 的三类 Provider
由一个常驻的独立 Python Worker 进程适配。一个 LogicalNode 一个 Worker，即使两个节点使用
相同脚本路径也不共享 Python 模块状态。Provider 必须在启动握手中通过 `describe()` 按
实际能力返回 `tick`、`object`、`match` 范围的 Descriptor；Contract 使用的每个 scope
必须在对应描述中出现，Ready-only 旧脚本不能再从场景字段取得能力描述。

绑定字段为 `script`（必填，宿主文件路径）、`python`（可选解释器）、`runner`（可选宿主
JSON-lines runner）和 `timeoutMs`（每次调用毫秒数，0/省略为 2 秒默认值）。`python` 和
`runner` 的路径由模拟器宿主解析；路径字符串按 API 服务器
文件系统解释，远程服务不能读取浏览器本机文件。启动时会加载脚本并完成 `handshake`；脚本
必须提供 `provide(method, data)` 和 `describe()`。`tick`、`object`、`initialize`、`join` 的
业务响应统一按 Contract 校验，协议错误、超时、异常或非法 Fact 会关闭 Worker，不自动
回退或重放。字段示例、协议帧和脚本见
[Python Provider 接入](python-provider.md)。

仓库内建 Go Provider 的 Python 参考脚本见
[`examples/simulator/builtin_provider.py`](../../examples/simulator/builtin_provider.py)；它必须
通过 `pythonProvider.script` 显式绑定，省略 `runner` 使用生产内建 runner，删除绑定则保持默认
Go Provider。场景绑定的握手 `Input` 一次包含 `ContractFacts` 和静态 `TickFacts`，业务 `object`
请求另带 ObservationRegistry 的原始 `ObjectFacts`；参考脚本固定声明能力目录，只按 Contract
投影响应，不用 Contract 生成 Descriptor。

场景替换会在发布新 runtime 前启动并握手全部 Worker；握手中的 Descriptor 先与
`rule.contract.facts` 对齐，再交给核心现有 Contract 校验。只读 `ValidateScenario` 不启动
外部进程，因此不声称握手已经完成；脚本 Descriptor 的缺失、结构错误或与 Contract 不匹配
在实际 runtime 构造时报告。
任一启动、握手或 Contract 校验失败，旧 runtime 继续提供服务。成功替换和模拟器关闭会
关闭旧 Worker。HTTP 场景静态校验对空脚本、负超时返回 `422 VALIDATION_FAILED`；宿主
文件/解释器不可用、握手/Descriptor 错误或运行时 Python 错误由 HTTP 层返回
`500 PYTHON_PROVIDER_FAILED`，并关闭发生错误的 Worker。

Provider Descriptor 可以声明多于 Contract 的合法 Facts；Contract 只需选用其中的子集，
因此共享 Provider 的规则只修改 `rule.contract.facts` 就可以引用已公开的能力，不必
为每条规则重复编辑 Descriptor。这个“能力超集”只影响握手元数据：Python 业务响应仍由
当前规则 Contract 严格校验，返回未在 Contract 声明的 Fact 会失败。Go 内建 Provider 与仓库
中的 [Python 参考实现](../../examples/simulator/builtin_provider.py) 在自己的自描述 Descriptor
中公开下面五个标准名称；这些名称不是从 Contract 合成的：

| scope | 标准 Fact | 类型 | 单位与计算口径 |
| --- | --- | --- | --- |
| Tick | `waitingCount` | `int64` | 人数；匹配尝试开始时该 LogicalNode 中 active Ticket 数量 |
| Object | `preferredRoles` | `strings`，`maxValues: 3` | 只复制 Ticket 的 Object Facts，不凭空生成默认值 |
| Object | `latencyMs` | `int64` | 只复制 Ticket 的 Object Facts，不凭空生成默认值 |
| Object | `waitingTime` | `int64` | 与 `Tick.Now`/`Ticket.CreatedAt` 相同的时间单位；`max(0, now-CreatedAt)`，在 `int64` 上限饱和；HTTP 通常为 Unix 毫秒 |
| Match | `memberCount` | `int64` | seed 从 1 开始；每个成功加入的候选加 1，溢出时报错 |

Go 中的 `FactNameWaitingCount`/`FactNameWaitingTime` 只是代码常量别名；当前内建 Provider
只使用上表的规范 wire 名称，不会把 `queueDepth`、`waiting-count`、`waitTime`、
`waiting-time` 或 `wait-time` 自动合成到 Descriptor 或业务响应。使用这些名称的自定义
Provider 仍须在自己的 Contract 和 `Describe()` 中明确声明。

如果 Contract 声明了某个 scope 的 Fact，却缺少最终有效 Descriptor，核心会返回
`MISSING_DESCRIPTOR`。Go Provider 必须通过进程内 `Describe()`，Python Provider 必须通过
JSON-lines 启动握手提供对应 Descriptor；不能用 Contract 自身合成能力，也不能用旧场景字段补齐。
对 Python 绑定，`ValidateScenario`
不启动外部进程，缺失描述会在实际 runtime 构造/场景 PUT 阶段报告；这不是把草案当成已完成
握手。

## 模拟器内置 Match Fact

当 Contract 声明标准 `memberCount`（`int64`、`scope: "match"`）时，模拟器默认的 Match
Fact Provider 会计算它。其统计口径是本次 Match 中实际接受的 Ticket 数量，包含 seed
Ticket；每次候选通过 `CanJoin` 并成功加入后加一，未加入或被拒绝的候选不计入。Match
完成时该值应与 `len(Match.Tickets)` 以及 HTTP `memberCount` 一致。

规则如果需要在表达式中读取它，应在 Contract 中声明。默认模拟器的 Match Provider 握手
已经公开该标准能力；自定义 Provider 应在自己的握手 Descriptor 中声明：

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
- `providerDescriptors.tick/object/match`：三类有效启动自描述声明；每个存在的 Descriptor
  带 `source`，`python` 表示来自 Python Worker 启动握手，`go` 表示来自 Go Provider
  进程内 `Describe()`；
- `runtimeFacts.tick`：模拟器的 Tick 运行时值。

Object 运行时值仍随 Ticket/Match member 返回，Match 运行时值仍随 Match 返回。这样
查看或编辑模拟数据时不会误把一个运行时样本当成 Provider 的能力声明。
Python 绑定本身随 `GET/PUT /api/v1/scenario` 的完整场景文档往返；Fact 元数据查询只
报告 Contract、有效 Descriptor 和运行时值，不把脚本路径混入 Fact 能力目录。Descriptor
的 `source` 和内容只属于查询响应，GET/PUT 场景不会把它回填到任何持久化字段。

## Web 表单入口

Rules → 全部 Facts 展示 Contract、有效 Provider Descriptor 与 Tick 值；场景设置还可以
编辑 LogicalNode 的 Python Provider 脚本、解释器、runner 和超时。Descriptor 只由握手返回，
Facts 页以只读元数据显示。脚本可以声明 Contract 未使用的额外 scope/Fact 能力，这些能力会在 Descriptor
展示中保留，但业务响应仍只能返回当前 Contract 声明的字段。Contract Fact 的
`description` 在字段契约表单编辑。
声明不会反向生成运行值，运行值也不会生成声明。详见[规则与场景表单](rule-forms.md)。
