# Seed 顺序策略与匹配轮次

本文只决定 seed 的尝试顺序与预算；选定 seed 后的 Prefilter、Top-L、Join、Match Fact
更新和 Complete 时序见 [Evaluation/v2](evaluation-layer.md)。

本文描述 `internal/matchsystem` 中 Seed 顺序策略、轮次快照和游标推进契约。

## 1. 核心模型

Seed 选择拆成两个职责：

```text
SeedOrderPolicy
  -> 在 BeginMatchRound 时生成不超过本轮上限的 TicketID 排列
  -> 不保存扫描游标，不执行匹配

LogicalNode.SeedRound
  -> 校验 TicketID 排列并解析为私有 DocID 排列
  -> 保存固定 now、DocID 排列和 cursor
  -> ProduceMatch 每次在评估前推进 cursor
  -> 保证同一轮一个 Seed 最多被选择一次
```

内置策略会直接构建不超过轮次上限的 Seed 序列；自定义策略则看到轮次开始时的全部活跃 Candidates，并通过 `SeedOrderContext.MaxSeeds` 获知本轮最多可以返回多少个 Seed。自定义策略可以从全池选择全局最佳子集，返回值必须是不超过 `MaxSeeds` 个唯一且当前有效的 TicketID；不要求覆盖全部 Candidates。LogicalNode 会验证返回值，再通过节点内 `TicketID -> DocID` 映射生成 SeedRound。DocID 不暴露给策略。

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
- evaluation/v2 的 Join 返回拒绝；
- 调用期间 context 被取消。

轮次具有快照语义：

- `BeginMatchRound` 之后新增的 Ticket 在下一轮才可成为 Seed；
- 按匹配编排契约，轮次消费期间不调用 Add/Remove；如果内部流程仍使未来 Seed 变成失效 DocID，读取时会防御性地直接跳过 stale DocID；
- 匹配失败并继续留在池中的 Seed 位于 cursor 之前，本轮不会再次选择；
- 新一轮重新构建受轮次上限约束的顺序，仍在池中的 Ticket 可以再次成为 Seed（但超出本轮上限的 Ticket 留待后续轮次）；
- LogicalNodeSelector 的节点选择游标不随 SeedRound 重置。

`AttemptLimitPerProduceMatch` 限制一次 `ProduceMatch` 最多连续尝试多少个有效 Seed。

`AttemptLimitPerMatchRound` 限制一个 LogicalNode 在同一 `BeginMatchRound` 到下一轮之间最多实际尝试多少个有效 Seed。预算会跨多次 `ProduceMatch` 累积，成功匹配不会重置；只有新的 `BeginMatchRound` 才会重置。单次调用使用两者剩余容量的较小值。已删除或已失效的 stale DocID 只推进 cursor，不消耗尝试预算；预算耗尽的 LogicalNode 会从 PhysicalNode selector 的候选中排除。

这两个字段的 `<= 0` 值都使用有限默认值 `500`。`AttemptLimitPerMatchRound` 同时限制 `SeedRound` 在构建时物化的最大长度，因此默认配置不会因为外层重复调用 `ProduceMatch` 而扫描整份大型 Seed 数组。内置 `oldest` / `int64_priority` 会直接选出上限以内的优先 Seed，`random` 只保留上限以内的随机样本；自定义策略保留完整 Candidates 视图，但返回结果仍受 `MaxSeeds` 限制。

## 4. 内置策略

通过 `LogicalNodeConfig.SeedScheduler.Order` 配置：

```go
Config: matchsystem.LogicalNodeConfig{
    SeedScheduler: matchsystem.SeedSchedulerConfig{
        AttemptLimitPerProduceMatch: 500,
        AttemptLimitPerMatchRound:   500,
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
    limit := len(ctx.Candidates)
    if limit > ctx.MaxSeeds {
        limit = ctx.MaxSeeds
    }
    order := make([]matchsystem.TicketID, limit)
    for index, ticket := range ctx.Candidates[:limit] {
        order[len(order)-1-index] = ticket.TicketID
    }
    return order, nil
})

spec.SeedOrderPolicy = policy
```

`SeedOrderContext.Candidates` 包含完整活跃 Ticket 集合并按到达顺序排列；`MaxSeeds` 是本轮允许返回的最大 Seed 数。Ticket 指针只读，只能在同步回调期间使用。策略不能修改或持有 Ticket，也不能重入 PhysicalNode。策略可以返回少于 `MaxSeeds` 的唯一 `[]TicketID` 子集，但每个 ID 必须属于当前 LogicalNode 且不能重复；LogicalNode 会立即校验并翻译成私有 `[]uint32 DocID`，不会持有策略返回的 slice。策略可以维护生成顺序所需的私有状态，例如随机数生成器，但不能自行维护轮次 cursor。

如果需要表达比 Seed 预算更严格的业务 eligibility，应在匹配规则或独立 eligibility 设计中表达；返回子集本身只用于实现策略选择和轮次上限，不应伪装成业务过滤。

## 6. 复杂度

- 设 `n` 为当前活跃 Ticket 数量、`L` 为 `AttemptLimitPerMatchRound`；`arrival` 在密集到达顺序下只借用节点内数组的前 `min(n,L)` 项，稀疏顺序扫描到上限即停止；
- `oldest`、`int64_priority`：扫描 O(n)，使用容量为 `L` 的 bounded heap，时间 O(n log L)、额外空间 O(L)；
- `random`：扫描 O(n)，使用容量为 `L` 的 reservoir，额外空间 O(L)；
- 自定义策略：框架为保持通用选择能力会构建完整 Candidates，策略自身的排序/选择复杂度由实现决定；框架只对返回的 DocID Seed 序列和校验集合按 `L` 限制；
- `nextSeed`：摊销 O(1)，stale DocID 每轮最多跳过一次。

轮次快照最多使用 O(L) 个 DocID，换取统一的不重复保证、稳定排序语义，以及 Add/Remove 与任意排序策略之间清晰的边界。

## 7. 测试契约

新增或自定义策略至少应验证：

1. 返回不超过 `MaxSeeds` 且唯一、有效的 TicketID 子集；
2. 同一轮遍历到耗尽时，返回序列中的每个 Seed 恰好选择一次；
3. 新一轮可以重新选择仍然活跃的 Ticket；
4. 删除未来 Seed 时游标可继续推进；
5. 轮次中新增 Ticket 不进入当前快照；
6. 相同输入的 tie-break 稳定，随机策略在固定 RandomSeed 下可复现。
