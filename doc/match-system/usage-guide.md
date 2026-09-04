# `internal/matchsystem` 使用指南

根包的生产接入顺序是：创建一个 `PhysicalNode` → `Load` 一个或多个
`LogicalNodeSpec` → 通过 `OwnerRef` 增删 Ticket → `BeginMatchRound` → 循环
`ProduceMatch`。所有调用应在同一 owner goroutine 串行执行。

## 1. 最小节点配置

每个规则只有一份 `match-rule/v1` RuleJSON。下面的文件内容声明一个按 `partition`
初筛的字符串索引和一个 Match Fact 计数器，可直接保存为 `rules/demo-1.json`：

```json
{
  "schemaVersion": "match-rule/v1",
  "ruleKey": {"namespace": "demo", "ruleId": 1},
  "contract": {
    "schemaVersion": "logical-node-contract/v3",
    "attributes": [{"name": "partition", "type": "strings", "maxValues": 1}],
    "facts": [{"name": "count", "type": "int64", "scope": "match"}],
    "indexes": [{"type": "multi_value", "name": "partition", "keyType": "string",
      "maxDocumentValues": 1, "maxQueryValues": 1}]
  },
  "prefilter": {
    "schemaVersion": "prefilter/v3",
    "bitmap": {"resultType": "bitmap", "expr": {
      "op": "lookup_string",
      "index": "partition",
      "values": {"schemaVersion": "expression-scalar/v3", "resultType": "strings",
        "expr": {"op": "strings_ref", "source": "seed_attributes", "name": "partition"}}
    }}
  },
  "evaluation": {
    "schemaVersion": "evaluation/v3",
    "canJoin": {"schemaVersion": "expression-scalar/v3", "resultType": "bool",
      "expr": {"op": "bool_literal", "value": true}},
    "canComplete": {"schemaVersion": "expression-scalar/v3", "resultType": "bool",
      "expr": {"op": "int64_gte",
        "left": {"op": "int64_ref", "source": "match_facts", "name": "count"},
        "right": {"op": "int64_literal", "value": 2}}}
  },
  "scoring": {"type": "created_at", "params": {"direction": "descending"}},
  "seedSelection": {"type": "oldest", "params": {}},
  "runtime": {
    "candidateScoringLimitPerSeed": 500,
    "candidateLimitPerSeed": 50,
    "maxPlayers": 2,
    "attemptLimitPerProduceMatch": 2,
    "attemptLimitPerMatchRound": 500
  }
}
```

宿主读取该文件后，将其作为 `LogicalNodeSpec.RuleJSON`，并用完整的 `LogicalNodeKey`
加载节点：

```go
ruleJSON, err := os.ReadFile("rules/demo-1.json")
if err != nil { return err }
spec := matchsystem.LogicalNodeSpec{
    Key: identity.LogicalNodeKey{
        Rule: identity.RuleKey{Namespace: "demo", RuleID: 1},
        PlacementID: "default",
    },
    RuleJSON:          ruleJSON,
    MatchFactProvider: matchFactProvider{},
    MatchFactProviderDescriptor: &matchsystem.ProviderDescriptor{
        ID:      "demo.match-counter",
        Version: "v1",
        Facts: []matchsystem.FactSpec{
            {Name: "count", Type: matchsystem.FactTypeInt64, Scope: matchsystem.FactScopeMatch},
        },
    },
}
```

`ruleKey` 必须与 `LogicalNodeKey.Rule` 完全一致；`PlacementID` 是部署拓扑信息，不属于
规则语义。`prefilter` 中的 `values` 仍是完整的 `expression-scalar/v3` envelope，不能
写成裸数组；其 `resultType` 必须与索引 key type 匹配。RuleJSON 的全部字段、内置评分和
Seed 选择和全部运行参数都由统一编译入口校验并固定到 LogicalNode。

## 2. 创建节点、投递 Ticket 和执行一轮

