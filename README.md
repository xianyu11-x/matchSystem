# MatchSystem 通用匹配模板（Prefilter）

这是一个进程内通用匹配池。索引初筛只通过 `prefilter` 子包中的声明式索引计划执行，最终正确性由 `GroupEvaluator`（组评估器）校验，建组只保留 Greedy（贪心）算法。

## 当前流程

外部 `MatchService.Tick(now, matchLimit)` 的建议调用方式：

1. 调用 `PhysicalNode.BeginMatchRound(ctx, now)`，为全部 LogicalNode 固化本轮 Seed 顺序并把各自游标置零；PhysicalNode 的逻辑节点选择游标继续自然轮询。
2. 外部 MatchService 按 `matchLimit` 循环调用 `PhysicalNode.ProduceMatch`；每次最多产出一个组。
3. 被选中的 LogicalNode 创建 Tick FactFrame，复制并固定本次产出尝试的 Tick Facts，同时准备索引。
4. 为 Seed 惰性生成一次 Object Facts，并作为 Prefilter 当前 Seed Facts 使用。
5. 扣除 Seed，为进入评分的 candidate 惰性生成并缓存 Object Facts，使用 FactView 执行 bounded Top-L。
6. GroupEvaluator 通过同一个 FactView 读取 Tick、Seed、candidate 和已有 group member 的 Facts，再由 Greedy GroupBuilder 建组。
7. 从 TicketStore、Active DocSet 和全部索引移除已匹配 Ticket；产出尝试结束后释放对象 Fact 缓存。同一 MatchService Tick 内，已经尝试过的 Seed 不会再次入选。

本包只提供调用原语，外部 MatchService 负责类似下面的一轮编排：

```go
if err := physical.BeginMatchRound(ctx, now); err != nil {
    return err
}
for len(matches) < matchLimit {
    result, err := physical.ProduceMatch(ctx)
    if errors.Is(err, matchsystem.ErrNoLogicalNodeAvailable) {
        break
    }
    if err != nil {
        return err
    }
    if result.Match != nil {
        matches = append(matches, result)
    }
}
```

每个 LogicalNode 的 `SeedSchedulerConfig` 同时提供单次调用和单轮 Seed 预算：`AttemptLimitPerProduceMatch` 限制一次 `ProduceMatch` 的有效 Seed 数，`AttemptLimitPerMatchRound` 跨同一轮多次调用累计并在下一次 `BeginMatchRound` 重置。两者取剩余值的较小者；stale/deleted Seed 不消耗预算，预算耗尽的 LogicalNode 不再进入 selector 候选。两个字段的 `<= 0` 默认均为 `500`，而且 BeginMatchRound 构建的 Seed 序列本身最多包含轮次上限个元素。自定义 `SeedOrderPolicy` 仍可看到完整 Candidates，通过 `SeedOrderContext.MaxSeeds` 从全池选择不超过上限的 Seed；内置策略则直接 bounded selection。

调度扩展见 [LogicalNode 负载均衡策略](doc/logical-node-selector.md) 和 [Seed 顺序策略与匹配轮次](doc/seed-order-policy.md)。

数据模型和生命周期见 [Ticket 生命周期、所有权与 DocID](doc/ticket-lifecycle.md) 以及 [Fact 生命周期、分层契约与缓存](doc/fact-lifecycle.md)。

候选路径没有 `CandidateFilter`、全池 Ticket 扫描或缺失索引时的扫描回退。

## 包边界

```text
internal/identity
  -> PhysicalNodeID、PlacementID、RuleKey、LogicalNodeKey、OwnerRef

internal/common
  -> 唯一的 Ticket 定义，以及跨客户端与 MatchService 边界的 Match、Endpoint、Route DTO
  -> 不包含 LogicalNode 内部 DocID

internal/client
  -> 不可变 RouteTable、ClientRouter、PhysicalNode 选择和 OwnerRef 解析

internal/matchsystem
  -> PhysicalNode、LogicalNodeSelector 和单 owner goroutine 执行边界
  -> LogicalNode 直接持有 TicketStore、索引、规则和匹配状态
  -> Tick FactFrame、ObjectFactProvider、FactView
  -> 通过 common.Ticket 类型别名执行 Top-L、GroupEvaluator、Greedy GroupBuilder
  -> 仅提供单组产出和 Seed 游标重置；不实现 MatchService、TickRunner、RateLimiter client、网络或 IO

internal/matchsystem/fact
  -> 与 Prefilter/评分/评估解耦的 Fact Values、Spec、Type、Frame、View、Provider、校验和深拷贝

internal/matchsystem/prefilter
  -> 过滤表达式、IndexQuery、动态值、Compiler、Requirements
  -> MultiValueIndex、Int64RangeIndex、Roaring Bitmap、TickSession
```

