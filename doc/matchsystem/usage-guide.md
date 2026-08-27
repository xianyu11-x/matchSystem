# `internal/matchsystem` 使用指南

根包的生产接入顺序是：创建一个 `PhysicalNode` → `Load` 一个或多个
`LogicalNodeSpec` → 通过 `OwnerRef` 增删 Ticket → `BeginMatchRound` → 循环
`ProduceMatch`。所有调用应在同一 owner goroutine 串行执行。

## 1. 最小节点配置

下面的 Contract 声明一个按 `partition` 初筛的字符串索引和一个 Match Fact 计数器：

```go
spec := matchsystem.LogicalNodeSpec{
    Key: identity.LogicalNodeKey{
        Rule: identity.RuleKey{Namespace: "demo", RuleID: 1},
        PlacementID: "default",
    },
    ContractJSON: []byte(`{
      "schemaVersion":"logical-node-contract/v3",
      "attributes":[{"name":"partition","type":"strings","maxValues":1}],
      "facts":[{"name":"count","type":"int64","scope":"match"}],
      "indexes":[{"type":"multi_value","name":"partition",
        "keyType":"string","maxDocumentValues":1,"maxQueryValues":1}]
    }`),
    PrefilterJSON: []byte(`{
      "schemaVersion":"prefilter/v3",
      "bitmap":{"resultType":"bitmap","expr":{
        "op":"lookup_string","index":"partition","values":{
          "schemaVersion":"expression-scalar/v3","resultType":"strings",
          "expr":{"op":"strings_ref","source":"seed_attributes","name":"partition"}
        }
      }}
    }`),
    EvaluationJSON: []byte(`{
      "schemaVersion":"evaluation/v3",
      "canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool",
        "expr":{"op":"bool_literal","value":true}},
      "canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool",
        "expr":{"op":"int64_gte",
          "left":{"op":"int64_ref","source":"match_facts","name":"count"},
          "right":{"op":"int64_literal","value":2}}}
    }`),
    CandidateScorer: func(ctx matchsystem.CandidateScoreContext) (float64, error) {
        return float64(ctx.Candidate.CreatedAt), nil
    },
    MatchFactProvider: matchFactProvider{},
    Config: matchsystem.LogicalNodeConfig{MaxPlayers: 2},
}
```

`PrefilterJSON` 中的 `values` 是完整的 `expression-scalar/v3` envelope，不是裸数组；
其 `resultType` 必须与索引 key type 匹配。Evaluation 的两个字段都必须存在且 root
为 Bool。生产配置不接受 pre-v3、typed Builder 或运行时注册表。

## 2. 创建节点、投递 Ticket 和执行一轮

```go
ctx := context.Background()
physical, err := matchsystem.NewPhysicalNode("physical-demo",
    matchsystem.WithLogicalNodeSelector(matchsystem.NewLargestQueueLogicalNodeSelector()))
if err != nil { return err }
if err := physical.Load(ctx, spec); err != nil { return err }

owner := identity.OwnerRef{PhysicalNodeID: physical.ID(), LogicalNode: spec.Key}
for id, partition := range map[uint64]string{1001: "blue", 1002: "blue", 1003: "green"} {
    _, err := physical.Add(ctx, owner, &common.Ticket{
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

`BeginMatchRound` 只捕获开始时已经存在的 Ticket；后续 `Add` 应等待下一轮。循环遇到
`ErrNoLogicalNodeAvailable` 表示当前没有可用 seed；`Match == nil` 不是错误，可能是
候选不足、`CanComplete` 未满足或本次尝试没有产出组。

## 3. MatchFactProvider 与 scorer

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
```

Provider 每次返回完整 Match Fact 层：Contract 中每个 `scope: match` Fact 都必须出现，
即使多值 Fact 为空也要提供空 slice。LogicalNode 在接受 candidate 前校验并深拷贝返回值；
Provider error、panic、取消、缺字段或类型/scope 错误都会 fail closed。

Scorer 只用于从 Prefilter 候选中选出 bounded Top-L。它可以读取 seed/candidate Ticket、
Tick/seed/candidate Object Fact 和固定的 `Now`，不能读取 Match Fact 或已有成员；返回
NaN/Inf 会被拒绝。

## 4. FactProvider 与 ObjectFactProvider

```go
FactProvider: func(ctx context.Context, now int64) (matchsystem.Facts, error) {
    return matchsystem.Facts{Int64Values: map[string]int64{"capacity": 10}}, nil
},
ObjectFactProvider: func(ticket *common.Ticket, now int64, tick matchsystem.Facts) (matchsystem.Facts, error) {
    return matchsystem.Facts{Int64Values: map[string]int64{
        "tier": ticket.Int64Values["tier"],
    }}, nil
},
```

Fact Provider 每次 `ProduceMatch` 至多调用一次；Object Provider 在同一次调用中按
TicketID 缓存，Prefilter、Scorer 和 Evaluation 复用同一份 Frame。返回值按 Contract
验证；不同 Fact 层不能出现同名键。Provider 不应保留输入指针或修改输入快照。

## 5. 调度与生命周期

```go
spec.Config = matchsystem.LogicalNodeConfig{
    MaxPlayers: 4,
    GroupBuilder: matchsystem.GroupBuilderConfig{CandidateLimitPerSeed: 64},
    SeedScheduler: matchsystem.SeedSchedulerConfig{
        AttemptLimitPerProduceMatch: 32,
        AttemptLimitPerMatchRound: 500,
        Order: matchsystem.SeedOrderPolicyConfig{
            Kind: matchsystem.SeedOrderInt64Priority,
            PriorityField: "rating",
            PriorityDirection: matchsystem.SeedPriorityDescending,
        },
    },
}
```

可通过 `WithLogicalNodeSelector` 选择跨 LogicalNode 的策略；Seed 策略通过
`SeedOrderPolicyConfig` 或 `LogicalNodeSpec.SeedOrderPolicy` 配置。`BeginDrain` 后节点仍
可运行当前 Ticket；只有 `Len()==0` 时 `Stop` 才成功。 `Get` 返回副本，`Remove`
对未知 TicketID 返回 false。

## 6. 错误处理检查表

- 加载失败：优先用 `errors.As`/`errors.Is` 检查 Contract、Prefilter、Evaluation
  的结构化错误和顶层状态错误；不要依赖错误字符串。
- 运行失败：Provider、Scorer、Evaluation、Fact 错误不会创建 Match；取消 context 直接停止。
- Owner 错误：`OwnerRef` 的物理 ID 或 LogicalNodeKey 不匹配时不会触碰任何 Ticket。
- 轮次错误：未调用 `BeginMatchRound` 不能 `ProduceMatch`；一轮耗尽后需开始下一轮。

完整示例见 [cmd/app/main.go](../../cmd/app/main.go)；实现入口见
[LogicalNode](../../internal/matchsystem/logical_node.go) 和
[PhysicalNode](../../internal/matchsystem/physical_node.go)。