```go
ctx := context.Background()
physical, err := matchsystem.NewPhysicalNode("physical-demo",
    matchsystem.WithLogicalNodeSelector(matchsystem.NewLargestQueueLogicalNodeSelector()))
if err != nil { return err }
if err := physical.Load(ctx, spec); err != nil { return err }

owner := identity.OwnerRef{PhysicalNodeID: physical.ID(), LogicalNode: spec.Key}
for id, partition := range map[uint64]string{1001: "blue", 1002: "blue", 1003: "green"} {
    err := physical.Add(ctx, owner, &common.Ticket{
        TicketID: id,
        CreatedAt: int64(id),
        StringLists: map[string][]string{"partition": {partition}},
    })
    if err != nil { return err }
}

if err := physical.BeginMatchRound(ctx, 100); err != nil { return err }
for {
    result, err := physical.ProduceMatch(ctx)
    if errors.Is(err, matchsystem.ErrNoLogicalNodeAvailable) { break }
    if err != nil { return err }
    if result.Match == nil { continue } // 本次 seed 未形成完整组
    useMatch(result.LogicalNode, result.Match)
}
```

`BeginMatchRound` 只捕获开始时已经存在的 Ticket；从 `BeginMatchRound` 到本轮
`ProduceMatch` 消费完成期间不应调用 `Add`，后续 Ticket 应等待下一轮。当前实现不提供
同轮 Add 的缓冲队列。循环遇到
`ErrNoLogicalNodeAvailable` 表示当前没有可用 seed；`Match == nil` 不是错误，可能是
候选不足、`CanComplete` 未满足或本次尝试没有产出组。

## 3. MatchFactProvider 与内置评分

```go
type matchFactProvider struct{}

func (matchFactProvider) Initialize(context.Context, matchsystem.InitializeInput) (matchsystem.Facts, error) {
    return matchsystem.Facts{Int64Values: map[string]int64{"count": 1}}, nil
}

func (matchFactProvider) OnJoin(_ context.Context, in matchsystem.JoinInput) (matchsystem.Facts, error) {
    return matchsystem.Facts{Int64Values: map[string]int64{
        "count": in.MatchFactsBefore.Int64Values["count"] + 1,
    }}, nil
}

var matchFactProviderDescriptor = &matchsystem.ProviderDescriptor{
    ID:      "demo.match-counter",
    Version: "v1",
    Facts: []matchsystem.FactSpec{
        {Name: "count", Type: matchsystem.FactTypeInt64, Scope: matchsystem.FactScopeMatch},
    },
}
```

Provider 每次返回完整 Match Fact 层：Contract 中每个 `scope: match` Fact 都必须出现，
即使多值 Fact 为空也要提供空 slice。`seedEvaluator` 在接受 candidate 前 clone 返回值；
Provider 自己负责契约正确性，生产路径不重复做缺字段、类型/scope 或值上限校验。Provider
error、取消仍会 fail closed；Provider panic 不被捕获，直接传播。契约测试可显式使用
`fact.Validator` 检查返回快照。

`scoring` 由 `seedEvaluator` 用于从 Prefilter 候选中选出 bounded Top-L。内置类型包括：

- `constant`：返回 `params.value`；
- `created_at`：按 Ticket 的 `CreatedAt` 和 `direction` 评分，可选 `weight`；
- `int64_field`：读取 Contract 中声明的 int64 Attribute，按 `direction` 评分，可选
  `weight` 和缺失值的 `missingScore`。

评分只读取 seed/candidate Ticket、Tick/seed/candidate Object Fact 和固定的 `Now`，不能
读取 Match Fact 或已有成员；非有限分数会被拒绝。评分实现由 RuleJSON 编译并固定到
LogicalNode，宿主不提供另一个评分来源。

## 4. 独立示例：FactProvider 与 ObjectFactProvider

第 1 节的 `demo-1.json` 只声明了 `count` 这个 Match Fact，下面是另一个规则的独立
`LogicalNodeSpec` 字段片段，不应直接与前面的 `demo-1.json` 混用。对应规则的
`contract.facts` 至少应声明以下两个 Fact：

```json
[
  {"name": "waiting-count", "type": "int64", "scope": "tick"},
  {"name": "tier", "type": "int64", "scope": "object"}
]
```

```go
FactProvider: func(ctx context.Context, in matchsystem.TickFactInput) (matchsystem.Facts, error) {
    _ = ctx
    return matchsystem.Facts{Int64Values: map[string]int64{
        "waiting-count": int64(in.Node.WaitingCount),
    }}, nil
},
FactProviderDescriptor: &matchsystem.ProviderDescriptor{
    ID:      "demo.tick-facts",
    Version: "v1",
    Facts: []matchsystem.FactSpec{
        {Name: "waiting-count", Type: matchsystem.FactTypeInt64, Scope: matchsystem.FactScopeTick},
    },
},
ObjectFactProvider: func(ticket *common.Ticket, now int64, tick matchsystem.Facts, out matchsystem.ObjectFactWriter) error {
    _ = now
    _ = tick
    return out.SetInt64("tier", ticket.Int64Values["tier"])
},
ObjectFactProviderDescriptor: &matchsystem.ProviderDescriptor{
    ID:      "demo.object-facts",
    Version: "v1",
    Facts: []matchsystem.FactSpec{
        {Name: "tier", Type: matchsystem.FactTypeInt64, Scope: matchsystem.FactScopeObject},
    },
},
```