`prefilter` 直接读取 `common.Ticket`，本地 `uint32 DocID` 作为独立参数传入，不再定义或构造第二套 Document 投影。

依赖方向保持为 `client -> identity/common`、`matchsystem -> identity/common/fact/prefilter`、`prefilter -> common/fact`。`client` 与 `matchsystem` 不互相导入。

MultiValueIndex 原生支持两种 KeyType：

- `KeyTypeString`：读取 `Ticket.StringLists map[string][]string`，也是默认类型。
- `KeyTypeUint64`：读取 `Ticket.Uint64Lists map[string][]uint64`，通过 `Uint64Query` 查询。

```go
uint64Index := prefilter.NewMultiValueIndex(prefilter.MultiValueIndexConfig{
    Name:    "uint64_dimension_index",
    Field:   "uint64_dimension",
    KeyType: prefilter.KeyTypeUint64,
})

uint64Lookup := prefilter.Lookup(prefilter.Uint64Query{
    Index:  "uint64_dimension_index",
    Values: prefilter.SeedUint64s("uint64_dimension"),
})
```

## 最小用法

```go
indexes := []prefilter.IndexSpec{
    prefilter.NewMultiValueIndex(prefilter.MultiValueIndexConfig{
        Name:                "dimension_a_index",
        Field:               "dimension_a",
        MaxDocumentValues: 64,
        MaxQueryValues:    64,
    }),
}

root := prefilter.Lookup(prefilter.StringQuery{
    Index:  "dimension_a_index",
    Values: prefilter.SeedStrings("dimension_a"),
})

startRule := matchsystem.FuncGroupEvaluator{
    EvaluatorFlagsValue: matchsystem.GroupEvaluatorStart,
    AllowFn: func(
        _ matchsystem.GroupEvaluatorContext,
        group []*matchsystem.Ticket,
        _ *matchsystem.Ticket,
    ) bool {
        return len(group) >= 4
    },
}

rules := matchsystem.NewRuleSet(startRule).WithCandidateScore(
    func(_ *matchsystem.Ticket, candidate *matchsystem.Ticket, now int64) float64 {
        return float64(now - candidate.CreatedAt)
    },
)

node, err := matchsystem.NewLogicalNode(matchsystem.LogicalNodeSpec{
    Key: logicalNodeKey,
    Config: matchsystem.LogicalNodeConfig{
        MaxPlayers: 4,
        GroupBuilder: matchsystem.GroupBuilderConfig{
            CandidateLimitPerSeed: 128,
        },
        Prefilter: prefilter.Config{
            Indexes: indexes,
            Root: root,
        },
    },
    Rules: rules,
})
if err != nil {
    return err
}

_, err = node.Add(&matchsystem.Ticket{
    TicketID:  1001,
    CreatedAt: now,
    StringLists: map[string][]string{
        "dimension_a": {"value_a", "value_b"},
    },
    Int64Values: map[string]int64{
        "numeric_value": 1,
    },
})
if err != nil {
    return err
}

if err := node.BeginMatchRound(now); err != nil {
    return err
}
match, err := node.ProduceMatch(matchsystem.Facts{})
```

Index/Fact 契约和 Prefilter 计划使用两份独立 JSON。必须先加载契约，再用冻结后的契约校验并编译计划：

```json
{
  "schemaVersion": "logical-node-contract/v1",
  "indexes": [
    {"type":"multi_value","name":"dimension_a_index","field":"dimension_a","keyType":"string","maxDocumentValues":64,"maxQueryValues":64}
  ],
  "facts": [
    {"name":"wait_millis","type":"int64"}
  ]
}
```

```go
contract, err := prefilter.ParseLogicalNodeContract(contractJSON, prefilter.JSONLimits{})
if err != nil {
    return err
}
jsonCompiler, err := prefilter.NewJSONCompiler(contract)
if err != nil {
    return err
}

config, err := jsonCompiler.Parse(planJSON)
// 或 plan, err := jsonCompiler.Compile(planJSON)
```

构造 LogicalNode 时，契约 Facts 属于整个评估链，而不是 Prefilter 私有配置：

```go
nodeConfig := matchsystem.LogicalNodeConfig{
    Facts:     contract.Facts,
    Prefilter: config,
}
```

