# `internal/matchsystem` 代码索引

范围是根包当前生产代码。子包的详细符号索引见同目录下的 `contract`、`expression`、
`fact`、`jsonstrict`、`prefilter` 和 `evaluation` 文档。

## 1. 公共类型与 facade

根包公开别名与运行时快照包括：EvaluationCanJoinInput、EvaluationCanCompleteInput、
EvaluationError；FactError、FactScope、FactScopeTick、FactScopeObject、FactScopeMatch、
FactType、FactTypeStrings、FactTypeInt64、FactTypeUint64s、MatchFacts；
LogicalNodeCandidate、LogicalNodeDescriptor；以及 SeedOrderContext、
SeedOrderPolicyKind、SeedPriorityDirection、SeedPriorityAscending。它们分别对应
Evaluation、Fact、PhysicalNode 选择器和 seed 排序的输入/输出边界。

| 文件 | 主要符号 | 用途 |
| --- | --- | --- |
| `ticket.go` | `TicketID`、`Ticket`、`Match` | 指向 `internal/common` 的类型别名；根包不创建第二套 Ticket |
| `fact_types.go` | `FactType`、`FactSpec`、`FactScope`、`Facts`、`FactView`、`FactValidator` | 指向 `fact`/`common` 的别名，以及三个 Fact Provider 类型 |
| `contract_api.go` | `ParseLogicalNodeContractJSON` | 以默认有界限制解析唯一 `logical-node-contract/v3` |
| `evaluation_api.go` | `CompileEvaluationJSON`、`EvaluationPredicates` | Evaluation facade；实际实现位于 `evaluation` |
| `evaluation_runtime.go` | `CandidateScoreContext`、`CandidateScorer` | 直接绑定 LogicalNode 的排名 callback |
| `group_builder.go` | `GroupBuilderConfig` | 每个 seed 的候选预算和组最大人数的内部运行配置 |

`CandidateScoreContext` 只有 `Seed`、`Candidate`、`Now`、Tick/seed/candidate
Facts；它不暴露 Match 或 Match-scoped Fact。Scorer 必须返回有限 `float64`，不能
保留或修改传入快照。

`GroupBuilderConfig.CandidateLimitPerSeed` 小于等于零时默认为 128；该配置只限制
Top-L 评分候选数量，不改变 Prefilter 的索引候选全集。`MaxPlayers` 由
`LogicalNodeConfig` 读取，小于等于零时默认为 8。

## 2. LogicalNode 配置与 API

`logical_node_core.go` 定义：

```go
type LogicalNodeConfig struct {
    SeedScheduler SeedSchedulerConfig
    GroupBuilder  GroupBuilderConfig
    MaxPlayers    int
}
```

`MaxPlayers <= 0` 默认 8；`GroupBuilderConfig.CandidateLimitPerSeed <= 0` 默认
128；`SeedSchedulerConfig` 的两个尝试上限小于等于零时均默认 500。

`logical_node.go` 定义 `LogicalNodeSpec`：

| 字段 | 必填/语义 |
| --- | --- |
| `Key` | 必须通过 `identity.LogicalNodeKey.Validate` |
| `ContractJSON` | 完整 `logical-node-contract/v3` |
| `PrefilterJSON` | 完整 `prefilter/v3` |
| `EvaluationJSON` | 完整 `evaluation/v3`，含两个 Bool root |
| `CandidateScorer` | 始终非 nil，直接用于 Top-L 排名 |
| `MatchFactProvider` | Contract 含 `scope: match` Fact 时必须非 nil |
| `FactProvider` | 可选，每次 `ProduceMatch` 至多创建一次 Tick 层 |
| `ObjectFactProvider` | 可选，每个 Ticket/本次调用至多执行一次 |
| `SeedOrderPolicy` | 可选；非 nil 时覆盖 `Config.SeedScheduler.Order` |
| `Config` | 轮次上限、分组人数和候选预算 |

主要状态与方法：

