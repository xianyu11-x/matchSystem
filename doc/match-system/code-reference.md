# `internal/matchsystem` 代码索引

范围是根包当前生产代码。规则的唯一配置入口是 `match-rule/v1` RuleJSON；子包的详细
符号索引见同目录下的 `contract`、`expression`、`fact`、`jsonstrict`、`prefilter` 和
`evaluation` 文档。

## 1. 公共类型与 facade

根包公开类型覆盖 Ticket/Match 别名、Fact facade、Provider、节点生命周期、候选评分、
Seed runtime、PhysicalNode 选择器和诊断指标。Evaluation 的 `CanJoinInput`、
`CanCompleteInput` 与 `Error` 属于 `internal/matchsystem/evaluation` 子包，不在根包重复导出。

| 文件 | 主要符号 | 用途 |
| --- | --- | --- |
| `ticket.go` | `TicketID`、`Ticket`、`Match` | 指向 `internal/common` 的类型别名；根包不创建第二套 Ticket |
| `fact_types.go` | `FactType`、`FactSpec`、`FactScope`、`Facts`、`ObjectFactWriter`、`FactValidator` | 指向 `fact`/`common` 的别名，以及三个 Fact Provider 类型 |
| `rule_config.go` | `CompileRuleJSON`、`CompiledRuleConfig`、`RuleConfigError` | 解析、编译并校验唯一 `match-rule/v1` RuleJSON |
| `evaluation_runtime.go` | `CandidateScoreContext`、`CandidateScorer` | 候选评分的只读运行时上下文与回调 |
| `provider_descriptor.go` | `ProviderHandshakeError`、`ProviderHandshake*` 错误码 | Provider 启动握手的结构化失败边界 |
| `produce_metrics.go` | `ProduceMatchMetrics`、`ProduceMatchWithMetrics` | 基准和诊断专用的聚合阶段耗时与计数 |

评分上下文只有 `Seed`、`Candidate`、`Now`、Tick/seed/candidate Facts；它不暴露 Match
或 Match-scoped Fact。上下文中的 Ticket/Facts 是 owner goroutine 在同步回调期间提供的
borrowed read-only 视图，不能保留或修改。内置评分必须返回有限 `float64`。

RuleJSON 的 `runtime.candidateScoringLimitPerSeed` 限制每个 seed 参与评分的候选数量，
Prefilter 结果按 DocID 升序超过上限时直接截断；`candidateLimitPerSeed` 再限制评分池
中保留的 Top-L 数量。前者和后者的默认基准分别为 500 和 50。`maxPlayers` 控制组大小，
两个 attempt limit 控制单次调用和整轮的 seed 消耗；所有运行时字段在编译时转换为
LogicalNode 的内部预算。

## 2. LogicalNode 配置与 API

`logical_node.go` 定义 `LogicalNodeSpec`：

| 字段 | 必填/语义 |
| --- | --- |
| `Key` | 必须通过 `identity.LogicalNodeKey.Validate`；Rule 部分必须与 RuleJSON.ruleKey 一致 |
| `RuleJSON` | 唯一且完整的 `match-rule/v1` 配置，包含所有规则行为和运行预算 |
| `MatchFactProvider` | Contract 含 `scope: match` Fact 时必须非 nil；由宿主动态注入 |
| `MatchFactProviderDescriptor` | Contract 含 `scope: match` Fact 时必须提供；声明 Match Provider 的稳定 ID、Version，并至少覆盖 Contract Facts；允许额外合法 Fact |
| `FactProvider` | 可选；每次 `ProduceMatch` 至多创建一次 Tick 层，由宿主动态注入；接收 `TickFactInput`（包含 `Now` 和只读 `Node` 快照） |
| `FactProviderDescriptor` | Contract 含 `scope: tick` Fact 时必须提供；启动时对 Contract 使用项严格匹配名称、类型、scope 和 `MaxValues`；允许额外合法 Fact |
| `ObjectFactProvider` | 可选；有 Object Fact 时每个 Ticket/generation 至多执行一次，通过 `ObjectFactWriter` 同步写入 slot；无 Object Fact 时不建 slot且不调用 |
| `ObjectFactProviderDescriptor` | Contract 含 `scope: object` Fact 时必须提供；启动时对 Contract 使用项严格匹配名称、类型、scope 和 `MaxValues`；允许额外合法 Fact |
| `MatchFactSnapshotMode` | `None`（默认，只返回 Tickets）或 `DeepCopy`（在 Match 中携带 detached Match/Object Facts） |