`logical-node-contract/v1` 定义整个匹配链可用的索引和 Fact；`prefilter/v1` 只定义 `plan` 和可选 `runtime.containsProbeThreshold`。两种 JSON 不能混用。计划引用契约外的名字或错误类型，会在 JSON 校验阶段返回带 JSON Path 的错误。generation 热更新尚未接入。

## 全链路 Tick/Object Facts

`FactProvider` 每次 `ProduceMatch` 生成一次共享 Facts。这里有意不把 FactFrame 提升到整轮缓存：前一次成功匹配可能改变外部容量等动态 Facts；同时 Object Facts 可以依赖 Tick Facts，整轮保留还可能把已评估的大量 Ticket Facts 一直留到轮次结束。`ObjectFactProvider` 在 Ticket 首次作为 seed 或 candidate 被使用时生成一次 Object Facts，并按 TicketID 缓存到本次产出尝试结束：

```go
spec.ObjectFactProvider = func(
    object *matchsystem.Ticket,
    now int64,
    tickFacts matchsystem.Facts,
) (matchsystem.Facts, error) {
    return matchsystem.Facts{
        Int64Values: map[string]int64{
            "wait_millis": now - object.CreatedAt,
        },
    }, nil
}
```

评分和最终评估读取同一个只读 FactView；示例中的 `priority`、`tier` 也必须预先出现在 `LogicalNodeConfig.Facts` / 契约 JSON 中：

```go
rules.WithCandidateScoreContext(func(ctx matchsystem.CandidateScoreContext) float64 {
    candidateFacts, _ := ctx.Facts.For(ctx.Candidate)
    return float64(candidateFacts.Int64Values["priority"])
})

evaluator := matchsystem.FuncGroupEvaluator{
    EvaluatorFlagsValue: matchsystem.GroupEvaluatorJoin,
    AllowFn: func(ctx matchsystem.GroupEvaluatorContext, group []*matchsystem.Ticket, candidate *matchsystem.Ticket) bool {
        seedFacts, _ := ctx.Facts.For(ctx.Seed)
        candidateFacts, _ := ctx.Facts.For(candidate)
        return seedFacts.Int64Values["tier"] == candidateFacts.Int64Values["tier"]
    },
}
```

`SeedFactProvider` 作为兼容字段保留，但执行语义已经升级为 ObjectFactProvider；两者不能同时设置。

## 单协程执行契约

整套可变匹配状态采用 single-owner goroutine（单所有者协程）模型，不使用 mutex、RWMutex、channel 或 atomic state handoff（原子状态交接）。每个 ClientRouter 实例由调用节点自己的一个 owner goroutine 驱动；每个 MatchService 及其唯一 PhysicalNode 由同一个 owner goroutine 顺序驱动，并同步调用 LogicalNode 和 Prefilter。尤其不能把同一 PhysicalNode 的 Add、Remove、Get、BeginMatchRound、ProduceMatch 和 MatchService.Tick 分派到不同 goroutine。

如果服务器入口存在网络并发，必须在进入本仓库的匹配核心之前完成串行化；核心 API 自身不提供并发保护，也不允许回调重入。LogicalNode 的 FactFrame 深拷贝 Tick/Object Facts，并按 TicketID 在本次 `ProduceMatch` 缓存；Prefilter 的 `TickSession` 仍只是索引执行视图，不是并发数据快照。

`TicketID` 使用 `uint64`，是跨边界稳定的业务身份；`DocID` 仍是 LogicalNode 本地的 `uint32` 索引编号。Ticket 从 Store 和全部索引删除后，DocID 进入空闲栈并由后续 Add 优先复用。这个回收策略依赖上述 owner 契约：Add/Remove 只能发生在两轮之间，不能插入已经开始消费的 SeedRound，否则复用的 DocID 可能与旧 Seed 条目混淆。

`common.Ticket` 是唯一 Ticket 数据模型，`matchsystem.Ticket` 只是它的类型别名。Add 对调用方 Ticket 深拷贝一次，池内通过不导出的 `storedTicket` 保存 `*common.Ticket + DocID + arrivalIndex`；Get 返回池内 Ticket 的独立深拷贝，调用方可以修改或跨命令持有返回值，不会影响池内状态；匹配流程内部通过不导出的 `lookupTicket` 借用池内指针，匹配成功时先从 Store 和索引移除成员，再把同一批 Ticket 指针转移给 `common.Match`，结果调用方取得完整所有权。Remove 只删除并丢弃池内对象，不转移所有权。

## 验证

```bash
go test ./...
go vet ./...
go build ./...
```
