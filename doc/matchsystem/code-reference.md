# `internal/matchsystem` 代码索引

范围是根包当前生产代码。规则的唯一配置入口是 `match-rule/v1` RuleJSON；子包的详细
符号索引见同目录下的 `contract`、`expression`、`fact`、`jsonstrict`、`prefilter` 和
`evaluation` 文档。

## 1. 公共类型与 facade

根包公开别名与运行时快照包括：EvaluationCanJoinInput、EvaluationCanCompleteInput、
EvaluationError；FactError、FactScope、FactScopeTick、FactScopeObject、FactScopeMatch、
FactType、FactTypeStrings、FactTypeInt64、FactTypeUint64s、MatchFacts；
LogicalNodeCandidate、LogicalNodeDescriptor、SeedOrderContext、
SeedPriorityDirection 和 SeedPriorityAscending。它们分别对应 Evaluation、Fact、
PhysicalNode 选择器和 seed round 的输入/输出边界。

| 文件 | 主要符号 | 用途 |
| --- | --- | --- |
| `ticket.go` | `TicketID`、`Ticket`、`Match` | 指向 `internal/common` 的类型别名；根包不创建第二套 Ticket |
| `fact_types.go` | `FactType`、`FactSpec`、`FactScope`、`Facts`、`FactView`、`FactValidator` | 指向 `fact`/`common` 的别名，以及三个 Fact Provider 类型 |
| `rule_config.go` | `CompileRuleJSON`、`CompiledRuleConfig`、`RuleConfigError` | 解析、编译并校验唯一 `match-rule/v1` RuleJSON |
| `evaluation_runtime.go` | `CandidateScoreContext`、`EvaluationCanJoinInput`、`EvaluationCanCompleteInput` | 评分和谓词求值的只读运行时上下文 |

评分上下文只有 `Seed`、`Candidate`、`Now`、Tick/seed/candidate Facts；它不暴露 Match
或 Match-scoped Fact。内置评分必须返回有限 `float64`，不能保留或修改传入快照。

RuleJSON 的 `runtime.candidateLimitPerSeed` 只限制 Top-L 评分候选数量，不改变
Prefilter 的索引候选全集；`maxPlayers` 控制组大小，两个 attempt limit 控制单次调用
和整轮的 seed 消耗。四个字段均为正整数，并在编译时转换为 LogicalNode 的内部预算。

## 2. LogicalNode 配置与 API

`logical_node.go` 定义 `LogicalNodeSpec`：

| 字段 | 必填/语义 |
| --- | --- |
| `Key` | 必须通过 `identity.LogicalNodeKey.Validate`；Rule 部分必须与 RuleJSON.ruleKey 一致 |
| `RuleJSON` | 唯一且完整的 `match-rule/v1` 配置，包含所有规则行为和运行预算 |
| `MatchFactProvider` | Contract 含 `scope: match` Fact 时必须非 nil；由宿主动态注入 |
| `FactProvider` | 可选；每次 `ProduceMatch` 至多创建一次 Tick 层，由宿主动态注入 |
| `ObjectFactProvider` | 可选；每个 Ticket/本次调用至多执行一次，由宿主动态注入 |

主要状态与方法：

| 符号 | 说明 |
| --- | --- |
| `LogicalNodeState` / `LogicalNodeReady` / `LogicalNodeDraining` / `LogicalNodeStopped` | 节点状态 |
| `NewLogicalNode(spec)` | 编译一份 RuleJSON，创建私有 Plan、谓词、评分器和 Seed 顺序实例 |
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

## 4. 选择器与 Seed 选择

`logical_node_selector.go` 提供 `LogicalNodeSelector`、`FuncLogicalNodeSelector`
和四个内建构造器：`NewRoundRobinLogicalNodeSelector`、
`NewSmoothWeightedRoundRobinLogicalNodeSelector`、`NewLargestQueueLogicalNodeSelector`、
`NewOldestWaitingLogicalNodeSelector`。选择器只返回 Key，不执行匹配；自定义选择器
收到包含所有节点（含不可选节点）的 `LogicalNodeSelectContext`，必须返回当前 Eligible
节点。

`seed_order.go` 实现 RuleJSON `seedSelection` 的内置顺序：

| 常量/配置 | 行为 |
| --- | --- |
| `arrival` | 按加入顺序 |
| `oldest` | `CreatedAt` 升序，同值按到达顺序 |
| `int64_priority` | 读取指定 Ticket `Int64Values`，升/降序；缺失值排后 |
| `random` | 使用 `randomSeed` 的确定性随机顺序 |

`CompileRuleJSON` 会校验类型、参数、方向和 Contract Attribute 绑定；未知类型、空字段、
错误类型或重复 seed ID 都会被拒绝。`int64_priority` 的 field 必须是 Contract 声明的
int64 Attribute；`random` 的 `randomSeed` 保证同一 RuleJSON 的顺序可重放。

## 5. 运行时私有辅助

`seed_evaluator.go` 内部的 `fact.Frame`、`topCandidates`、bounded candidate
heap、`initializeMatchFacts` 和 `onJoinMatchFacts` 固定运行顺序；
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
