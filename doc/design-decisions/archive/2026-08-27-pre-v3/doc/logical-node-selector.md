# LogicalNode 负载均衡策略

`LogicalNodeSelector` 属于 PhysicalNode，只负责为一次 `ProduceMatch` 选择一个本地 LogicalNode。它不决定 Ticket 的路由归属，不建立匹配轮次，也不直接执行匹配。

## 1. 使用方式

Round Robin 是默认策略：

```go
physical, err := matchsystem.NewPhysicalNode("physical-a")
```

注入其他策略：

```go
selector, err := matchsystem.NewSmoothWeightedRoundRobinLogicalNodeSelector(
    map[identity.RuleKey]uint32{
        rankedRule: 3,
        casualRule: 1,
    },
)
if err != nil {
    return err
}

physical, err := matchsystem.NewPhysicalNode(
    "physical-a",
    matchsystem.WithLogicalNodeSelector(selector),
)
```

一个有状态 selector 实例只能由一个 PhysicalNode 持有。所有调用继续遵守 PhysicalNode 的 single-owner goroutine 契约。

## 2. 候选资格

PhysicalNode 为每次选择构建只读候选快照：

```go
type LogicalNodeCandidate struct {
    Key             identity.LogicalNodeKey
    Eligible        bool
    TicketCount     int
    OldestCreatedAt int64
}
```

`Eligible` 还要求本轮 Seed 尝试预算未耗尽。`AttemptLimitPerMatchRound` 跨同一轮的多次 `ProduceMatch` 累计；达到上限后节点会从 selector 看到的可运行集合中消失，即使内部顺序数组仍有未消费项。

只有同时满足以下条件的节点才是 Eligible：

- 状态为 Ready 或 Draining；
- 当前仍有 Ticket；
- 当前 SeedRound 仍有未尝试且未删除的 Seed。

所有已加载节点仍按 Load 顺序出现在候选列表中，使 Round Robin 即使跳过临时不合格节点也能保持稳定位置。PhysicalNode 会验证 selector 返回的 LogicalNodeKey；未知、不属于当前 PhysicalNode 或不合格的节点都会被拒绝。

## 3. 内置策略

| 构造函数 | 行为 | 适用场景 |
| --- | --- | --- |
| `NewRoundRobinLogicalNodeSelector` | 从上次选择节点的后一个位置继续，跳过不合格节点 | 默认公平调度 |
| `NewSmoothWeightedRoundRobinLogicalNodeSelector` | 按 RuleKey 权重执行平滑加权轮询；未配置权重为 1 | 不同规则需要不同执行份额 |
| `NewLargestQueueLogicalNodeSelector` | 选择当前 TicketCount 最大的节点 | 优先消化积压吞吐 |
| `NewOldestWaitingLogicalNodeSelector` | 选择最小 CreatedAt 所在节点 | 优先控制最长等待时间 |

LargestQueue 和 OldestWaiting 在相同指标下按 PhysicalNode Load 顺序稳定打破平局。LogicalNode 使用带 stale 清理的最小堆维护最早 CreatedAt，因此选择 OldestWaiting 不需要每次扫描整个 Ticket 池。

“最小队列优先”没有作为内置策略：这里是在给已有队列分配匹配执行机会，不是在给新连接选择空闲服务器；持续选择最小队列可能使大积压节点饥饿。如果业务确实需要，可以通过自定义策略实现。

## 4. 自定义策略

```go
selector := matchsystem.FuncLogicalNodeSelector(func(
    ctx matchsystem.LogicalNodeSelectContext,
) (identity.LogicalNodeKey, error) {
    for _, candidate := range ctx.Candidates {
        if candidate.Eligible && candidate.Key.Rule == preferredRule {
            return candidate.Key, nil
        }
    }
    return identity.LogicalNodeKey{}, matchsystem.ErrNoLogicalNodeAvailable
})
```

自定义策略必须：

- 只返回 `Eligible` 的候选；
- 不修改或持有候选切片；
- 不调用或重入 PhysicalNode；
- 在相同输入下提供明确的 tie-break；
- 无候选时返回 `ErrNoLogicalNodeAvailable`。

策略可以保存公平性所需的私有状态，但节点生命周期、SeedRound 和匹配执行仍由 PhysicalNode/LogicalNode 管理。

## 5. 与 SeedOrderPolicy 的边界

```text
LogicalNodeSelector
  -> 在多个 LogicalNode 之间分配一次 ProduceMatch 机会

SeedOrderPolicy
  -> 在一个 LogicalNode 内生成受轮次预算限制的 Seed 顺序

ClientRouter
  -> Add Ticket 时在多个 PhysicalNode 之间确定归属
```

三者不能互相替代。一次 `ProduceMatch` 即使返回 NoMatch 或错误，也不会在同一次调用中改选第二个 LogicalNode；是否继续由外部 MatchService 的轮次循环决定。
