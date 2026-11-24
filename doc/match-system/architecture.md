# `matchsystem` 架构说明

本文描述 Go 库根包 `matchsystem`（仓库根模块中的 `pkg/matchsystem/` 包）。它是一个进程内匹配内核：
`PhysicalNode` 作为单一 owner 管理多个隔离的 `LogicalNode`；每个逻辑节点由
`LogicalNode` 协调状态与轮次，`ticketStore` 负责 Ticket/DocID 和 Prefilter 生命周期，
`seedEvaluator` 负责一次 ProduceMatch 的完整评估流程。
网络、配置中心、限流、持久化和服务发现不属于本包。

根模块 `github.com/xianyu11-x/matchSystem` 在 `pkg/` 下公开 `client`、`common`、
`identity` 和 `matchsystem`。`client` 计算路由结果，不提供网络传输；`common` 提供共享值对象，
`identity` 定义匹配核心和客户端共用的 `RuleKey`、`LogicalNodeKey` 和 `OwnerRef`，
这些身份字段统一采用 `uint64`。路由表与可注入选择器见[路由接入](client-router.md)。
`matchsystem` 的 `contract`、`evaluation`、`expression`、
`fact`、`jsonstrict` 和 `prefilter` 六个子包也可按各自的导出 API 使用。
这些包共同版本化；模拟器宿主仍在 `internal/simulator` 和 `internal/simulatorapi`。
`pkg/` 代码在本仓库内只依赖 `pkg/`。Go 标准库与第三方模块仍可直接使用，例如
`prefilter` 使用 RoaringBitmap。依赖方向和验证入口见 [Go 接入指南](go-consumer.md#包与宿主边界)。
外部模块的导入方式见 [Go 接入指南](go-consumer.md)。

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
                ├── ticketStore (Ticket、DocID、Prefilter、Commit、selector oldest metric)
                ├── SeedOrderRuntime (arrival/oldest/priority/random indexes)
                └── seedEvaluator (Fact、Prefilter、Top-L、评分、谓词、Seed round)
```

RuleJSON 是规则行为的唯一配置来源；其中 `contract` 是所有表达式和内置算法共享的
声明来源。`expression` 提供 Prefilter 和 Evaluation 共用的标量语言；`fact` 提供三种
Fact scope、clone 和测试/调试校验工具；`jsonstrict` 只负责 JSON 结构安全。根包的
编排入口不复制这些模型。Fact Provider 实现仍由宿主动态注入。

## 2. 生命周期与所有权

`PhysicalNode` 和其中的 `LogicalNode` 都没有锁，也没有并发快照。外部必须指定一个
owner goroutine，串行调用 `Load`、`Reconfigure`、`Add`、`Remove`、`ClearTickets`、`Get`、
`BeginMatchRound`、`ProduceMatch`、`BeginDrain`、`Stop` 和 `Describe`。
一个 LogicalNode 的 Prefilter `IndexStore`、`TickSession` 和候选 `DocSet` 同样
遵守该边界。

进入节点时，`ticketStore.Add` 对 Ticket 做一次深拷贝并同步写入 Prefilter；`Get` 返回
独立副本；Match 提交时由 `ticketStore.Commit` 转移节点持有的 Ticket 指针。
`seedEvaluator` 只通过只读 store 视图查找候选，Provider、评分、Evaluation 只接收
本次调用的副本或只读快照，不能借此修改 owner 状态。

## 3. 创建与编译顺序

节点可将运行事件写入宿主注入的结构化日志。逻辑节点实例是时间线的采集边界，独立匹配调用在物理选择器完成选择后记录。宿主提取和规则替换的用法见[模拟器时间线](../simulator/timeline-replay.md)。

`NewLogicalNode` 的生产配置只有一份 RuleJSON：

1. 校验 `LogicalNodeKey`，调用 `CompileRuleJSON` 校验 `match-rule/v1`，并确认
   `ruleKey.ruleId` 与 `LogicalNodeKey.Rule.RuleID` 一致；
2. 从 `contract` 建立共享 schema，编译 `prefilter` 和 `evaluation`；Prefilter 使用的
   Fact 不能是 `scope: match`；
3. 从 `scoring` 编译内置评分，从 `seedSelection` 编译内置 Seed 顺序，并校验类型、
   参数和 Contract Attribute 绑定；
4. 从 `runtime` 读取候选上限、组大小和两个 Seed 尝试上限；四个值必须为正整数；
5. Contract 含 Match Fact 时要求宿主提供非 nil `MatchFactProvider`，并创建
   `ticketStore` 与 `seedEvaluator`，返回 `Ready` 状态的隔离 LogicalNode。

完整 RuleJSON 在创建后保存为防御性快照。`CompiledRuleConfig.Fingerprint()` 覆盖
RuleID、Contract、Prefilter、Evaluation、评分、Seed 选择和 runtime。Provider 实现
属于宿主动态依赖，不写入规则文件。

## 4. 一轮匹配的固定流程

```text
BeginMatchRound(now)
  -> 为每个 LogicalNode 重置 now、attempt budget，并启动 runtime seed stream
ProduceMatch(ctx)
  -> selector 选一个可运行 LogicalNode
  -> 从 runtime stream 预留一个 seed（失败也不会在本轮重选）
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

宿主在 `BeginMatchRound` 到本轮 `ProduceMatch` 消费结束期间不得调用 `Add`。新 Ticket
由宿主自行暂存，并在下一轮开始前投递；核心不提供轮内 `Add` 的快照或延迟缓冲保证。
runtime stream 中已经删除的 entry 不会被再次返回，也不会占用有效 seed 尝试预算。
一次 `ProduceMatch` 和整轮都有独立
尝试上限，由 RuleJSON 的 `runtime` 显式提供，且单次调用上限不能超过整轮上限。
同一轮中已经返回过的 seed 会由策略暂存，下一轮仅恢复仍 active 的 entry。

## 5. PhysicalNode 与 LogicalNode

PhysicalNode 只做本地路由和生命周期协调：按 `RuleID` 防止重复加载，使用
`LogicalNodeSelector` 选择节点，校验 `OwnerRef`，并将调用转发给目标 LogicalNode。
默认是 Round Robin，也可配置平滑加权轮询、最大队列或最早等待节点。
宿主可先构建新增或替换的逻辑节点，再由目标物理节点的 owner 协程调用 `Reconfigure`，
按最终顺序一次提交。目标节点将新实例加入当前活动轮次，沿用节点的匹配队列和轮次进度随原实例
保留。完整方法契约见 [PhysicalNode API](code-reference.md#3-physicalnode-api)。

队列清空由 `ClearTickets` 在同一 owner 协程中处理。它沿用逻辑节点的 Ticket 删除流程更新
存储、预筛选与种子顺序，逻辑部署、调度器和活动轮次随原实例继续运行。

`RouteSnapshot` 在物理节点的 owner 协程中提供部署身份、生命周期和实时队列数。宿主
补充部署配置并发布给 `client.Router`，新 Ticket 使用 Router 创建时绑定的选择器。
[路由快照与选择器](client-router.md#从物理节点快照发布路由)

LogicalNode 是匹配状态隔离单元：它持有状态、轮次 now/预算以及 `ticketStore`、
`SeedOrderRuntime` 和 `seedEvaluator` 的组合。Ticket/DocID、Prefilter membership 和
消费回收由 `ticketStore` 封装；到达顺序及其他 seed 排序索引由各自的
`SeedOrderRuntime` 封装；Fact、评分、CanJoin/CanComplete 和 Match 组装都由
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

实现入口：[LogicalNode 生命周期](../../pkg/matchsystem/logical_node.go)、
[匹配评估](../../pkg/matchsystem/seed_evaluator.go)、
[Ticket 生命周期](../../pkg/matchsystem/ticket_store.go)、
[PhysicalNode](../../pkg/matchsystem/physical_node.go)、
[选择器](../../pkg/matchsystem/logical_node_selector.go)、
[Seed 选择](../../pkg/matchsystem/seed_order.go)。
