# 匹配系统整体设计框架

> 状态：Prefilter、LogicalNode、主键/common、ClientRouter 和进程内 PhysicalNode 基线已实现；MatchService 宿主与远程协议尚未实现。
> 范围：只定义系统分层、核心对象和主流程；各层的算法、配置与接口细节由独立设计文档负责。

## 1. 设计目标

`MatchService` 是整个匹配服务器，每个 MatchService 内严格一一对应地承载一个 `PhysicalNode`（物理算法实例）。Ticket 发起方通过 ClientRouter 选择 PhysicalNode；MatchService 在合适时机调用该 PhysicalNode，从隔离逻辑节点中产出匹配结果。匹配核心只实现到 PhysicalNode，不实现 MatchService 的 TickRunner、RateLimiter client、网络或 IO 编排。核心也不解释地区、模式、玩法版本等业务字段，不内置具体匹配规则。

系统遵循以下边界：

- 每个 ClientRouter 实例绑定调用节点内自己的 owner goroutine；每个 PhysicalNode 实例绑定 MatchService 内自己的 owner goroutine，并在同一 goroutine 中向下驱动 LogicalNode 和 Prefilter。所有状态命令严格顺序执行，核心内部不使用锁、channel 或原子状态切换。
- Add、Remove、Get、Tick、Load、Drain、Stop 和配置替换不能分派到不同 goroutine；外部并发请求必须在进入核心前完成串行化。
- 一个 Ticket 在等待期间只归属于一个 `OwnerNode`，普通匹配不跨节点执行。
- 路由只决定 Ticket 的归属节点，不承担候选过滤和组合法性判断。
- 索引初筛只产生候选安全超集，最终合法性由评估层保证。
- MatchService 如何安排和限制调用属于外层集成；匹配核心只提供 `PhysicalNode.Tick`。LogicalNode 不拥有独立 Tick。
- 一次 `PhysicalNode.Tick` 只对应一次 LogicalNode 匹配尝试；被选逻辑节点无结果时，本次调用不会继续尝试第二个逻辑节点。
- 一次 PhysicalNode Tick 最多返回一个组，但被选 LogicalNode 内部可以连续尝试多个 seed。
- 同一轮的 seed 游标跨 Tick 保留，后续 Tick 不会重新从最高优先级 Ticket 开始。
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
        -> SeedCursor
        -> Prefilter + IndexStore（索引层）
        -> bounded Top-L + Ticket materialization
        -> GroupBuilder + GroupEvaluator（评估层）
        -> MatchResult 或 NoMatch

```

`MatchService` 是三层能力之外的服务器与调用边界，持有唯一 PhysicalNode；LogicalNodeSelector 属于 PhysicalNode。Ticket 调用节点只负责选择 PhysicalNode 并路由远程 Ticket 命令，不触发匹配执行。MatchService 内部是否使用 TickRunner 或 RateLimiter client 不属于本框架的匹配核心设计。

在物理/逻辑拓扑中，`MatchService : PhysicalNode = 1 : 1`。MatchService 是服务器，PhysicalNode 是由稳定 PhysicalNodeID 标识的算法实例；本文的 `OwnerNode` 对应 PhysicalNode 内一个数据隔离的 `LogicalNode`（逻辑节点），`OwnerRef = LogicalNodeKey + PhysicalNodeID`。故障后在原部署位置重启 MatchService 并重建同一 PhysicalNodeID，不把 LogicalNode 或 Ticket 迁移到其他节点，旧 Ticket 不恢复；节点重新 Ready 后继续接收新 Add 和 `PhysicalNode.Tick` 调用。完整身份和 Tick 流程见 [Router 物理节点与逻辑节点设计](./router-physical-logical-node.md)。

## 3. 三层核心能力

| 层 | 输入 | 核心职责 | 输出 |
| --- | --- | --- | --- |
| ClientRouter（客户端路由） | 新增 Ticket、上游选定的 `RuleKey` 与本地 ClientRouteTable | 在 Ticket 调用节点选择一个承载该规则的 PhysicalNode，并建立 Ticket 生命周期内的节点归属 | 目标 `OwnerRef` 与 MatchService Endpoint |
| Candidate Index（索引层） | seed、固定 now/Fact 的 `TickSession`、编译后的 `Prefilter` | 执行树形索引查询和集合运算，限制候选规模并产生安全超集 | 有界候选集合 |
| Evaluation（评估层） | seed 与已物化候选 | 挑选候选、构建 group，并执行 Join、Start、ForceStart 等最终合法性判断 | 合法 group 或无结果 |

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
  -> 等待所属 MatchService 调用 PhysicalNode.Tick

PhysicalNode.Tick
  -> LogicalNodeSelector 选择一个可执行 LogicalNode
  -> Prefilter 按 seed 生成候选安全超集
  -> Evaluation 构建并确认合法组
  -> 成功：顺序整体删除组成员并返回 MatchResult
  `-> 无结果：返回 NoMatch
