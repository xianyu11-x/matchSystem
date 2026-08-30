# `internal/matchsystem` 架构说明

本文描述当前工作树的根包 `internal/matchsystem`。它是一个进程内匹配内核：
`PhysicalNode` 作为单一 owner 管理多个隔离的 `LogicalNode`；每个逻辑节点由
`LogicalNode` 协调状态与轮次，`ticketStore` 负责 Ticket/DocID 和 Prefilter 生命周期，
`seedEvaluator` 负责一次 ProduceMatch 的完整评估流程。
网络、配置中心、限流、持久化和服务发现不属于本包。

## 1. 分层与依赖

```text
RuleJSON (match-rule/v1)
       ├── ruleKey -> complete RuleKey identity
       ├── contract -> shared logical-node-contract/v3 schema
       ├── prefilter -> immutable Plan -> IndexStore/TickSession -> DocSet
       ├── evaluation -> CanJoin / CanComplete Bool predicates
       ├── scoring(type+params) -> built-in candidate scoring
       ├── seedSelection(type+params) -> built-in Seed ordering
       └── runtime -> candidate/group/attempt budgets

PhysicalNode (跨 LogicalNode 选择)
       └── LogicalNode (状态、轮次、预算协调)
                ├── ticketStore (Ticket、DocID、arrival、Prefilter、Commit)
                └── seedEvaluator (Fact、Prefilter、Top-L、评分、谓词、Seed round)
```

RuleJSON 是规则行为的唯一配置来源；其中 `contract` 是所有表达式和内置算法共享的
声明来源。`expression` 提供 Prefilter 和 Evaluation 共用的标量语言；`fact` 提供三种
Fact scope、clone 和测试/调试校验工具；`jsonstrict` 只负责 JSON 结构安全。根包的
编排入口不复制这些模型。Fact Provider 实现仍由宿主动态注入。

## 2. 生命周期与所有权

`PhysicalNode` 和其中的 `LogicalNode` 都没有锁，也没有并发快照。外部必须指定一个
owner goroutine，串行调用 `Load`、`Add`、`Remove`、`Get`、
`BeginMatchRound`、`ProduceMatch`、`BeginDrain`、`Stop` 和 `Describe`。
一个 LogicalNode 的 Prefilter `IndexStore`、`TickSession` 和候选 `DocSet` 同样
遵守该边界。

进入节点时，`ticketStore.Add` 对 Ticket 做一次深拷贝并同步写入 Prefilter；`Get` 返回
独立副本；Match 提交时由 `ticketStore.Commit` 转移节点持有的 Ticket 指针。
`seedEvaluator` 只通过只读 store 视图查找候选，Provider、评分、Evaluation 只接收
本次调用的副本或只读快照，不能借此修改 owner 状态。

## 3. 创建与编译顺序

`NewLogicalNode` 的生产配置只有一份 RuleJSON：

1. 校验 `LogicalNodeKey`，调用 `CompileRuleJSON` 校验 `match-rule/v1`，并确认
   `ruleKey` 与 `LogicalNodeKey.Rule` 完全一致；
2. 从 `contract` 建立共享 schema，编译 `prefilter` 和 `evaluation`；Prefilter 使用的
   Fact 不能是 `scope: match`；
3. 从 `scoring` 编译内置评分，从 `seedSelection` 编译内置 Seed 顺序，并校验类型、
   参数和 Contract Attribute 绑定；
4. 从 `runtime` 读取候选上限、组大小和两个 Seed 尝试上限；四个值必须为正整数；
5. Contract 含 Match Fact 时要求宿主提供非 nil `MatchFactProvider`，并创建
   `ticketStore` 与 `seedEvaluator`，返回 `Ready` 状态的隔离 LogicalNode。

完整 RuleJSON 在创建后保存为防御性快照；`CompiledRuleConfig.Fingerprint()` 覆盖
`ruleKey`、Contract、Prefilter、Evaluation、评分、Seed 选择和 runtime。Provider 实现
属于宿主动态依赖，不写入规则文件。

## 4. 一轮匹配的固定流程

```text
BeginMatchRound(now)
  -> 为每个 LogicalNode 固化 seed 顺序、now 和 cursor
ProduceMatch(ctx)
  -> selector 选一个可运行 LogicalNode
  -> 预留一个 seed（失败也不会在本轮重选）
   -> seedEvaluator.BeginSession：FactProvider 创建 Tick Facts，Frame clone 并拥有 Tick 层
  -> seedEvaluator.Evaluate：Object Fact、MatchFactProvider、CanComplete、Prefilter、Top-L、CanJoin
       ├─ true  -> 提交 seed 组
       └─ false -> Prefilter.Candidates
                    -> 内置评分建立 bounded Top-L
                    -> 逐个 CanJoin
                         ├─ false -> 下一个候选
                         └─ true  -> MatchFactProvider.OnJoin
                                       -> clone Fact 并原子接受
                                      -> CanComplete
                                           ├─ true  -> 提交 Match
                                           └─ false -> 继续候选
  -> ticketStore.Commit（仅成功返回的 Match，原子消费 Ticket 与 Prefilter membership）
```

`BeginMatchRound` 之后加入的 Ticket 只进入下一轮；删除的 seed 快照项会被游标跳过，
不会占用有效 seed 尝试预算。一次 `ProduceMatch` 和整轮都有独立尝试上限，由 RuleJSON
的 `runtime` 显式提供，且单次调用上限不能超过整轮上限。

## 5. PhysicalNode 与 LogicalNode

PhysicalNode 只做本地路由和生命周期协调：按 `RuleKey` 防止重复加载，使用
`LogicalNodeSelector` 选择节点，校验 `OwnerRef`，并将调用转发给目标 LogicalNode。
默认是 Round Robin，也可配置平滑加权轮询、最大队列或最早等待节点。

LogicalNode 是匹配状态隔离单元：它持有状态、轮次 cursor/预算以及 `ticketStore` 和
`seedEvaluator` 的组合。Ticket/DocID、到达顺序、Prefilter membership 和消费回收都由
`ticketStore` 封装；Fact、评分、CanJoin/CanComplete 和 Match 组装都由
`seedEvaluator` 封装。`Ready` 接收 Ticket；`Draining` 仍可完成当前轮次；`Stopped`
只能在 Ticket 数为零时进入。

## 6. 失败语义与边界

- RuleJSON 及其嵌套 section 的 JSON/compile 错误发生在节点创建阶段，不创建半初始化节点。
- 表达式读取缺失 Fact、Provider error/cancel、评分 error 或非有限分数都会停止当前
  尝试；Provider panic 直接传播，不会被转换或吞掉。Provider Fact 契约由测试
  保证，生产路径不重复做类型/scope/完整性校验；不会用旧快照 patch 或静默放行。
- `CanComplete` 只读 Tick Fact 和完整 Match Fact；`CanJoin` 还可读 seed/candidate
  属性、Object Fact 和加入前 Match Fact；两者都不直接读已有 Match 成员。
- Prefilter 只返回候选 DocID，不扫描全池 Ticket，不评分，不负责 Join/Complete 或
  Match 提交；Join/Complete 由 seedEvaluator 完成，Match 提交由 ticketStore 完成。

实现入口：[LogicalNode 生命周期](../../internal/matchsystem/logical_node.go)、
[匹配评估](../../internal/matchsystem/seed_evaluator.go)、
[Ticket 生命周期](../../internal/matchsystem/ticket_store.go)、
[PhysicalNode](../../internal/matchsystem/physical_node.go)、
[选择器](../../internal/matchsystem/logical_node_selector.go)、
[Seed 选择](../../internal/matchsystem/seed_order.go)。
