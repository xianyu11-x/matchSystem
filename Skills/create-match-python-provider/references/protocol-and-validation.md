# 生产 Python Provider 协议与验证

本参考描述 `simulator-scenario/v1` 与 `match-rule/v1` 当前接入。若运行的模拟器版本不同，以其实际契约为准；有源码时可对照仓库 `doc/simulator/python-provider.md`、`doc/simulator/timeline-replay.md`、`api/schema/simulator-scenario/v1.schema.json` 和 `internal/simulator/python_provider.go`。

## 声明、回调与值

内建 runner 在模块加载后要求 `describe()`、`provide(method, data)` 均可调用，握手时先把 `Input` 注入模块全局 `_provider_context`，再调用无参数的 `describe()`。模块顶层加载时该变量尚不存在。场景绑定的上下文含 `ContractFacts`（每项 `Name`、`Type`、`Scope`、可选 `MaxValues`）与静态 `TickFacts`；通用入口可能传入 `None`。上下文可用于响应投影，不能当作脚本真实能力的来源。

`describe()` 返回如下形状，顶层 scope、Descriptor 与 Fact 字段均为小写；`id` 和 `version` 非空且稳定：

```python
def describe():
    return {"match": {"id": "simulator.match-facts", "version": "v1", "facts": [
        {"name": "memberCount", "type": "int64", "scope": "match"}
    ]}}
```

只能使用 `tick`、`object`、`match` 键，至少声明一个 scope；仅需声明实现能提供的范围。每个 Contract Fact 的名称、类型、scope、`maxValues` 必须与对应 Descriptor 完全一致。`strings`、`uint64s` 的 `maxValues` 是正数；`int64` 不带该字段。可声明额外真实能力，但不能因此返回规则未声明的业务值。不要向场景写入 Descriptor 或 `source`。

`provide` 的 `data` 是本次只读输入，返回值直接是 Fact map，不包含 `Version` 或 `Facts` 外壳：

| method | 作用域与主要输入 | 预期结果 |
| --- | --- | --- |
| `tick` | Tick；`Now`、`Node.WaitingCount` | 当前 Tick Facts。静态值来自 `_provider_context.TickFacts`，只在需要时使用 |
| `object` | Object；`Ticket`、`Now`、`TickFacts`、`ObjectFacts` | 当前 Ticket 的 Object Facts；`Ticket` 中有 `TicketID`、`CreatedAt`、`StringLists`、`Uint64Lists`、`Int64Values`；`ObjectFacts` 是宿主登记的原始观察值 |
| `initialize` | Match；`Now`、`SeedAttributes`、`SeedFacts`、`TickFacts` | 以 seed 初始化的完整 Match Fact 快照 |
| `join` | Match；除 seed、Tick 和时间外，还有 `Candidate`、`CandidateFacts`、`MatchFactsBefore` | 在加入前快照上结合候选人计算后的完整 Match Fact 快照 |

回调数据中的 Fact 值及返回值用 `{"StringLists":{},"Uint64Lists":{},"Int64Values":{}}` 形状；不含值的 map 可省略。`strings` 放 `StringLists`，`uint64s` 放 `Uint64Lists`，`int64` 放 `Int64Values`。同名值不能出现在多个 map、错误 scope 或规则未声明的输出中。集合长度不得超过上限；值必须符合对应整型范围。`initialize` 与 `join` 每次都包含所有 Match Fact，空集合也要有键；不得把响应当作 patch 或依赖旧快照自动补全。`join` 在候选通过 `canJoin` 后调用。

宿主输入中的列表型 Fact 即使键存在，也可能为 `null`（Python 的 `None`，表示空集合）；先检查类型 map 与键是否存在，再将已存在的 `None` 当作空列表处理，返回空集合时保留键并使用 `[]`。

仅当规则及语义与模拟器标准实现一致时，以下事实可直接采用：`waitingCount`（tick/int64）取当前活动 Ticket 数；`preferredRoles`（object/strings，maxValues 3）和 `latencyMs`（object/int64）复制 `ObjectFacts` 中已有值，不生成默认值；`waitingTime`（object/int64）为 `max(0, Now - Ticket.CreatedAt)`，在 int64 最大值饱和；`memberCount`（match/int64）seed 为 1、每次成功加入加 1，溢出报错。`Now` 是调用者提供的逻辑时间，HTTP 模拟器通常使用 Unix 毫秒。历史名称不是自动别名。

## 真实 JSON-lines 封装

生产 runner 负责 UTF-8、逐行收发及外层包装；用户脚本只提供上面两个函数，不必自行读取 stdin。外层字段区分大小写，版本固定为整数 1：