`FactProvider` 的输入只暴露值快照，不暴露 `LogicalNode`、Ticket 指针、Store 或可
重入方法。`LogicalNodeSnapshot.WaitingCount` 是 provider 调用时节点仍持有的 active
Ticket 数量；匹配尚未提交，因此本次 seed 仍计入该值。`Key` 和 `State` 可用于复用同一
个 provider 实例处理多个节点。

主要状态与方法：

| 符号 | 说明 |
| --- | --- |
| `LogicalNodeState` / `LogicalNodeReady` / `LogicalNodeDraining` / `LogicalNodeStopped` | 节点状态 |
| `NewLogicalNode(spec)` | 编译一份 RuleJSON，创建私有 Plan、谓词、评分器和 Seed 顺序实例 |
| `(*LogicalNode).Add(ticket)` | 校验 ID、深拷贝并写入 Prefilter；仅返回错误，DocID 不越过节点边界 |
| `(*LogicalNode).Remove(ticketID)` | 删除 Ticket、索引 posting 和 arrival entry；不存在返回 false |
| `(*LogicalNode).Get(ticketID)` | 返回 Ticket 深拷贝 |
| `(*LogicalNode).Len()` | 当前池大小 |
| `(*LogicalNode).BeginMatchRound(now)` | 重置 round 时间/预算，并启动 runtime-owned seed stream |
| `(*LogicalNode).ProduceMatch(ctx)` | 消费本轮 seed，最多返回一个 Match |
| `(*LogicalNode).ProduceMatchWithMetrics(ctx)` | 执行相同流程并返回聚合指标；仅用于基准和诊断 |

顶层错误包括 `ErrDuplicateRuleKey`、`ErrLogicalNodeNotFound`、
`ErrLogicalNodeNotReady`、`ErrLogicalNodeNotEmpty`、`ErrWrongPhysicalNode`、
`ErrOwnerMismatch`、`ErrNoLogicalNodeAvailable` 和 `ErrMatchRoundNotStarted`。
Provider 握手失败通过 `ProviderHandshakeError` 返回，并使用稳定的
`MISSING_PROVIDER`、`MISSING_DESCRIPTOR`、`INVALID_DESCRIPTOR`、`MISSING_FACT`、
`FACT_TYPE_MISMATCH`、`FACT_SCOPE_MISMATCH`、
`FACT_MAX_VALUES_MISMATCH` 或 `DUPLICATE_FACT` code。
Provider Descriptor 的 Fact 集合可以是 Contract 集合的超集；Provider-only 的额外合法 Fact
不会触发握手错误。没有 Contract Fact 的 scope 也可以保留合法的非空 Descriptor，且不会因此
产生 Contract 义务。

## 3. PhysicalNode API

`physical_node.go` 的公开入口：

```go
type PhysicalMatchResult struct {
    LogicalNode identity.LogicalNodeKey
    Match       *common.Match
}

type PhysicalNodeOption func(*PhysicalNode) error
func WithLogicalNodeSelector(selector LogicalNodeSelector) PhysicalNodeOption
func NewPhysicalNode(id identity.PhysicalNodeID, options ...PhysicalNodeOption) (*PhysicalNode, error)
```

| 方法 | 语义 |
| --- | --- |
| `ID()` | 返回物理节点 ID |
| `Load(ctx, spec)` | 创建并加载 LogicalNode；RuleKey 在本物理节点内唯一 |
| `Add/Remove/Get(ctx, OwnerRef, ...)` | 校验 owner 后转发到目标 LogicalNode；Add 仅返回错误，DocID 不暴露 |
| `BeginMatchRound(ctx, now)` | 为所有 LogicalNode 预检后启动新的 runtime seed stream |
| `ProduceMatch(ctx)` | selector 选择可运行节点并执行一次匹配尝试 |
| `BeginDrain(ctx, key)` | 将目标 LogicalNode 标为 Draining |
| `Stop(ctx, key)` | 仅空节点可停止并从调度顺序删除 |
| `Describe()` | 返回按 Load 顺序排列的状态和 TicketCount 快照 |