| 符号 | 说明 |
| --- | --- |
| `LogicalNodeState` / `LogicalNodeReady` / `LogicalNodeDraining` / `LogicalNodeStopped` | 节点状态 |
| `NewLogicalNode(spec)` | 解析三份 JSON、编译计划、绑定 provider/scorer |
| `(*LogicalNode).Add(ticket)` | 校验 ID、深拷贝并写入 Prefilter；返回节点内 DocID |
| `(*LogicalNode).Remove(ticketID)` | 删除 Ticket、索引 posting 和 arrival entry；不存在返回 false |
| `(*LogicalNode).Get(ticketID)` | 返回 Ticket 深拷贝 |
| `(*LogicalNode).Len()` | 当前池大小 |
| `(*LogicalNode).BeginMatchRound(now)` | 固化 seed 顺序、时间和游标 |
| `(*LogicalNode).ProduceMatch(ctx)` | 消费本轮 seed，最多返回一个 Match |

顶层错误包括 `ErrDuplicateRuleKey`、`ErrLogicalNodeNotFound`、
`ErrLogicalNodeNotReady`、`ErrLogicalNodeNotEmpty`、`ErrWrongPhysicalNode`、
`ErrOwnerMismatch`、`ErrNoLogicalNodeAvailable` 和 `ErrMatchRoundNotStarted`。

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
| `Add/Remove/Get(ctx, OwnerRef, ...)` | 校验 owner 后转发到目标 LogicalNode |
| `BeginMatchRound(ctx, now)` | 为所有 LogicalNode 先构建后一次性安装 round snapshot |
| `ProduceMatch(ctx)` | selector 选择可运行节点并执行一次匹配尝试 |
| `BeginDrain(ctx, key)` | 将目标 LogicalNode 标为 Draining |
| `Stop(ctx, key)` | 仅空节点可停止并从调度顺序删除 |
| `Describe()` | 返回按 Load 顺序排列的状态和 TicketCount 快照 |

## 4. 选择器与 Seed 策略

`logical_node_selector.go` 提供 `LogicalNodeSelector`、`FuncLogicalNodeSelector`
和四个内建构造器：`NewRoundRobinLogicalNodeSelector`、
`NewSmoothWeightedRoundRobinLogicalNodeSelector`、`NewLargestQueueLogicalNodeSelector`、
`NewOldestWaitingLogicalNodeSelector`。选择器只返回 Key，不执行匹配；自定义选择器
收到包含所有节点（含不可选节点）的 `LogicalNodeSelectContext`，必须返回当前 Eligible
节点。

`seed_order.go` 提供 `SeedOrderPolicy`、`FuncSeedOrderPolicy` 和：

| 常量/配置 | 行为 |
| --- | --- |
| `SeedOrderArrival` | 按加入顺序 |
| `SeedOrderOldest` | `CreatedAt` 升序，同值按到达顺序 |
| `SeedOrderInt64Priority` | 读取指定 Ticket `Int64Values`，升/降序；缺失值排后 |
| `SeedOrderRandom` | 使用 `RandomSeed` 的确定性随机顺序 |

`NewSeedOrderPolicy` 会校验 `PriorityField`、方向和未知 Kind。自定义策略只能返回
不超过 `MaxSeeds` 的唯一 `TicketID`；LogicalNode 会拒绝未知或重复 ID。

## 5. 运行时私有辅助

`logical_node_core.go` 内部的 `fact.Frame`、`topCandidates`、bounded candidate
heap、`initializeMatchFacts`、`onJoinMatchFacts` 和 `commitMatch` 固定运行顺序。
Scorer 失败、panic、NaN/Inf 由 `SCORER_ERROR`、`SCORER_PANIC`、
`NONFINITE_SCORE` 结构化为 Evaluation error；Provider 相应为 `PROVIDER_ERROR`、
`PROVIDER_PANIC`、`PROVIDER_CANCELED`。这些函数不是扩展点，不应由外部绕过
`ProduceMatch` 直接调用。

实现链接：[LogicalNode 生命周期](../../internal/matchsystem/logical_node.go)、
[匹配编排](../../internal/matchsystem/logical_node_core.go)、
[PhysicalNode](../../internal/matchsystem/physical_node.go)、
[选择器](../../internal/matchsystem/logical_node_selector.go)、
[Seed 策略](../../internal/matchsystem/seed_order.go)。
