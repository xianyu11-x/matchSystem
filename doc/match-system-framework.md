# 匹配系统整体设计框架

> 状态：Prefilter、LogicalNode、主键/common、ClientRouter 和进程内 PhysicalNode 基线已实现；MatchService 宿主与远程协议尚未实现。
> 范围：只定义系统分层、核心对象和主流程；各层的算法、配置与接口细节由独立设计文档负责。

## 1. 设计目标

`MatchService` 是本仓库之外的完整匹配服务器，每个 MatchService 内严格一一对应地承载一个 `PhysicalNode`（物理算法实例）。Ticket 发起方通过 ClientRouter 选择 PhysicalNode；外部 `MatchService.Tick` 通过 `BeginMatchRound` 固化匹配轮次并控制产出组数，PhysicalNode 提供轮次建立和单组产出。本仓库不实现 MatchService、TickRunner、RateLimiter client、网络或 IO 编排。核心也不解释地区、模式、玩法版本等业务字段，不内置具体匹配规则。

系统遵循以下边界：

- 每个 ClientRouter 实例绑定调用节点内自己的 owner goroutine；每个 PhysicalNode 实例绑定 MatchService 内自己的 owner goroutine，并在同一 goroutine 中向下驱动 LogicalNode 和 Prefilter。所有状态命令严格顺序执行，核心内部不使用锁、channel 或原子状态切换。
- Add、Remove、Get、BeginMatchRound、ProduceMatch、Load、Drain、Stop 和配置替换不能分派到不同 goroutine；外部并发请求必须在进入核心前完成串行化。
- 一个 Ticket 在等待期间只归属于一个 `OwnerNode`，普通匹配不跨节点执行。
- 路由只决定 Ticket 的归属节点，不承担候选过滤和组合法性判断。
- 索引初筛只产生候选安全超集，最终合法性由评估层保证。
- MatchService 的定时与限速属于外层集成；`MatchService.Tick` 负责一轮匹配并按上限调用 `PhysicalNode.ProduceMatch`。LogicalNode 不拥有独立执行循环。
- 一次 `PhysicalNode.ProduceMatch` 只对应一次 LogicalNode 匹配尝试；被选逻辑节点无结果时，本次调用不会继续尝试第二个逻辑节点。
- 一次 `PhysicalNode.ProduceMatch` 最多返回一个组，但被选 LogicalNode 内部可以连续尝试多个 seed。
- 同一轮的 Seed 游标跨 `ProduceMatch` 调用保留；每个 Seed 在被评估前先推进游标，失败和错误都不会使它在本轮重新入选。
- 合法组产出时，其成员在同一个 owner goroutine 操作中从 TicketStore、Active 集合和各项索引整体移除。

## 2. 总体结构

```text
Ticket 调用节点 / Gateway
  |- ClientRouteTable + ClientRouter
  |    -> 为 Add Ticket 选择 PhysicalNode
  |    -> 由一一对应关系得到 MatchService Endpoint 和 OwnerRef
  `- MatchServiceClient

MatchService（匹配服务器）
  +-- LocalDispatcher
  |     -> 校验 OwnerRef
  |     -> 唯一 PhysicalNode -> 指定 LogicalNode.Add/Remove/Get
  `-- PhysicalNode（匹配算法实例）
        -> LogicalNodeSelector 选择一个可执行 LogicalNode
        -> SeedOrderPolicy + SeedRound
        -> Prefilter + IndexStore（索引层）
        -> bounded Top-L + Ticket materialization
        -> GroupBuilder + GroupEvaluator（评估层）
        -> MatchResult 或 NoMatch
```

`MatchService` 是三层能力之外的服务器与调用边界，持有唯一 PhysicalNode；LogicalNodeSelector 属于 PhysicalNode。Ticket 调用节点只负责选择 PhysicalNode 并路由远程 Ticket 命令，不触发匹配执行。MatchService 内部是否使用 TickRunner 或 RateLimiter client 不属于本框架的匹配核心设计。