## 4. 选择器与 Seed 选择

`logical_node_selector.go` 提供 `LogicalNodeSelector`、`FuncLogicalNodeSelector`
和四个内建构造器：`NewRoundRobinLogicalNodeSelector`、
`NewSmoothWeightedRoundRobinLogicalNodeSelector`、`NewLargestQueueLogicalNodeSelector`、
`NewOldestWaitingLogicalNodeSelector`。选择器只返回 Key，不执行匹配；自定义选择器
收到包含所有节点（含不可选节点）的 `LogicalNodeSelectContext`，必须返回当前 Eligible
节点。

`seed_order.go` 实现 RuleJSON `seedSelection` 的内置顺序。`SeedOrderRuntime` 接收
Ticket 生命周期事件（`Add`/`Remove`），并通过 `BeginRound(limit)`、`HasNext`、`Next`
提供有界且本轮不重复的 TicketID stream；DocID 只属于 ticketStore/Prefilter。四种
内置策略各自维护所需的 arrival/list、heap 或 dense-array 状态，不在 BeginMatchRound
时全量物化候选：

| 常量/配置 | 行为 |
| --- | --- |
| `arrival` | 按加入顺序 |
| `oldest` | `CreatedAt` 升序，同值按到达顺序 |
| `int64_priority` | 读取指定 Ticket `Int64Values`，升/降序；缺失值排后 |
| `random` | 使用 `randomSeed` 的确定性随机顺序 |

`CompileRuleJSON` 会校验类型、参数、方向和 Contract Attribute 绑定；未知类型、空字段或
错误类型会被拒绝。内置 runtime 通过同一 owner 串行接收 `Add`/`Remove` 生命周期事件，
保证 active TicketID 唯一；每轮由策略把已返回 entry 暂存，下一轮恢复仍 active 的 entry。
`int64_priority` 的 field 必须是 Contract 声明的 int64 Attribute；`random` 的
`randomSeed` 保证相同生命周期下的 stream 可重放。BeginMatchRound 到本轮 ProduceMatch
消费完成期间不应调用 `Add`，当前实现不提供 pending Add buffer。
PhysicalNode 的 `OldestWaiting` selector 另从 ticketStore 的 live waiting heap 读取
最早 `CreatedAt`；该 heap 是跨策略的调度指标，不是 `SeedOrderRuntime` 的 seed 缓存。

## 5. 运行时私有辅助

`seed_evaluator.go` 内部的 `fact.Frame`、`topCandidates`、bounded Top-L selection、
`initializeMatchFacts` 和 `onJoinMatchFacts` 固定运行顺序；当 effective candidate
limit >= scoring limit 时，Top-L selection 走 append+sort，否则走 bounded heap；
`ticket_store.go` 的 `Commit` 负责成功 Match 的原子消费。
内置评分配置错误在 `CompileRuleJSON` 阶段返回结构化 RuleConfig error；运行时非有限
分数和评分错误分别以 `NONFINITE_SCORE`、`SCORER_ERROR` 结构化为 Evaluation error。
Provider error/cancel 相应为 `PROVIDER_ERROR`、`PROVIDER_CANCELED`。Provider panic 不被
捕获或转换，直接传播；Fact Provider 是宿主动态依赖，不写入规则文件。

实现链接：[LogicalNode 生命周期](../../internal/matchsystem/logical_node.go)、
[匹配评估](../../internal/matchsystem/seed_evaluator.go)、
[Ticket 生命周期](../../internal/matchsystem/ticket_store.go)、
[PhysicalNode](../../internal/matchsystem/physical_node.go)、
[选择器](../../internal/matchsystem/logical_node_selector.go)、
[Seed 策略](../../internal/matchsystem/seed_order.go)。
