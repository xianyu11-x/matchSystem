# MatchSystem 通用匹配模板

这是一个可扩展的进程内匹配池，只保留当前的 `RuleSet + indexed filtering（索引化过滤）+ Greedy（贪心建组）` 路径。仓库不包含具体游戏模式、评分体系、黑名单、分段放宽或其他业务规则。

## 核心流程

每次 `Tick(now)` 依次执行：

1. 按调度配置选择 seed（种子 Ticket）。
2. 让 `CandidateFilter` 生成候选集；过滤器可使用通用数值与字符串索引。
3. 按默认或自定义分数排序候选。
4. 使用唯一保留的 Greedy 算法依次调用 `GroupEvaluator` 建组。
5. 检查正常开局或强制开局规则，并从池和索引中移除已匹配 Ticket。

同一个 `MatchPool` 应由单个 actor/event loop（执行器/事件循环）串行调用；不同池可以由上层并行调度。

## 通用扩展点

- `Ticket.Attributes`：字符串业务数据。
- `Ticket.Numeric`：`int64` 数值业务数据。
- `FuncCandidateFilter`：定义候选过滤和成本估算；可通过 `CandidateFilterContext` 查询通用索引。
- `FuncGroupEvaluator`：定义 Join（入组）、Start（开局）和 ForceStart（强制开局）条件。
- `AllEvaluators`、`AnyEvaluators`、`NotEvaluator`、`WhenEvaluator`：组合通用评估器。
- `RuleSet.WithCandidateScore`：替换候选排序分数。

核心没有内置业务规则。空 `RuleSet` 不会隐式开局，调用方至少应注册一个 `GroupEvaluatorStart` 或 `GroupEvaluatorForceStart` 评估器。

## 最小用法

```go
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
    func(seed, candidate *matchsystem.Ticket, now int64) float64 {
        return float64(now - candidate.CreatedAt)
    },
)

pool := matchsystem.NewMatchPool(matchsystem.PoolConfig{
    MaxPlayers: 4,
    GroupBuilder: matchsystem.GroupBuilderConfig{
        CandidateLimitPerSeed: 128,
    },
}, rules)

pool.Add(&matchsystem.Ticket{
    TicketID:  "ticket-1",
    CreatedAt: now,
    Attributes: map[string]string{
        "your_string_field": "value",
    },
    Numeric: map[string]int64{
        "your_numeric_field": 1,
    },
})

matches := pool.Tick(now)
```

仓库内的测试展示了基于函数适配器实现索引过滤、开局条件、超时兜底和候选评分的方法。

## 验证

```bash
go test ./...
go vet ./...
go build ./...
```