```

调用频率、定时器和限速策略属于 MatchService 外层集成，不进入 PhysicalNode、LogicalNode 或 Router 协议。

## 5. PhysicalNode Tick

MatchService 调用唯一 `PhysicalNode.Tick`。PhysicalNode 由自己的 LogicalNodeSelector 从符合资格的 LogicalNode 中选择一个，随后该逻辑节点从自己的 seed 游标继续，依次尝试 seed，直到发生以下任一结果：

- 找到一个合法组、顺序整体移除成员并返回 `MatchResult`；
- 本次 seed 尝试预算耗尽；
- 当前轮 seed 已全部尝试；
- 当前等待池为空。

找到组或达到停止条件后立即结束本次 Tick。即使返回 NoMatch，也不能在同一次 Tick 中再选择另一个 LogicalNode。新 Ticket 不插入当前 seed 序列之前；已删除的 stale seed 在读取时直接跳过。

PhysicalNode 的 LogicalNodeSelector 保存本地选择游标，可采用轮询或加权轮询；每个 LogicalNode 独立保存自己的 seed 轮次和索引状态，但不拥有独立执行线程。

PhysicalNode 也不通过 mutex（互斥锁）模拟串行。创建它的 owner goroutine 必须独占完整命令流，并同步向下调用 LogicalNode 和 Prefilter。GroupEvaluator、CandidateScore 和 FactProvider 回调都在该 goroutine 内执行，禁止重入 PhysicalNode 或启动并等待另一个会访问同一节点的 goroutine。

## 6. 核心对象关系

| 对象 | 框架职责 |
| --- | --- |
| `MatchService` | 完整匹配服务器；对外接收 Ticket 命令并持有唯一 PhysicalNode，但不属于匹配核心实现范围 |
| `PhysicalNode` | 与 MatchService 一一对应的算法实例；持有本地 LogicalNode 容器、LogicalNodeSelector 和选择游标 |
| `ClientRouteTable` / `ClientRouter` | 部署在 Ticket 调用节点，根据 RuleKey 选择 PhysicalNode，并解析唯一 MatchService Endpoint |
| `LocalDispatcher` | 部署在 MatchService，校验 OwnerRef 并把命令交给唯一 PhysicalNode |
| `LogicalNodeSelector` | 部署在 PhysicalNode，为一次 `PhysicalNode.Tick` 选择一个本地 LogicalNode |
| `OwnerNode` / `LogicalNode` | 持有本节点 TicketStore、Active、索引、计划与匹配轮次状态 |
| `Prefilter` | 描述树形初筛的执行结构 |
| `IndexStore` | 维护 Active 与物理索引，并根据 `IndexQuery` 产生 DocSet（文档集合） |
| `SeedScheduler` / `SeedCursor` | 生成一轮 seed 顺序并跨调用保存扫描位置 |
| `GroupBuilder` / `GroupEvaluator` | 构建 group 并完成最终正确性判断 |
| `MatchResult` | 一次 PhysicalNode Tick 成功产出的最终匹配结果 |

## 7. 文档导航

- [Prefilter 树形索引初筛层设计](./index-prefiltering.md)：索引层的 过滤表达式、Query、Index、编译验证、Bitmap 执行、动态范围、Top-L 和测试验收。
- [Router 物理节点与逻辑节点设计](./router-physical-logical-node.md)：client-side routing（客户端路由）、MatchService 本地分发、物理/逻辑节点选择边界、Draining 和共享基础设施。

## 8. 当前实现边界

当前代码已经在 `internal/matchsystem/prefilter` 子包实现树形 `Prefilter`、MultiValue/Int64Range 索引、Roaring Bitmap 执行、编译契约和 `TickSession`，并由上层 `LogicalNode` 完成 bounded Top-L、GroupEvaluator 与 Greedy 建组。旧线性候选过滤入口已经移除。

`internal/identity`、`internal/common` 和 `internal/client` 分别实现主键、跨边界 DTO 与纯内存 ClientRouter；`internal/matchsystem` 实现 PhysicalNode、LogicalNode 和 Prefilter。`LogicalNode.TickOne` 为 PhysicalNode 提供单组执行语义。

PhysicalNode 是最高代码边界。MatchService、LocalDispatcher、路由配置分发、远程传输、健康探测、TickRunner、RateLimiter client、网络与 IO 编排仍由外部工程负责，也不进入 `prefilter` 子包。

外部工程可以使用自己的事件循环接收请求，但调用匹配核心的最终入口必须只有一个 owner goroutine；本仓库不提供多协程访问兼容层。
