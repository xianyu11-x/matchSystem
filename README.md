# MatchSystem 通用匹配模板（Prefilter）

这是一个进程内通用匹配池。索引初筛只通过 `prefilter` 子包中的声明式索引计划执行，最终正确性由 `GroupEvaluator`（组评估器）校验，建组只保留 Greedy（贪心）算法。

## 当前流程

每次 `Tick(now)`：

1. 在 owner goroutine 中创建 `TickSession`，固定本轮的 `now` 和 Fact，并准备索引。
2. 为每个 seed 执行编译后的 Prefilter。
3. 扣除 seed 和本 Tick 已使用的 DocID。
4. 使用 bounded Top-L（有界堆）保留最高分候选。
5. 物化 Top-L Ticket，并由 GroupEvaluator 和 Greedy GroupBuilder 建组。
6. 从 TicketStore、Active DocSet 和全部索引移除已匹配 Ticket。

候选路径没有 `CandidateFilter`、全池 Ticket 扫描或缺失索引时的扫描回退。

## 包边界

```text
internal/identity
  -> PhysicalNodeID、PlacementID、RuleKey、LogicalNodeKey、OwnerRef

internal/common
  -> 跨客户端与 MatchService 边界的 Ticket、Match、Endpoint、Route DTO
  -> 不包含 LogicalNode 内部 DocID

internal/client
  -> 不可变 RouteTable、ClientRouter、PhysicalNode 选择和 OwnerRef 解析

internal/matchsystem
  -> PhysicalNode、LogicalNodeSelector 和单 owner goroutine 执行边界
  -> LogicalNode 直接持有 TicketStore、索引、规则和匹配状态
  -> Ticket、Top-L、GroupEvaluator、Greedy GroupBuilder
  -> PhysicalNode 是匹配核心最高实现层；不包含 TickRunner 或 RateLimiter client

internal/matchsystem/prefilter
  -> 过滤表达式、IndexQuery、动态值、Compiler、Requirements
  -> MultiValueIndex、Int64RangeIndex、Roaring Bitmap、TickSession
```

`prefilter` 只读取通用 `Document` 投影，不依赖上层 `matchsystem.Ticket`。

依赖方向保持为 `client -> identity/common`、`matchsystem -> identity/common/prefilter`。`client` 与 `matchsystem` 不互相导入。

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
    TicketID:  "ticket-1",
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

matches, err := node.Tick(now)
```

JSON 解析与热更新尚未接入；当前计划由 Go API 构造，并在 `NewLogicalNode` 时完成严格编译。

## 单协程执行契约

整套可变匹配状态采用 single-owner goroutine（单所有者协程）模型，不使用 mutex、RWMutex、channel 或 atomic state handoff（原子状态交接）。每个 ClientRouter 实例由调用节点自己的一个 owner goroutine 驱动；每个 PhysicalNode 实例由 MatchService 内自己的一个 owner goroutine 顺序驱动，并在同一 goroutine 中同步调用 LogicalNode 和 Prefilter。尤其不能把同一 PhysicalNode 的 Add、Remove、Get 和 Tick 分派到不同 goroutine。

如果服务器入口存在网络并发，必须在进入本仓库的匹配核心之前完成串行化；核心 API 自身不提供并发保护，也不允许回调重入。`TickSession` 只固定一次尝试批次的 `now` 和 Fact，它不是并发数据快照。

## 验证

```bash
go test ./...
go vet ./...
go build ./...
```