Fact Provider 每次 `ProduceMatch` 至多调用一次；Object Provider 在每个 generation 中按
TicketID 缓存，`seedEvaluator` 的 Prefilter、Scorer 和 Evaluation 复用同一个 slot。Writer
会复制 list 输入并拒绝未知名称、错误类型和超出 MaxValues 的写入；不同 Fact 层不能出现
同名键。Provider 不应保留或修改输入指针、Tick、Writer 或 Writer.Values。需要自动化检查
时，应在 Provider 契约测试中调用 `fact.Validator`，而不是把完整校验放进生产热路径。

`in.Node` 是本次回调的值快照，包含 `Key`、`State` 和 `WaitingCount`；它不提供
LogicalNode/Store 指针，也不允许 provider 通过回调重入节点。若不需要动态 Tick Facts，
Simulator 可通过 `RuleSpec.TickFacts` 提供静态快照。

## 5. 调度与生命周期

Seed 选择由 RuleJSON 的 `seedSelection` 编译，支持四种内置类型：`arrival` 按加入顺序、
`oldest` 按 `CreatedAt` 升序、`int64_priority` 读取 Contract 中声明的 int64 Attribute
并按方向排序、`random` 使用 `randomSeed` 生成可重放的确定性顺序。`runtime` 中的
`candidateScoringLimitPerSeed`（默认 500）限制参与评分的候选池，Prefilter 结果超出时按
DocID 升序截断；`candidateLimitPerSeed`（默认 Top-L 50）限制评分后保留的候选数。
`maxPlayers`、`attemptLimitPerProduceMatch` 和 `attemptLimitPerMatchRound` 也必须是正整数；
单次尝试上限不能超过整轮上限。

Seed runtime 在 Ticket Add/Remove/Commit 时维护各自的有序索引；`BeginMatchRound` 把
`attemptLimitPerMatchRound` 作为本轮有效 seed 上限，并启动策略自己的 stream，不会全量
物化 TicketID snapshot。`arrival` 使用 list cursor，`oldest`/`int64_priority` 从 heap
取出 entry 暂存，`random` 从 dense active 数组无放回移入 held。当前轮已经返回的 seed
不会重复；下一轮只恢复仍 active 的 held entry。正常编排契约是从 `BeginMatchRound` 到
本轮 `ProduceMatch` 消费完成期间不调用 `Add`，当前实现不提供 pending Add buffer。若
store lookup 防御性发现 TicketID 已失效，`nextSeed` 会跳过且不消耗有效 attempt；
Commit/Remove 会同步清理 active 或 held entry。轮次结束后重新 Add 的 TicketID 会被
runtime 作为新的 arrival/index entry，并在下一轮重新变为可选 seed。

`WithLogicalNodeSelector` 只选择跨 LogicalNode 的物理调度策略，不改变规则文件中的
Seed 选择。`BeginDrain` 后节点仍可运行当前 Ticket；只有 `Len()==0` 时 `Stop` 才成功。
`Get` 返回副本，`Remove` 对未知 TicketID 返回 false。

## 6. 错误处理检查表

- 加载失败：优先用 `errors.As`/`errors.Is` 检查 RuleJSON 各 section 的结构化错误和顶层
  状态错误；不要依赖错误字符串。
- 运行失败：Provider、内置评分、Evaluation、Fact 错误不会创建 Match；取消 context 直接停止。
  evaluator 不修改 Ticket 池，只有成功返回 Match 后才由 ticketStore 原子 Commit。
- Owner 错误：`OwnerRef` 的物理 ID 或 LogicalNodeKey 不匹配时不会触碰任何 Ticket。
- 轮次错误：未调用 `BeginMatchRound` 不能 `ProduceMatch`；一轮耗尽后需开始下一轮。

完整示例见 [cmd/app/main.go](../../cmd/app/main.go)；实现入口见
[LogicalNode](../../internal/matchsystem/logical_node.go) 和
[PhysicalNode](../../internal/matchsystem/physical_node.go)。