在物理/逻辑拓扑中，`MatchService : PhysicalNode = 1 : 1`。MatchService 是服务器，PhysicalNode 是由稳定 PhysicalNodeID 标识的算法实例；本文的 `OwnerNode` 对应 PhysicalNode 内一个数据隔离的 `LogicalNode`（逻辑节点），`OwnerRef = LogicalNodeKey + PhysicalNodeID`。故障后在原部署位置重启 MatchService 并重建同一 PhysicalNodeID，不把 LogicalNode 或 Ticket 迁移到其他节点，旧 Ticket 不恢复；节点重新 Ready 后继续接收新 Add 和 `MatchService.Tick` 调用。完整身份和 Tick 流程见 [Router 物理节点与逻辑节点设计](./router-physical-logical-node.md)。

## 3. 三层核心能力

| 层                         | 输入                                                                 | 核心职责                                                                                | 输出                                      |
| -------------------------- | -------------------------------------------------------------------- | --------------------------------------------------------------------------------------- | ----------------------------------------- |
| ClientRouter（客户端路由） | 新增 Ticket、上游选定的`RuleKey` 与本地 ClientRouteTable           | 在 Ticket 调用节点选择一个承载该规则的 PhysicalNode，并建立 Ticket 生命周期内的节点归属 | 目标`OwnerRef` 与 MatchService Endpoint |
| Candidate Index（索引层）  | seed、Tick/Object Facts 的 Prefilter 视图、编译后的 `Prefilter` | 执行树形索引查询和集合运算，限制候选规模并产生安全超集                                  | 候选 DocSet                               |
| Evaluation（评估层）       | seed、候选和 Tick 内只读 `FactView`                                  | 生成候选 Object Facts、Top-L、构建 group，并执行 Join、Start、ForceStart 最终判断       | 合法 group 或无结果                       |

索引层的详细设计见：[Prefilter 树形索引初筛层设计](./index-prefiltering.md)。

## 4. Ticket 生命周期

```text
创建 Ticket
  -> 上游 RuleSelector 提供 RuleKey
  -> Ticket 调用节点 ClientRouter 选择 PhysicalNode
  -> 一一映射到 MatchService Endpoint，并形成唯一 OwnerRef
  -> MatchService.LocalDispatcher 校验并交给唯一 PhysicalNode
  -> PhysicalNode 按 RuleKey 定位并同步调用指定 LogicalNode
  -> LogicalNode 保存 Ticket 并写入 Active 与索引
  -> 等待所属 MatchService 开始 Tick

MatchService.Tick(now, matchLimit)
  -> PhysicalNode.BeginMatchRound(now)
  -> 按产出上限循环调用 PhysicalNode.ProduceMatch
  -> LogicalNodeSelector 选择一个可执行 LogicalNode
  -> 创建 Tick FactFrame
  -> 为 seed 惰性生成 Object Facts，Prefilter 生成候选安全超集
  -> 为候选惰性生成 Object Facts，评分与 Evaluation 共用 FactView
  -> Evaluation 构建并确认合法组
  -> 成功：顺序整体删除组成员并返回 MatchResult
  `-> 无结果：返回 NoMatch