```jsonl
{"Version":1,"Method":"handshake","Input":{"ContractFacts":[{"Name":"memberCount","Type":"int64","Scope":"match"}],"TickFacts":{"StringLists":{},"Uint64Lists":{},"Int64Values":{}}}}
{"Version":1,"Ready":true,"Descriptors":{"match":{"id":"simulator.match-facts","version":"v1","facts":[{"name":"memberCount","type":"int64","scope":"match"}]}}}
{"Version":1,"Method":"initialize","Input":{"Now":1000,"SeedAttributes":{},"SeedFacts":{},"TickFacts":{}}}
{"Version":1,"Facts":{"Int64Values":{"memberCount":1}}}
{"Version":1,"Method":"join","Input":{"MatchFactsBefore":{"Int64Values":{"memberCount":1}}}}
{"Version":1,"Facts":{"Int64Values":{"memberCount":2}}}
```

这些行展示请求、响应交替，并非可以直接运行的单向输入文件；真实输入还包括该回调的其他字段。握手一次完成，`Ready` 必须伴随非空 `Descriptors`；自定义 runner 也必须实现此协议。生产 runner 将脚本普通 stdout 重定向到 stderr；协议输出保留给 runner。单帧上限 1 MiB；异常、超时、协议错误或非法事实会关闭 Worker，不自动回退或重放。普通脚本不要依赖研究用 Ready-only runner。

## 部署与检查

1. 将脚本放到运行模拟器 API 的机器上。Web/桌面打开“运行总览”，在拓扑面板编辑目标物理节点的逻辑部署，设置 Python Provider 的 `script` 为服务端可读的绝对路径；如需指定可用 Python 解释器则填 `python`。省略 `runner` 使用生产内建 runner，`timeoutMs` 为业务调用毫秒，0 或省略为 2000；握手时间至少 2000 ms。远程浏览器的本机路径不能直接用于服务器。
2. 或对独立测试实例的 `PUT /api/v1/scenario` 提交 `{"scenario": <完整场景>}`，把 `{"script":"服务器绝对路径","timeoutMs":2000}` 放入对应 `rules[*].pythonProvider`，该项的 `rule` 为实际 RuleJSON，`logicalNode.rule.ruleId` 与规则 ID 对齐。`tickFacts` 属于场景静态值，JSON 形状是 `strings`、`uint64s`、`int64s`，与 Worker 的 PascalCase 不同。保存部署可能清除该部署的等待 Ticket；整场景替换会重建场景，故演示和验证应使用独立实例。失败时原场景保留。
3. 查询 `GET /api/v1/logical-nodes/facts?ruleId=91002&placementId=1`，把两个身份替换为实际部署的正十进制 ID。核对 `contractFacts` 与需要由 Python 提供的各 scope 的 `providerDescriptors`，确认其 `source: "python"`、id/version 和各 Fact；未用到的 scope 可能显示内建 Go Provider。静态校验或脚本能导入不等于握手成功。以规则需要的 Ticket 属性和 Object Facts 提交代表性数据，按规则执行轮次并查看 Match、成员和聚合 Facts。无匹配也可能来自规则的预筛选或完成条件，应分别检查而不是认定 Provider 成功。
4. 做时间线回放时，先把候选规则保存到规则库并提取完整源节点时间线，再在回放界面选择候选规则及作为 Python 来源的现有部署；HTTP `POST /api/v1/replay/run` 接收 `timelineJson`、`targetRuleId`、可选 `providerLogicalNode`。例如 `providerLogicalNode` 的形状为 `{"rule":{"namespace":"fifo","ruleId":"91002"},"placementId":"1"}`；替换为提供 Python 绑定的实际部署。`targetRuleId` 是规则库中的十进制字符串，`timelineJson` 保持提取时的原始 JSON 文本，以免损失 64 位整数精度。未指定来源默认使用独立内建 Provider；指定 Python 来源时回放单独启动 Worker。候选 Fact 必须与源配置匹配名称、类型、scope、上限，所需 Object 输入须有历史记录，源记录中的各 scope Descriptor ID、version 及能力也须与候选兼容。不要只为了通过校验而伪造身份或变更语义。回放返回 200 仍须检查结果 `status` 是 `completed` 还是 `interrupted`。

如果没有可启动的模拟器，至少检查语法、导入并直接调用 `describe` 和相关 `provide` 分支；覆盖初始化、连续加入、缺失输入、数值上下限、集合上限等与该实现相关的情况。报告模块自测结果，并明确未验证真实握手、场景绑定或回放。若有可用的独立实例，在真实 runtime 中完成上述握手和业务验证。
