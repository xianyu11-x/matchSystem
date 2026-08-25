# Seed 顺序策略与匹配轮次

本文描述 `internal/matchsystem` 中 Seed 顺序策略、轮次快照和游标推进契约。

## 1. 核心模型

Seed 选择拆成两个职责：

```text
SeedOrderPolicy
  -> 在 BeginMatchRound 时生成本轮完整 TicketID 排列
  -> 不保存扫描游标，不执行匹配

LogicalNode.SeedRound
  -> 校验 TicketID 排列并解析为私有 DocID 排列
  -> 保存固定 now、DocID 排列和 cursor
  -> ProduceMatch 每次在评估前推进 cursor
  -> 保证同一轮一个 Seed 最多被选择一次
```

策略返回的顺序必须是轮次开始时全部活跃 TicketID 的完整排列：长度相等、不重复、不包含未知 TicketID，也不能漏掉 Ticket。LogicalNode 会验证自定义策略的返回值，再通过节点内 `TicketID -> DocID` 映射生成 SeedRound。DocID 不暴露给策略。

## 2. 轮次 API

正式调用入口是：

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

`BeginMatchRound` 原子构建所有 LogicalNode 的 SeedRound。任一节点构建失败时，不提交部分新轮次。`now` 固定在轮次中，所以后续 `ProduceMatch` 不再接收时间。

直接驱动单个 LogicalNode 时使用：

```go
if err := node.BeginMatchRound(now); err != nil {
    return err
}
match, err := node.ProduceMatch(facts)
```

未开始轮次就调用 `ProduceMatch` 返回 `ErrMatchRoundNotStarted`。

## 3. 游标与数据变化

`nextSeed` 在返回 Ticket 前推进 cursor。以下结果都不会回退游标：

- ObjectFactProvider 或 Fact 校验失败；
- Prefilter 查询失败；
- 没有形成合法组；
- GroupEvaluator 返回拒绝；
- 调用期间 context 被取消。

轮次具有快照语义：

- `BeginMatchRound` 之后新增的 Ticket 在下一轮才可成为 Seed；
- 按匹配编排契约，轮次消费期间不调用 Add/Remove；如果内部流程仍使未来 Seed 变成失效 DocID，读取时会防御性地直接跳过 stale DocID；
- 匹配失败并继续留在池中的 Seed 位于 cursor 之前，本轮不会再次选择；
- 新一轮重新构建完整顺序，仍在池中的 Ticket 可以再次成为 Seed；
- LogicalNodeSelector 的节点选择游标不随 SeedRound 重置。

`AttemptLimitPerProduceMatch` 限制一次 `ProduceMatch` 最多连续尝试多少个 Seed，不限制整轮 Seed 数量。

## 4. 内置策略

通过 `LogicalNodeConfig.SeedScheduler.Order` 配置：

```go
Config: matchsystem.LogicalNodeConfig{
    SeedScheduler: matchsystem.SeedSchedulerConfig{
        AttemptLimitPerProduceMatch: 500,
        Order: matchsystem.SeedOrderPolicyConfig{
            Kind:              matchsystem.SeedOrderInt64Priority,
            PriorityField:     "priority",
            PriorityDirection: matchsystem.SeedPriorityDescending,
        },
    },
}
```

当前内置策略：

| Kind | 顺序 | 相同值处理 |
| --- | --- | --- |
| `arrival` | LogicalNode Add 顺序；也是零值默认策略 | 保持到达顺序 |
| `oldest` | `CreatedAt` 从小到大 | 保持到达顺序 |
| `int64_priority` | 指定 `Int64Values` 字段升序或降序；缺失字段排在末尾 | 保持到达顺序 |
| `random` | 使用节点私有伪随机源每轮洗牌 | `RandomSeed` 保证执行可复现 |

`int64_priority` 必须配置 `PriorityField`；方向零值为 `descending`。随机策略实例只能由一个 LogicalNode 持有，不能在多个节点间共享。

## 5. 自定义策略

代码扩展可通过 `LogicalNodeSpec.SeedOrderPolicy` 注入，它会覆盖内置配置：

```go
policy := matchsystem.FuncSeedOrderPolicy(func(ctx matchsystem.SeedOrderContext) ([]matchsystem.TicketID, error) {
    order := make([]matchsystem.TicketID, len(ctx.Candidates))
    for index, ticket := range ctx.Candidates {
        order[len(order)-1-index] = ticket.TicketID
    }
    return order, nil
})

spec.SeedOrderPolicy = policy
```

`SeedOrderContext.Candidates` 已按到达顺序排列，Ticket 指针只读，只能在同步回调期间使用。策略不能修改或持有 Ticket，也不能重入 PhysicalNode。`BuildOrder` 返回 `[]TicketID`；LogicalNode 会立即校验并翻译成私有 `[]uint32 DocID`，不会持有策略返回的 slice。策略可以维护生成顺序所需的私有状态，例如随机数生成器，但不能自行维护轮次 cursor。

如果需要跳过部分 Ticket，应在匹配规则或独立 eligibility 设计中表达，不能通过返回不完整排列实现，否则轮次建立会失败。

## 6. 复杂度

- `arrival`：时间和额外空间均为 O(n)，密集到达顺序可直接借用节点内 DocID 数组；
- `oldest`、`int64_priority`：O(n log n) 时间、O(n) 空间；
- `random`：O(n) 时间、O(n) 空间；
- `nextSeed`：摊销 O(1)，stale DocID 每轮最多跳过一次。

轮次快照使用 O(n) DocID 内存换取统一的不重复保证、稳定排序语义，以及 Add/Remove 与任意排序策略之间清晰的边界。

## 7. 测试契约

新增或自定义策略至少应验证：

1. 返回完整且唯一的 TicketID 排列；
2. 同一轮遍历到耗尽时每个 Seed 恰好选择一次；
3. 新一轮可以重新选择仍然活跃的 Ticket；
4. 删除未来 Seed 时游标可继续推进；
5. 轮次中新增 Ticket 不进入当前快照；
6. 相同输入的 tie-break 稳定，随机策略在固定 RandomSeed 下可复现。