```

调用频率、定时器和限速策略属于 MatchService 外层集成，不进入 PhysicalNode、LogicalNode 或 Router 协议；但一次服务 Tick 的匹配轮次与产出上限由 `MatchService.Tick` 明确控制。

## 5. MatchService Tick 与 PhysicalNode 单组产出

`MatchService.Tick(ctx, now, matchLimit)` 先调用 `PhysicalNode.BeginMatchRound(ctx, now)`，由每个 LogicalNode 的 `SeedOrderPolicy` 基于当前活跃 Ticket 生成完整顺序快照并把游标置零；再循环调用 `PhysicalNode.ProduceMatch(ctx)`，直到产出 `matchLimit` 个组或本轮全部 LogicalNode 的 Seed 都已耗尽。PhysicalNode 的 LogicalNodeSelector 游标不随轮次重置，继续按正常轮询选择符合资格且仍有未尝试 Seed 的 LogicalNode。

- 找到一个合法组、顺序整体移除成员并返回 `MatchResult`；
- 本次 seed 尝试预算耗尽；
- 当前轮 seed 已全部尝试；
- 当前等待池为空。

找到组或达到停止条件后立即结束本次 `ProduceMatch`。即使返回 NoMatch，也不能在同一次调用中再选择另一个 LogicalNode；是否继续调用由 MatchService 的本轮循环决定。Seed 游标在尝试前逐个推进，因此成功组之后尚未尝试的 Ticket 仍可在本轮后续调用中成为 Seed，而已经尝试过的 Ticket 直到下一轮开始前都不会再次成为 Seed。轮次开始后新增的 Ticket 只进入下一轮；已删除的 stale Seed 在读取时直接跳过。

PhysicalNode 的 LogicalNodeSelector 保存本地选择游标，可采用轮询或加权轮询；每个 LogicalNode 独立保存自己的 seed 轮次和索引状态，但不拥有独立执行线程。

PhysicalNode 也不通过 mutex（互斥锁）模拟串行。创建它的 owner goroutine 必须独占完整命令流，并同步向下调用 LogicalNode 和 Prefilter。GroupEvaluator、CandidateScore、FactProvider 和 ObjectFactProvider 回调都在该 goroutine 内执行，禁止重入 PhysicalNode 或启动并等待另一个会访问同一节点的 goroutine。

## 6. 核心对象关系

| 对象                                    | 框架职责                                                                                      |
| --------------------------------------- | --------------------------------------------------------------------------------------------- |
| `MatchService`                        | 外部完整匹配服务器；持有唯一 PhysicalNode，由 Tick 建立一轮匹配并控制最多产出多少组           |
| `PhysicalNode`                        | 与 MatchService 一一对应的算法实例；持有本地 LogicalNode 容器、LogicalNodeSelector 和选择游标 |
| `ClientRouteTable` / `ClientRouter` | 部署在 Ticket 调用节点，根据 RuleKey 选择 PhysicalNode，并解析唯一 MatchService Endpoint      |
| `LocalDispatcher`                     | 部署在 MatchService，校验 OwnerRef 并把命令交给唯一 PhysicalNode                              |
| `LogicalNodeSelector`                 | 部署在 PhysicalNode，为一次`PhysicalNode.ProduceMatch` 选择一个本地 LogicalNode             |
| `OwnerNode` / `LogicalNode`         | 持有本节点 TicketStore、Active、索引、计划与匹配轮次状态                                      |
| `Prefilter`                           | 描述树形初筛的执行结构                                                                        |
| `IndexStore`                          | 维护 Active 与物理索引，并根据`IndexQuery` 产生 DocSet（文档集合）                          |
| `SeedOrderPolicy` / `SeedRound`    | 在轮次开始时生成完整 Seed 排列，由统一游标跨 ProduceMatch 调用保存扫描位置                    |
| `GroupBuilder` / `GroupEvaluator`   | 构建 group 并完成最终正确性判断                                                               |
| `FactFrame` / `FactView`            | 固定 Tick Facts，按 TicketID 生成一次 Object Facts，并向评分与评估提供只读分层访问            |
| `MatchResult`                         | 一次 PhysicalNode.ProduceMatch 成功产出的最终匹配结果                                         |

### 6.1 全链路 Fact 生命周期

Fact 的数据模型和生命周期实现都位于中立 `internal/matchsystem/fact` 包，包括 `Values`、`Spec`、`Frame`、`View`、Provider、校验与深拷贝。`matchsystem.Facts`、`matchsystem.FactView` 和 `prefilter.Facts` 都只是零成本兼容别名，不存在转换或复制。Fact 契约由 `LogicalNodeConfig.Facts` 持有；构造 LogicalNode 时，同一份契约会注入 Prefilter 编译配置，因此不存在“Prefilter Facts”和“评估 Facts”两套声明。

```go
type LogicalNodeConfig struct {
    Facts     []FactSpec
    Prefilter prefilter.Config
    // ...
}

