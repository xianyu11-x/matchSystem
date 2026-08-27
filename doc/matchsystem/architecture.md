# `internal/matchsystem` 架构说明

本文描述当前工作树的根包 `internal/matchsystem`。它是一个进程内匹配内核：
`PhysicalNode` 作为单一 owner 管理多个隔离的 `LogicalNode`；每个逻辑节点拥有
自己的 Ticket 池、Prefilter 索引、Evaluation 计划、Fact 生命周期和轮次游标。
网络、配置中心、限流、持久化和服务发现不属于本包。

## 1. 分层与依赖

```text
Contract JSON (logical-node-contract/v3)
       ├── Prefilter JSON (prefilter/v3)
       │       └── immutable Plan -> IndexStore/TickSession -> DocSet
       └── Evaluation JSON (evaluation/v3)
               └── CanJoin / CanComplete Bool predicates

PhysicalNode (跨 LogicalNode 选择)
       └── LogicalNode (Ticket、轮次、评分、Provider、原子提交)
                ├── fact.Frame / ObjectFactProvider
                ├── CandidateScorer
                ├── MatchFactProvider
                └── Prefilter + Evaluation
```

`contract` 是唯一业务声明来源；`expression` 提供 Prefilter 和 Evaluation 共用的
标量语言；`fact` 提供三种 Fact scope、校验和快照；`jsonstrict` 只负责 JSON
结构安全。根包的 facade 只暴露别名和编排入口，不复制这些模型。

## 2. 生命周期与所有权

`PhysicalNode` 和其中的 `LogicalNode` 都没有锁，也没有并发快照。外部必须指定一个
owner goroutine，串行调用 `Load`、`Add`、`Remove`、`Get`、
`BeginMatchRound`、`ProduceMatch`、`BeginDrain`、`Stop` 和 `Describe`。
一个 LogicalNode 的 Prefilter `IndexStore`、`TickSession` 和候选 `DocSet` 同样
遵守该边界。

进入节点时，`Add` 对 Ticket 做一次深拷贝并把它作为节点池的不可变状态；`Get` 返回
独立副本；Match 提交时转移节点持有的 Ticket 指针。Provider、Scorer、Evaluation
只接收本次调用的副本或只读快照，不能借此修改 owner 状态。

## 3. 创建与编译顺序

`NewLogicalNode` 的生产配置是 JSON-only：

1. 校验 `LogicalNodeKey`，用 `contract.Parse` 接受唯一
   `logical-node-contract/v3`；
2. 根据 `SeedScheduler` 创建内建或自定义 Seed 策略，并补齐尝试上限、最大人数等默认值；
3. 用同一份 Contract 编译 `prefilter/v3`，建立 `IndexStore`；Prefilter 使用的
   Fact 不能是 `scope: match`；
4. 要求非 nil `CandidateScorer`；Contract 含 Match Fact 时还要求非 nil
   `MatchFactProvider`；
5. 用同一份 Contract 编译 `evaluation/v3`，创建 Match Fact `Validator`；
6. 返回 `Ready` 状态的隔离 LogicalNode。

Contract 和三个编译结果在创建后都保存为防御性快照。运行时值、Provider 实现和 scorer
不属于编译计划身份。

## 4. 一轮匹配的固定流程

```text
BeginMatchRound(now)
  -> 为每个 LogicalNode 固化 seed 顺序、now 和 cursor
ProduceMatch(ctx)
  -> selector 选一个可运行 LogicalNode
  -> 预留一个 seed（失败也不会在本轮重选）
  -> FactProvider 创建 Tick Facts，Frame 校验并拥有 Tick 层
  -> seed ObjectFactProvider（每个 Ticket/本次调用至多一次）
  -> MatchFactProvider.Initialize 返回完整 Match Fact
  -> CanComplete
       ├─ true  -> 提交 seed 组
       └─ false -> Prefilter.Candidates
                    -> CandidateScorer 建立 bounded Top-L
                    -> 逐个 CanJoin
                         ├─ false -> 下一个候选
                         └─ true  -> MatchFactProvider.OnJoin
                                      -> 完整 Fact 校验并原子接受
                                      -> CanComplete
                                           ├─ true  -> 提交 Match
                                           └─ false -> 继续候选
```

`BeginMatchRound` 之后加入的 Ticket 只进入下一轮；删除的 seed 快照项会被游标跳过，
不会占用有效 seed 尝试预算。一次 `ProduceMatch` 和整轮都有独立尝试上限，默认均为
500（可由 `SeedSchedulerConfig` 覆盖）。

## 5. PhysicalNode 与 LogicalNode

PhysicalNode 只做本地路由和生命周期协调：按 `RuleKey` 防止重复加载，使用
`LogicalNodeSelector` 选择节点，校验 `OwnerRef`，并将调用转发给目标 LogicalNode。
默认是 Round Robin，也可配置平滑加权轮询、最大队列或最早等待节点。

LogicalNode 是匹配状态隔离单元：它持有 `TicketID -> DocID`、到达顺序、seed cursor、
Prefilter store、Evaluation、scorer 和 Fact provider。`Ready` 接收 Ticket；`Draining`
仍可完成当前轮次；`Stopped` 只能在 Ticket 数为零时进入。

## 6. 失败语义与边界

- Contract、Prefilter、Evaluation 的 JSON/compile 错误发生在节点创建阶段，不创建半初始化节点。
- Fact 缺失、类型/scope 冲突、Provider error/panic/cancel、scorer error/panic 或
  非有限分数都会停止当前尝试；不会用旧快照 patch 或静默放行。
- `CanComplete` 只读 Tick Fact 和完整 Match Fact；`CanJoin` 还可读 seed/candidate
  属性、Object Fact 和加入前 Match Fact；两者都不直接读已有 Match 成员。
- Prefilter 只返回候选 DocID，不扫描全池 Ticket，不评分，不负责 Join/Complete 或
  Match 提交；这些均由 LogicalNode 完成。

实现入口：[LogicalNode 生命周期](../../internal/matchsystem/logical_node.go)、
[匹配编排](../../internal/matchsystem/logical_node_core.go)、
[PhysicalNode](../../internal/matchsystem/physical_node.go)、
[选择器](../../internal/matchsystem/logical_node_selector.go)、
[Seed 策略](../../internal/matchsystem/seed_order.go)。