type ObjectFactProvider func(
    object *Ticket,
    now int64,
    tickFacts Facts,
) (Facts, error)
```

每次 ProduceMatch 创建一个 FactFrame：

```text
FactProvider(now)
  -> FactFrame 深拷贝并校验 Tick Facts（唯一自有副本）
  -> Prefilter TickSession 只读借用该副本
  -> 为 seed 首次生成 Object Facts
  -> Prefilter(seed, Tick Facts, Object Facts(seed))
  -> 为每个实际评分 candidate 首次生成 Object Facts
  -> CandidateScoreContext.Facts
  -> GroupEvaluatorContext.Facts
  -> ProduceMatch 结束后整体释放
```

FactFrame 不提升为整个 MatchRound 的缓存。一次成功匹配可能改变外部容量等动态 Fact，下一次 ProduceMatch 必须重新调用 FactProvider；否则即使本轮 Ticket 集合不发生 Add/Remove，也可能使用过期业务状态。Prefilter TickSession 与 FactFrame 保持分层，但只读借用 FactFrame 的 Tick Facts，不再建立第二份深拷贝。

Object Facts 按 TicketID 惰性缓存。同一个 Ticket 先作为 candidate、随后作为 seed 或 group member 时不会重新调用 Provider。Provider 返回值会被深拷贝，因此 Provider 可以在下一次对象调用时复用自己的 Map/slice 缓冲区。Tick/Object 两层不合并，所有回调只能通过只读 FactView 访问：

```go
tickFacts := ctx.Facts.Tick()
seedFacts, _ := ctx.Facts.For(ctx.Seed)
candidateFacts, _ := ctx.Facts.For(candidate)
memberFacts, _ := ctx.Facts.For(group[i])
```

`WithCandidateScore` 保留旧三参数回调；需要 Fact 时使用 `WithCandidateScoreContext`。`GroupEvaluatorContext` 直接包含 FactView。FactFrame 校验 `UNDECLARED_FACT`、`FACT_TYPE_COLLISION`、`FACT_TYPE_MISMATCH`、`FACT_VALUE_LIMIT` 和 `FACT_SCOPE_COLLISION`。对象 Fact 生成失败时，seed 失败会跳过该 seed，candidate 失败只跳过该 candidate；错误携带 TicketID 并继续处理其他对象。

## 7. 文档导航

- [Prefilter 树形索引初筛层设计](./index-prefiltering.md)：索引层的 过滤表达式、Query、Index、编译验证、Bitmap 执行、动态范围、Top-L 和测试验收。
- [Router 物理节点与逻辑节点设计](./router-physical-logical-node.md)：client-side routing（客户端路由）、MatchService 本地分发、物理/逻辑节点选择边界、Draining 和共享基础设施。
- [LogicalNode 负载均衡策略](./logical-node-selector.md)：Round Robin、平滑加权轮询、积压优先、最老等待优先和自定义 Selector。
- [Seed 顺序策略与匹配轮次](./seed-order-policy.md)：内置 Seed 排序策略、轮次快照、游标不重复契约与自定义扩展接口。

## 8. 当前实现边界

当前代码已经在 `internal/matchsystem/prefilter` 子包实现树形 `Prefilter`、MultiValue/Int64Range 索引、Roaring Bitmap 执行、编译契约和 `TickSession`，并由上层 `LogicalNode` 完成 bounded Top-L、GroupEvaluator 与 Greedy 建组。旧线性候选过滤入口已经移除。

`internal/identity`、`internal/common` 和 `internal/client` 分别实现主键、跨边界 DTO 与纯内存 ClientRouter；`internal/matchsystem` 实现 PhysicalNode、LogicalNode 和 Prefilter。`BeginMatchRound` 固化轮次，`ProduceMatch` 提供单组执行语义。

PhysicalNode 仍是最高代码边界。MatchService 的匹配轮次编排、LocalDispatcher、路由配置分发、远程传输、健康探测、TickRunner、RateLimiter client、网络与 IO 编排均由外部工程负责，也不进入 `prefilter` 子包。

外部工程可以使用自己的事件循环接收请求，但调用匹配核心的最终入口必须只有一个 owner goroutine；本仓库不提供多协程访问兼容层。
