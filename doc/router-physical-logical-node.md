# Router 物理节点与逻辑节点设计

> 状态：身份、common DTO、ClientRouter 和进程内 PhysicalNode/LogicalNode 基线已实现；MatchService 网络宿主、远程协议及运维能力仍是目标设计。
> 范围：定义 Router（路由层）如何把 Ticket 路由到 PhysicalNode（物理算法实例），以及 MatchService（匹配服务器）、PhysicalNode 与 LogicalNode（逻辑匹配节点）之间的承载、执行、隔离和故障边界。
> 前置框架：[匹配系统整体设计框架](match-system-framework.md)。节点内候选查询仍遵循 [Prefilter 树形索引初筛层设计](index-prefiltering.md)。

## 1. 设计结论

本设计采用两级路由、单一归属和显式分区：

1. `MatchService` 是完整的匹配服务器；`PhysicalNode` 是部署在其中的匹配算法实例。二者严格一一对应，但不是同一个职责对象。
2. 一个 MatchService 只承载一个 PhysicalNode；PhysicalNode 由稳定的 `PhysicalNodeID` 标识，MatchService 进程重启后仍重建同一 PhysicalNodeID。
3. 一个物理节点可以承载多个 `LogicalNode`，但同一物理节点内一个 `RuleID` 最多对应一个逻辑节点。
4. 每个逻辑节点只加载一个 `RuleID`，并独占自己的 TicketStore、Active、Indexes、PlanGeneration、SeedOrderPolicy、SeedRound 和匹配执行状态。
5. 相同 `RuleID` 可以部署到不同物理节点；这些逻辑节点共享规则语义，但默认是数据互不访问的独立 matching partition（匹配分区），不是共享状态的 HA replica（高可用副本）。
6. `RuleID` 只在一个物理节点内足以定位逻辑节点，不能单独作为集群内的全局节点身份。
7. 新 Ticket 的发起方仍通过 `ClientRouter` 选择一个承载目标 RuleID 的 PhysicalNode；由于 PhysicalNode 与 MatchService 一一对应，该选择同时确定唯一的 MatchService Endpoint（服务端点）和本地 LogicalNode。
8. Ticket 被接受后，后续 Remove 和查询始终使用原 `OwnerRef`，不重新负载均衡。
9. Router 只建立和解析 Ticket 归属，不执行 Prefilter、组合法性判断、匹配调度或 Ticket 迁移。
10. 外部 `MatchService.Tick` 建立一轮匹配并控制产出组数；PhysicalNode 对它暴露 `BeginMatchRound` 与 `ProduceMatch`。MatchService、定时调度、限速器和服务器 IO 均不属于本仓库匹配核心实现。
11. PhysicalNode 的 `LogicalNodeSelector` 选择一个本地 LogicalNode，并同步执行一次匹配尝试；一次 `ProduceMatch` 最多产出一个匹配结果。
12. MatchService 必须把外部请求汇聚成唯一 owner goroutine 的顺序命令流；PhysicalNode 及其全部 LogicalNode 不共享 worker pool，也不允许跨 goroutine 调用。
13. MatchService 崩溃后只在原部署位置重启并重建同一 PhysicalNodeID 及原 Placement；节点重新 Ready 后，新 Add 自动进入恢复节点。进程内旧 Ticket 和匹配状态不恢复。

最重要的身份关系是：

```text
RuleID                           本物理节点内的逻辑节点查找键
RuleKey                         集群内的规则语义身份
RuleKey + PlacementID           一个稳定、隔离的逻辑匹配分区
OwnerRef                        该分区固定所在的物理节点
```

## 2. 目标与非目标

### 2.1 目标

- 支持一个 `MatchService` 承载一个 PhysicalNode，并由该 PhysicalNode 管理多个规则不同、数据绝对隔离的逻辑节点。
- 支持相同规则在多个物理节点上水平分布。
- 为每个 Ticket 建立唯一、可验证、可在后续命令中复用的 Owner（所有者）。
- 由 `MatchService.Tick` 控制单轮组数上限，并通过 `PhysicalNode.ProduceMatch` 每次最多产出一个匹配结果。
- 支持物理节点进入 Ready、NotReady、Draining 等状态，并支持逻辑节点独立进入 Loading、Ready、Draining、Failed 等状态。
- 调用节点使用不可变 `ClientRouteTable`（客户端路由表）完成全部远程选路。
- 明确进程内存模式下的故障边界，不把“同 RuleID 的其他节点”误当成可直接接管数据的副本。

### 2.2 非目标

- Router 不根据地区、模式、段位等业务字段推导 `RuleID`；业务层应在调用 Router 前完成 RuleSelector（规则选择）。
- Router 不执行候选过滤、跨节点 Prefilter 查询、建组或最终规则评估。
- Router 不保存 Ticket 正文、索引、seed 游标或匹配结果。
- Router 不把 Prefilter 配置更新等同于路由拓扑更新。
- Ticket 调用节点不远程触发匹配执行；MatchService 的定时触发和限速属于外层集成。
- LogicalNode 不创建自己的 Tick 循环；所有逻辑节点都由所属 PhysicalNode 在 MatchService Tick 中同步驱动。
- 首版不提供运行中 Ticket 的透明迁移、跨物理节点联合匹配或进程崩溃后的无损恢复。

RuleSelector 属于 Ticket 调用节点的业务 Gateway，并在 ClientRouter 之前执行；它不部署在 MatchService，也不属于 Router、LogicalNode 或匹配核心。

## 3. 术语与身份模型

### 3.1 RuleID 不是全局 NodeID

`RuleID` 是正数 `int32`，表示一套稳定的匹配语义和匹配人口域，例如 `1001`（业务侧可将其映射为 ranked-5v5）。它不表示：

- 物理进程；
- 配置版本；
- 某一个精确的数据分区；
- Prefilter 的 `configId`、`revision` 或 `GenerationID`。

热更新时 `RuleID` 保持不变，使用 `RuleRevision` 和 `Fingerprint` 表示版本与内容。否则每次规则更新都会被 Router 视为新的人口域，导致等待池被意外拆分。

一个 ClientRouter 只处理一个 Cluster/Environment（集群/环境），该作用域由调用节点的本地配置确定，不进入每个路由键。若系统存在多租户，作用域内的规则键为：

```go
// 设计草案
type RuleKey struct {
    Namespace string
    RuleID    int32
}
```

下文为简洁而写“本地 `RuleID` 唯一”时，指 canonical RuleKey（规范规则键）唯一；单 Namespace 部署时两者等价。

### 3.2 三层身份

| 身份 | 示例 | 作用 |
| --- | --- | --- |
| `PhysicalNodeID` | `physical-07` | PhysicalNode 算法实例的稳定身份；通过一一对应关系定位 MatchService |
| `PlacementID` | `ranked-5v5-p03` | 一个稳定、隔离的逻辑匹配分区 |
| `RuleID` | `1001` | 本地查找键和规则语义身份；必须大于 0 |

一个逻辑分区的稳定身份是：

```go
// 设计草案
type LogicalNodeKey struct {
    Rule        RuleKey
    PlacementID string
}
```

该分区的精确路由引用为：

```go
// 设计草案
type OwnerRef struct {
    LogicalNode   LogicalNodeKey
    PhysicalNodeID string
}
```

本文的 Owner 专指“固定承载该 LogicalNode 的 PhysicalNode”，不是 MatchService 本身，也不是玩家、Ticket 所有者或可竞争的主节点。首版 Placement 固定在 PhysicalNode 上；MatchService 进程崩溃后在原部署位置重建同一 PhysicalNode，不执行 Owner 转移。

- `PlacementID` 表示一个稳定的隔离匹配分区；首版把它固定部署在指定 PhysicalNode，不支持跨物理节点迁移。
- 同一 RuleKey 下 `PlacementID` 全局唯一；进程重启继续使用原 PlacementID，已经永久删除的 ID 不再分配给另一个分区。

在物理节点内部仍可使用：

```go
map[RuleKey]*LogicalNode
```

因此“`RuleID` 唯一代表一个节点”只在单个 PhysicalNode 的本地查找范围内成立。调用节点配置、日志、指标和协议不得只用 `RuleID` 表示精确节点。

现有 Prefilter 文档中的节点身份应映射为稳定的 `LogicalNodeKey`：索引、TickSession 和 generation 按逻辑分区绑定。`OwnerRef` 表示该分区固定所在的 PhysicalNode，不应作为索引内容身份；命令进入节点时必须同时验证完整 OwnerRef。

### 3.3 相同 RuleID 的节点是什么

相同 `RuleID` 的多个逻辑节点组成一个 `RulePlacementSet`：

```text
RuleID = 1001
  |- Placement p01 -> Physical A，独立 Ticket 数据
  |- Placement p02 -> Physical B，独立 Ticket 数据
  `- Placement p03 -> Physical C，独立 Ticket 数据
```

在“逻辑节点数据绝对隔离、普通匹配不跨节点”的约束下，这三个节点是三个 matching partition。增加分区可以提升容量，但也会拆分候选人口，可能降低匹配质量。Router 不能消除这种算法层面的取舍。

同一 `RuleID` 的多个 Placement 在首版中始终是独立内存分区，不构成主备关系。持久化恢复或数据复制属于后续独立方案，Router 首版不定义。

## 4. 总体结构

```text
Ticket 调用节点 / Gateway
  |- ClientRouteTable（本地不可变路由配置）
  |- ClientRouter（按 RuleKey 选择 PhysicalNode）
  `- MatchServiceClient
                         |
             +-----------+-----------+
             |                       |
      MatchService A            MatchService B
      |- LocalDispatcher        |- LocalDispatcher
      `- PhysicalNode A         `- PhysicalNode B
         |- LogicalNodeSelector    |- LogicalNodeSelector
         |- RuleNode(r1,p1)        `- RuleNode(r1,p2)
         `- RuleNode(r2,p1)

MatchService.Tick(now, matchLimit)
  -> PhysicalNode.BeginMatchRound(now)
  -> 循环 PhysicalNode.ProduceMatch
     -> LogicalNodeSelector 选择一个仍有未尝试 Seed 的 LogicalNode
     -> 同步执行一次单组匹配尝试
     -> 返回 MatchResult 或 NoMatch

MatchService 外层：Transport / IO；进入核心前汇聚为 owner goroutine 命令流
PhysicalNode 持有：LogicalNodeSelector / 选择游标 / map[RuleKey]*LogicalNode
LogicalNode 独占：TicketStore / Active / Index / PlanGeneration / SeedOrderPolicy / SeedRound
```

Transport、IO 以及 Tick/限速编排属于外部 MatchService；它们不能直接并发调用核心，必须先汇聚到唯一 owner goroutine。LogicalNodeSelector、选择游标和逻辑节点容器属于 PhysicalNode。匹配核心不定义 TickRunner、RateLimiter client 或 worker pool。Router 在 MatchService 内只需要一个顺序转交命令的 LocalDispatcher 适配边界。

Router 由部署在两端的两个 data plane（数据面）组件组成：

- 调用节点侧 `ClientRouter`：根据 `RuleKey + AffinityKey` 从本地 ClientRouteTable 选择一个承载该规则的 PhysicalNode；再通过一一对应关系解析 MatchService Endpoint，并形成精确 `OwnerRef`。
- MatchService 侧 `LocalDispatcher`：请求到达后，先验证请求指定的是本服务承载的唯一 PhysicalNode，再由该 PhysicalNode 根据 `RuleKey` 查找本地唯一 LogicalNode，并比较完整 `LogicalNodeKey`。

两端可以复用同一个协议包中的身份类型，但不共享内存状态，也不能共用含糊的 `NodeID`。Router 运行组件只部署在 Ticket 调用节点和 MatchService 两端。

## 5. 核心不变量

以下规则是实现和测试必须共同维护的 requirements（契约）：

1. 同一物理节点在任意非终止状态下最多保留一个相同 `RuleKey` 的逻辑节点。
2. 每个逻辑节点只加载一个 `RuleKey`；RuleID 别名必须在进入 Router 前解析为 canonical RuleKey（规范规则键）。
3. 一个等待中的 Ticket 同时只属于一个 `OwnerRef`。
4. 普通 Prefilter 的 Universe 永远是当前逻辑节点自己的 Active Bitmap，不得引入其他逻辑节点的 DocID。
5. TicketStore、Active、Indexes、PlanGeneration、seed 游标和匹配执行状态均不得跨逻辑节点共享。
6. `DocID` 只需在单个逻辑节点内唯一；跨节点引用必须同时携带 `OwnerRef`。
7. 新 Ticket 可以基于调用节点当前 ClientRouteTable 选择 Owner；已接受 Ticket 不因客户端路由表变化自动换 Owner。
8. Add、Remove 和 Ticket 查询都必须验证精确 Owner；状态变更命令必须幂等。
9. PhysicalNode 重启期间不接收请求；重新 Ready 后，新的 Add 和重试 Add 使用同一个 PhysicalNodeID 进入恢复节点。
10. MatchService 必须在服务端确认 PhysicalNode 和目标 LogicalNode 都处于 Ready，不能只信任客户端路由配置。
11. 路由错误不等于随机降级到其他 `RuleID`，也不触发跨节点候选扫描。
12. 同一 RuleKey 下 PlacementID 唯一且不重新分配；首版一个 Placement 固定属于一个 PhysicalNode，进程重启仍使用原 PlacementID。
13. 一个运行中的 MatchService 必须且只能承载一个 PhysicalNode；一个 PhysicalNode 也不能同时挂到多个 MatchService。

## 6. 组件职责

### 6.1 组件部署表

| 部署位置 | 组件 | 职责 |
| --- | --- | --- |
| Ticket 调用节点 | `ClientRouteTable` | 保存 PhysicalNode、Endpoint、Placement、RuleKey 和权重的本地不可变配置 |
| Ticket 调用节点 | `ClientRouter` | 为新 Ticket 选择 PhysicalNode，由一一对应关系得到 MatchService Endpoint，并形成精确 OwnerRef |
| Ticket 调用节点 | `MatchServiceClient` | 按 ClientRouter 结果发送 Add、Remove 和查询命令 |
| MatchService | `LocalDispatcher` | 校验 PhysicalNodeID 和服务状态，把命令同步交给唯一 PhysicalNode |
| MatchService（外部集成） | 匹配轮次 | 每次 Tick 调用 `BeginMatchRound(now)` 固化全部 LogicalNode Seed 顺序，并按 `matchLimit` 调用 `PhysicalNode.ProduceMatch` |
| MatchService（外部集成） | 定时 / 限速 / Transport / IO / owner command stream | 把外部请求汇聚为单 owner goroutine；不由匹配核心实现 |
| PhysicalNode | `LogicalNodeSelector` + 选择游标 | 每次 ProduceMatch 选择一个仍有未尝试 Seed 的本地 LogicalNode |
| PhysicalNode | `map[RuleKey]*LogicalNode` | 保存本地唯一规则槽位及完全隔离的匹配状态 |

调用节点如何获得或更新 ClientRouteTable 属于部署配置输入，不由 Router 协议定义；MatchService 不参与路由配置分发。

### 6.2 调用节点侧 ClientRouter

`ClientRouter` 负责：

- 在 owner goroutine 的命令边界读取并整体切换 `ClientRouteTable`；
- 按 RuleKey 找到承载该规则且可接收新 Ticket 的 PhysicalNode；
- 使用稳定 AffinityKey 在这些 PhysicalNode 之间做确定性负载分配；
- 由选中的 PhysicalNode 得到其唯一 MatchService Endpoint 和本地 LogicalNode，返回 `RouteDecision`，不保存 Ticket 正文；
- 对已经绑定的请求从本地表解析原 Owner 的 Endpoint。

调用节点侧组件不负责：

- 解释业务字段并选择 RuleID；
- 触发 MatchService 内部匹配执行、选择 LogicalNode 或执行匹配；
- 在超时后盲目改投其他 Owner；
- 迁移 Ticket 或合并不同逻辑节点的候选集合。

MatchService 如何调度或限制 Tick 属于外层服务器集成；LogicalNodeSelector（逻辑节点选择器）属于 PhysicalNode。二者都不属于 ClientRouter。

### 6.3 MatchService

`MatchService` 是整个匹配服务器。它与 PhysicalNode 严格一一对应，负责：

- 对外监听、连接管理、序列化和远程 IO；
- 创建、持有并暴露唯一一个 PhysicalNode；
- 每次 Tick 开始时调用 `PhysicalNode.BeginMatchRound(ctx, now)` 固化全部 LogicalNode Seed 顺序，不重置 LogicalNodeSelector 游标；
- 按调用方给定的 `matchLimit` 重复调用 `PhysicalNode.ProduceMatch`；
- 把 IO 请求汇聚为唯一 owner goroutine 的顺序核心调用；
- 提供本地 Health/Describe 查询、Draining 和服务器级可观测性。

上述 MatchService 能力属于外部部署与集成边界，本仓库不实现 MatchService、TickRunner、RateLimiter client、网络服务器或 IO 编排。MatchService 不选择 LogicalNode，也不保存逻辑节点选择游标；它只驱动自己的唯一 PhysicalNode，PhysicalNode 再同步选择并调用 LogicalNode。

### 6.4 PhysicalNode

`PhysicalNode` 是匹配算法实例，不是网络服务器。它负责：

- 以稳定 `PhysicalNodeID` 标识自身，并验证本地 Placement；
- 管理 `map[RuleKey]*LogicalNode`，保证本物理节点内 RuleKey 唯一；
- 持有 LogicalNodeSelector 和选择游标；
- 每次 `ProduceMatch` 只选择并同步调用一个仍有未尝试 Seed 的 LogicalNode；
- 返回 `MatchResult`、`NoMatch` 或错误，不在同一次 ProduceMatch 中改选第二个节点；
- 通过 `BeginMatchRound` 原子建立全部 LogicalNode 的 SeedRound；逻辑节点选择游标始终正常连续推进。

PhysicalNode 不监听网络、不解析远程 Endpoint、不获取限速 token，也不持有 ClientRouteTable。它的 `BeginMatchRound` 和 `ProduceMatch` 只由所属 MatchService 调用。

### 6.5 LogicalNode

`LogicalNode` 是现有框架 `OwnerNode` 的具体化，也是隔离和 Ticket 所有权的最小运行单元。它独占：

- `TicketStore` 与 TicketID 去重表；
- `IndexStore`、Active DocSet 和可回收的本地 `uint32 DocID` 分配器；
- `RuleRevision`、PlanGeneration 和 Facts；
- SeedOrderPolicy、SeedRound 和单次匹配执行状态。

LogicalNode 是普通内存对象，不创建独立执行线程或后台事件循环。一个 PhysicalNode 绑定一个 owner goroutine，该 goroutine 直接同步调用所有 LogicalNode。Load、Add、Remove、Get、BeginMatchRound、ProduceMatch、配置切换、Drain、Stop 和 Describe 必须进入同一条顺序命令流；实现不使用 mutex（互斥锁），也不允许每个 LogicalNode 自行启动 goroutine。

### 6.6 LogicalNodeSelector

LogicalNodeSelector 属于 PhysicalNode，只在 MatchService 调用 `PhysicalNode.ProduceMatch` 时运行，本身不创建周期性任务，也不知道外层是否使用限速器：

1. 从本物理节点中筛选允许执行匹配的 LogicalNode；正常服务时为 Ready，排空已有 Ticket 时可以是 Draining。
2. 使用默认 round-robin、smooth weighted round-robin、largest queue、oldest waiting 或自定义确定性算法选择一个节点；具体接口见 [LogicalNode 负载均衡策略](logical-node-selector.md)。
3. 只调用被选节点一次；无论返回匹配结果、无结果还是业务错误，本次 ProduceMatch 都立即结束。
4. 保存选择游标，使本轮后续 ProduceMatch 继续选择，而不是总从第一个 RuleKey 开始。

### 6.7 Ticket 路由与 MatchService Tick 边界

Add Ticket 路径需要精确选择 LogicalNode，因为 Ticket 从接受开始就固定归属于该分区：

```text
Ticket 调用节点 RuleSelector
  -> ClientRouter 按 RuleKey + AffinityKey 选择 PhysicalNode
  -> 由 PhysicalNode 一一映射到 MatchService Endpoint，并形成 OwnerRef
  -> MatchServiceClient.AddTicket
  -> MatchService.LocalDispatcher 校验 OwnerRef
  -> 唯一 PhysicalNode 定位并同步调用指定 LogicalNode.Add
```

匹配执行不经过 ClientRouter，也不由 Ticket 调用节点远程触发：

```text
MatchService.Tick(now, matchLimit)
  -> PhysicalNode.BeginMatchRound(now)
  -> 循环调用唯一 PhysicalNode.ProduceMatch
     -> LogicalNodeSelector 选择一个本地 LogicalNode
     -> 直接同步调用一次单组匹配尝试
  -> 返回不超过 matchLimit 个 MatchResult
```

ClientRouter 只负责 Ticket 命令路由，并且其主要路由决策是选择 PhysicalNode；LocalDispatcher 和 PhysicalNode 不得为 Add Ticket 重新选路；外层 Tick 调度也不使用 ClientRouteTable。

## 7. 客户端路由表与本地启动

### 7.1 ClientRouteTable

```go
// 设计草案
type ClientRouteTable struct {
    PhysicalNodes map[string]PhysicalRouteEntry
    ByRule        map[RuleKey][]PhysicalNodeRuleEntry
}

type PhysicalRouteEntry struct {
    PhysicalNodeID string
    Endpoint       string
    Weight         uint32
    Enabled        bool
}

type PhysicalNodeRuleEntry struct {
    PhysicalNodeID string
    LogicalNode    LogicalNodeKey
    Weight         uint32
    Enabled        bool
}
```

`ByRule` 的每个候选项表示“一个承载该 RuleKey 的 PhysicalNode”。同一 PhysicalNode 内该 RuleKey 唯一，因此选中 PhysicalNode 后即可确定 LogicalNode；再从 `PhysicalNodes` 得到与它一一对应的 MatchService Endpoint。

ClientRouter 与匹配核心一样由 owner goroutine 串行调用。收到新配置时，先完整解析和校验新表，再在命令边界通过普通指针赋值整体替换；不能在 RouteNew/ResolveOwner 执行中途修改 map，也不提供并发 RouteNew/Replace 保证。

ClientRouteTable 只表达调用方配置，不证明服务端当前 Ready。调用节点可以根据本进程的请求失败、主动探测和 circuit breaker（熔断器）暂时屏蔽 Endpoint；不同调用节点的健康视图允许短暂不一致。MatchService 仍必须验证请求中的 PhysicalNodeID、LogicalNodeKey 和本地状态。

动态队列长度可以用于摘除明显过载节点或缓慢调整权重，但不能让每个请求都按瞬时负载重新排序，否则会产生路由抖动和重试不一致。

### 7.2 MatchService 本地启动

推荐顺序：

```text
MatchService 启动
  -> 根据部署配置创建唯一 PhysicalNode，并使用稳定 PhysicalNodeID
  -> PhysicalNode 状态进入 Starting
  -> 按顺序为每个 RuleID 预留本地槽位
  -> 编译规则并准备逻辑节点索引和 owner 状态
  -> 校验部署配置中的 Placement 仍属于本 PhysicalNodeID
  -> LogicalNode 进入 Ready
  -> PhysicalNode 满足服务条件后进入 Ready
  -> 开始接受 ClientRouter 发来的请求
```

本地 RuleID 在 owner goroutine 中按顺序检查并预留。若准备失败，槽位在逻辑节点完全停止后释放。系统不接受并发 Load，因此不需要锁或原子预留。

ClientRouteTable 与 MatchService 本地部署配置必须由部署者保持一致。两边不一致时采用 server-authoritative validation（服务端权威校验）：不存在的 Placement、错误的 PhysicalNodeID 或未 Ready 的 LogicalNode 由 MatchService 明确拒绝，不做自动注册、自动修正或跨节点改投。

## 8. 新 Ticket 路由

### 8.1 输入

```go
// 设计草案
type RouteRequest struct {
    Rule          RuleKey
    TicketID      uint64
    AffinityKey   string
    RequestID     string
}
```

- `RuleKey` 必须由上游显式提供。
- `AffinityKey` 是一致性哈希输入。组队 Ticket 可使用 PartyID；没有更强亲和要求时默认使用 TicketID 的十进制字符串。
- `RequestID` 是 Add 操作的幂等键。

ClientRouter 不读取候选 Ticket，也不允许规则回调决定路由。

### 8.2 选择算法

首版推荐 weighted rendezvous hashing（加权最高随机权重哈希）：

1. 从 `ByRule[RuleKey]` 过滤出客户端配置为 Enabled、且未被本调用进程暂时熔断的 PhysicalNode。
2. 对 `RuleKey + AffinityKey + PhysicalNodeID` 计算稳定分数。
3. 按配置权重选择最高分 PhysicalNode；该节点内唯一的 RuleKey 槽位随之确定。
4. 形成精确 `OwnerRef`，并根据 PhysicalNode 与 MatchService 的一一对应关系解析 Endpoint；MatchService 再执行服务端 Ready 校验。

它具有以下性质：

- 相同 AffinityKey 只保证在相同 ClientRouteTable 内容和权重下选择相同 PhysicalNode；
- 节点变化时只重映射部分新 Ticket；
- 不需要 Router 保存逐 Ticket 路由表；
- 静态权重可以表达不同物理节点的容量差异。

不要默认优先本机节点。按 ingress（入口）位置偏置会让容量分布依赖流量入口，并削弱同一 AffinityKey 的稳定性。确有跨机房成本需求时，应把 locality（本地性）作为明确的路由策略版本，而不是隐式捷径。

一致性哈希不是长期 affinity binding（亲和绑定）。Placement 增删或权重变化后，同一 PartyID 的新 Ticket 可能选择不同分区。若多个独立 Ticket 在整个 Party 生命周期内必须共置，调用方必须保存首个 `RouteDecision` 并为后续 Ticket 复用其 Owner，不能只依赖重新计算哈希。

### 8.3 接受与返回

核心接口采用两阶段协议，调用方必须先取得并保存路由决定，再发送 Add：

```text
RouteNew -> 返回 RouteDecision
  -> 调用方保存 RouteDecision
  -> AddTicket(RouteDecision, Ticket, RequestID)
  -> 目标 MatchService
  -> LocalDispatcher 校验 RuleKey / PlacementID / PhysicalNodeID
  -> LogicalNode 对 common.Ticket 深拷贝一次，并在 storedTicket 中分配本地 DocID
  -> owner goroutine 在目标 LogicalNode 中顺序执行幂等 Add
  -> 返回 RouteToken
```

```go
// 设计草案
type RouteToken struct {
    TicketID uint64
    Owner    OwnerRef
}
```

`RouteToken` 只是 Ticket 的精确路由与幂等绑定，不提供可转移的所有权保护。它可以是受保护的 opaque token（不透明令牌），也可以是内部可信协议中的结构体。调用方必须保存它，并在 Ticket 后续命令中原样携带。

两阶段协议保证 Add 响应丢失时，调用方仍持有原 `RouteDecision`，可以用相同 RequestID 重试同一 PhysicalNode；在同一次进程运行期间不会重复创建。若节点已经完成重启，该请求会作为空状态恢复后的新 Add 进入同一 PhysicalNode。若对外只暴露单次 `RouteAndAdd`，Gateway 必须保存 `RequestID -> RouteDecision/RouteToken` 的 admission binding（准入绑定），避免错误改投其他 Placement。

### 8.4 后续命令不重新选 Owner

Remove、Update 和 GetStatus 的路由流程是：

```text
RouteToken
  -> 精确 PhysicalNodeID
  -> 精确 RuleID / PlacementID
  -> 校验完整 OwnerRef
  -> 逻辑节点幂等执行
```

即使当前路由表已经把相同 AffinityKey 指向其他分区，已有 Ticket 仍使用原 Owner。拓扑变化只影响尚未被接受且没有复用既有亲和绑定的新 Ticket。

若后续命令只携带 TicketID，调用方必须在自己的业务状态中保存 `TicketID -> RouteToken`；Router 不保存该映射。

## 9. 生命周期与可路由状态

### 9.1 物理节点状态

```text
Starting -> Ready -> Draining -> Stopped
   ^          |
   |          v
   `------ NotReady
              `-> 停止旧进程，在原部署槽位重新 Starting
```

### 9.2 逻辑节点状态

```text
Loading -> Ready -> Draining -> Stopped
   |         |
   `-------> Failed -> 重启整个 MatchService 后重新 Loading
```

Prefilter 的 Decoding、Compiling、PreparingIndexes、Active 等状态属于逻辑节点内部的规则 generation 状态，不替代上述服务状态。

### 9.3 不同操作的资格

| 状态 | 新 Add | 已有 Ticket 命令 | 可被 MatchService Tick 选择 | 说明 |
| --- | --- | --- | --- | --- |
| Loading | 拒绝 | 拒绝 | 否 | 尚未拥有完整运行契约 |
| Ready | 允许 | 允许 | 是 | 正常服务 |
| Draining | 拒绝 | 允许 | 是 | Tick 可以继续产出已有 Ticket，直至排空 |
| NotReady | 拒绝 | 拒绝 | 否 | 物理节点暂时不可用，等待原节点重启 |
| Failed / Stopped | 拒绝 | 拒绝 | 否 | 不可服务 |

物理节点进入 Draining 时，其所有逻辑节点立即从“新 Add 候选”中摘除，但仍可处理精确 RouteToken 指向的已有请求，并可通过本地 Tick 消化已有 Ticket。排空条件至少包括：

- Active Ticket 为 0；
- 没有正在执行的 Tick；
- 没有未完成的节点内 IO。

到达排空期限但仍有 Ticket 时，首版应报告并停止下线，不得静默把 Ticket 改投其他同 RuleID 节点。若运维仍强制停止进程，剩余内存 Ticket 按失败丢失处理，由上游决定是否重新提交。

## 10. 数据隔离与共享基础设施

### 10.1 必须隔离

逻辑节点之间禁止直接访问以下对象：

- Ticket 指针、TicketStore 和 TicketID 映射；
- DocID 分配器、Active Bitmap 和 posting；
- Prefilter 执行器与可变运行状态；
- Facts、seed 序列和 Tick 游标；
- 匹配执行游标和命令幂等记录。

`LogicalNode` 不应接收 PhysicalNode 的 `map[RuleKey]*LogicalNode` 或 MatchService 全局容器引用。跨节点管理只通过 MatchService 命令和只读描述完成。

### 10.2 可以共享

以下无状态或只写观测基础设施可以由 MatchService 提供，但不能直接调用或修改匹配核心：

- 网络 listener、连接池、序列化器和外部客户端；
- 外部计时器只能向 owner 调度下一条 Tick 命令，不能直接执行 Tick；
- 日志、指标和 tracing（链路追踪）基础设施。

匹配核心不启动 worker、后台索引任务或节点级事件循环。配置编译、索引准备以及结果应用都必须在 owner goroutine 的明确命令边界内完成；观测基础设施只能接收同步产生的不可变日志或指标值。

### 10.3 PhysicalNode 调用边界

PhysicalNode 只暴露 `BeginMatchRound` 和 `ProduceMatch` 两个执行能力。前者原子固化所有 LogicalNode 的本轮 Seed 排列和时间，后者只允许选择一个 LogicalNode 并执行一次匹配尝试；返回 `NoMatch` 或错误时，不在同一次调用中改选第二个逻辑节点。PhysicalNode 的节点选择游标不随轮次重置。

调用频率、限速 token、定时器和服务器生命周期均停留在 PhysicalNode 之外，不进入 PhysicalNode、LogicalNode 或 Router 协议，也不成为匹配核心状态。

## 11. 客户端健康视图、重启与重试

### 11.1 客户端快速失败与服务端校验

ClientRouteTable 可能与服务端实际状态短暂不一致，因此目标 MatchService 必须重新验证：

- PhysicalNodeID 和 LogicalNodeKey 是否匹配部署配置；
- 当前状态是否允许该操作。

每个调用节点可以维护仅对本进程有效的 Endpoint 健康视图或 circuit breaker。它只用于快速失败，不能替代 MatchService 自身校验，也不会在调用节点之间同步；因此不同调用节点短暂得到不同的可用节点集合是允许的。

物理节点发生致命故障时，本地状态进入 NotReady 并停止 Tick，随后重启原 MatchService 部署槽位，不把 LogicalNode 交给其他 PhysicalNode。PhysicalNodeID 和 Placement 保持不变，节点从空内存状态进入 Starting。调用节点在 Ticket 请求失败后暂时熔断该 Endpoint；探测或后续请求确认服务重新 Ready 后，再把它纳入本进程的新 Add 候选。

首版依赖部署系统保证旧进程已经终止后才启动新进程，不提供应用层 split-brain（脑裂）防护。若运行环境无法保证这一点，必须先补充单实例约束或进程隔离，不能靠 Router 缓存解决。

### 11.2 幂等键

建议所有 Ticket 状态变更命令携带稳定操作 ID：

- `AddTicket(TicketID, RequestID)`；
- `RemoveTicket(TicketID, OperationID)`；

网络超时后的处理顺序：

1. 节点 Ready 时，Add 使用已保存的原 RouteDecision 和 RequestID 重试相同 PhysicalNode；其他 Ticket 命令使用相同 RouteToken 和操作 ID 请求原节点。
2. 节点重启后旧 Ticket 状态不存在；Remove 返回 `TICKET_NOT_FOUND`，不能投递给其他同 RuleID 节点。
3. 重启完成后，相同 RouteDecision 的 Add 可以作为恢复后的新 Add 进入该节点；其幂等记录也已清空，因此调用方必须接受旧内存状态已经丢失。

系统对外语义是 at-least-once delivery + idempotent state machine（至少一次投递 + 幂等状态机），不承诺跨网络的 exactly-once（严格一次）。

## 12. 故障和扩缩容语义

### 12.1 故障矩阵

| 场景 | 新 Ticket | 已有 Ticket | 推荐行为 |
| --- | --- | --- | --- |
| 单个 LogicalNode Failed | 摘除该 Placement | 该节点内存数据不可用 | 重启整个 MatchService，不把 Placement 迁到其他物理节点 |
| PhysicalNode NotReady | 暂时摘除其全部 Placement | 其内存 Ticket 同时丢失 | 使用相同 PhysicalNodeID 重启；Ready 后恢复新 Add 路由 |
| 固定节点尚未 Ready | 等待 | 原 Token 仍指向固定 PhysicalNode | 返回 `NODE_UNAVAILABLE`，恢复后沿原 RouteDecision 重试 |
| Placement 已删除或改变 | 重新 RouteNew | 原绑定不再有效 | 返回 `ROUTE_STALE` |
| Prefilter 更新失败 | 不改变路由 | 继续使用最后有效 generation | 保持 Ready，并记录 rejected revision |
| 无可用 Placement | 拒绝 | 不影响其他 Owner | 返回 `RULE_UNAVAILABLE`，不降级到其他 RuleID |

### 12.2 内存模式的明确边界

当前目标继承进程内匹配池模型。物理节点崩溃时：

- 其他相同 RuleID 节点没有失败节点的 Ticket、索引和匹配执行状态；
- 部署系统只在原 PhysicalNodeID 上启动新 MatchService 进程，不把 LogicalNode 状态迁移到其他物理节点；
- 重启后的 TicketStore、索引、seed 游标和幂等记录均从空状态开始；
- 原 RouteToken 仍指向同一 PhysicalNode，但对应的旧 Ticket 已不存在；
- 上游若决定重新提交 Ticket，应作为新的 Add 处理，并按业务需要保留原 `CreatedAt`；

本设计不提供无损故障恢复。若未来需要持久化重放，应建立独立状态恢复方案，但不改变首版 Router 的固定 Placement 语义。

### 12.3 扩容

增加相同 RuleID 的 Placement 时：

1. 新逻辑节点以 Loading 启动并完成规则、索引和同步状态准备。
2. MatchService Ready 后，将新 Placement 加入各 Ticket 调用节点的 ClientRouteTable 配置。
3. 收到新配置的调用节点才会把新 Ticket 分布到该节点。
4. 旧 Ticket 留在原 Owner，直至匹配、取消或该进程失败；不会转移到新 Placement。

增加分区会缩小每个节点的候选人口。上线前应同时评估吞吐、匹配等待时间和匹配质量，而不是只评估 CPU。

### 12.4 缩容

首版只支持 drain-first（先排空）缩容：

```text
Ready -> Draining -> 从新 Add 路由摘除 -> 处理已有 Ticket -> Drained -> Stopped
```

缩容时先从调用节点的 ClientRouteTable 配置中禁用目标 Placement，再让 MatchService 进入 Draining。尚未更新配置的调用节点可能继续发送 Add，MatchService 必须返回 `NODE_DRAINING`。首版不支持迁移；若无法排空，运维必须选择继续等待或接受剩余内存 Ticket 丢失。

## 13. 规则版本与热更新衔接

`RuleID`、Prefilter 配置身份和运行版本必须分离：

```text
RuleID             = 稳定的规则语义和路由人口域
ConfigID           = 配置对象身份
RuleRevision       = 单调发布序号
Fingerprint    = 规范化内容指纹
PlanGeneration     = 逻辑节点内不可变运行代
```

这些版本不能互相替代。特别是：

- Prefilter 更新不自动触发 Ticket 重路由或迁移。
- ClientRouteTable 更新不修改 PhysicalNode 内任一 LogicalNode 的活动 PlanGeneration。
- 一个逻辑节点的 PlanGeneration 只在该 LogicalNode 被 Tick 选中执行前切换，并在本次执行内固定；失败时保留最后有效版本。
- 若要 canary（灰度）运行不同规则内容，应引入显式 VariantID 或新 RuleID，不能让相同 RuleID 静默混用不同语义。
- Ticket 不绑定创建时的 RuleRevision，而是在每次所属 LogicalNode 被 Tick 选中执行时使用当前不可变 active generation。

[Prefilter JSON 配置与热更新实现方案](json-prefilter-hot-reload.md) 继续负责单个规则运行代、索引代和 LogicalNode 被 PhysicalNode Tick 选中时的执行边界发布。它不负责 Router 拓扑切换，也不暗示已有 Ticket 会因配置更新迁移。

热更新文档已将原来的“整个 MatchSystem”作用域收敛为一个 RuleKey 的本地 RuleRuntime（规则运行时），不能解释为承载多个 RuleID 的整个 PhysicalNode。一个 PhysicalNode 内不同 RuleKey 的逻辑节点拥有各自的 PlanManager 和 `activeGeneration`，可以独立准备与发布；`NodeID` 映射为 LogicalNodeKey，本地 PlanGeneration 不能持有远程节点的索引指针。

Router 不协调跨 PhysicalNode 的规则版本。如果业务要求相同 RuleID 的所有 Placement 使用同一 RuleRevision/Fingerprint，部署者必须先更新各 MatchService，再更新 Ticket 调用节点的 ClientRouteTable；不同调用节点和 MatchService 短暂观察到不同版本属于 client-side routing（客户端路由）模式的明确边界。

## 14. 接口草案

本节同时展示外层集成协议和匹配核心接口，用于固定边界而不是扩大实现范围：当前仓库只实现 14.2 的 PhysicalNode 执行入口和 14.3 的本地节点管理；14.1 的 ClientRouter、TicketAdmission 以及 14.4 的远程命令信封由外部工程实现。

### 14.1 调用节点 ClientRouter

```go
// 设计草案
type ClientRouter interface {
    RouteNew(ctx context.Context, req RouteRequest) (RouteDecision, error)
    ResolveOwner(owner OwnerRef) (ResolvedOwner, error)
}

type RouteDecision struct {
    DecisionID string
    Owner      OwnerRef
    Endpoint   string
}
```

`RouteNew` 只用于尚未建立归属的新 Ticket。它先选择 PhysicalNode，选中结果记录在 `Owner.PhysicalNodeID`；由于 PhysicalNode 与 MatchService 一一对应，随后解析出的 `Endpoint` 就是承载该物理节点的服务器地址。Add 发生前，调用方取得并保存完整 `RouteDecision`。配置或 DNS 变化后，调用方通过稳定 OwnerRef 在本地再次 `ResolveOwner`，不能重新选择 PhysicalNode。RouteToken 内同样保存该 OwnerRef。

当前 `internal/client.ResolveOwner` 从当前 RouteTable 解析 Endpoint，但不检查 `Enabled`，因此禁用的 Placement 仍可服务已有 Ticket。路由配置发布方必须先禁用旧条目、等待其 Ticket 和外部 route binding 排空，再删除条目；若立即删除，调用方只能继续使用自己保存的 RouteDecision，不能依赖新表恢复旧 Owner。

目标 MatchService 的准入接口独立返回 RouteToken：

```go
// 设计草案
type TicketAdmission interface {
    AddTicket(
        ctx context.Context,
        decision RouteDecision,
        requestID string,
        ticket *Ticket,
    ) (RouteToken, error)
}
```

`DecisionID + RequestID` 共同约束幂等准入。目标端必须确认 decision 中的完整 Owner 仍有效：固定节点尚未 Ready 时返回 `NODE_UNAVAILABLE`，调用方等待恢复后沿原 decision 重试；ClientRouteTable 与服务端 Placement 配置不一致时返回 `ROUTE_STALE`，调用方必须更新本地配置。

### 14.2 PhysicalNode 执行入口

```go
// internal/matchsystem 当前基线；不是远程接口
type PhysicalNodeAPI interface {
    ID() identity.PhysicalNodeID
    Add(ctx context.Context, owner identity.OwnerRef, ticket *common.Ticket) (uint32, error)
    Remove(ctx context.Context, owner identity.OwnerRef, ticketID uint64) (bool, error)
    Get(ctx context.Context, owner identity.OwnerRef, ticketID uint64) (*common.Ticket, bool, error)
    BeginMatchRound(ctx context.Context, now int64) error
    ProduceMatch(ctx context.Context) (PhysicalMatchResult, error)
}

type PhysicalMatchResult struct {
    LogicalNode identity.LogicalNodeKey
    Match       *common.Match
}
```

`common.Ticket` 是唯一 Ticket 定义，`matchsystem.Ticket` 仅为类型别名。`Add` 建立唯一一次深拷贝并由目标 LogicalNode 持有；`Get` 返回用于立即同步读取的借用指针，调用方不得修改，也不得跨下一条节点命令持有；匹配提交会先删除节点内 `storedTicket` 和索引，再把同一个 `*common.Ticket` 指针放入 `common.Match.Tickets`，结果接收方取得所有权，不再执行出池拷贝。

`PhysicalNode.BeginMatchRound` 只在新一轮 MatchService Tick 开始时调用，使用同一个 `now` 为每个 LogicalNode 构建 SeedRound，并且不改变 LogicalNodeSelector 的轮询位置。`PhysicalNode.ProduceMatch` 不再接收时间，也不接收组数上限或限速 token；它只从当前轮次选择一个节点并同步调用一次，即使返回 NoMatch 或错误，也不在同一次调用中尝试第二个节点。

### 14.3 本地节点管理

```go
// internal/matchsystem 当前基线
type LogicalNodeManager interface {
    Load(ctx context.Context, spec LogicalNodeSpec) error
    BeginDrain(ctx context.Context, key identity.LogicalNodeKey) error
    Stop(ctx context.Context, key identity.LogicalNodeKey) error
    Describe() []LogicalNodeDescriptor
}

type LogicalNodeSpec struct {
    Key                identity.LogicalNodeKey
    Config             matchsystem.LogicalNodeConfig
    Rules              *matchsystem.RuleSet
    FactProvider       matchsystem.FactProvider
    ObjectFactProvider matchsystem.ObjectFactProvider
}
```

`LogicalNode` 直接持有主键、状态、Tick/Object FactProvider、TicketStore、索引、规则及匹配游标，不再存在第二个内部匹配池对象。`SeedFactProvider` 仅作为兼容字段保留，语义与 ObjectFactProvider 相同且不能同时配置。

`Load` 必须在 owner goroutine 中顺序预留完整 RuleKey，重复 RuleKey 返回 `DUPLICATE_LOCAL_RULE_KEY`。规则热更新通过现有逻辑节点的 PlanManager 完成，不通过再次 `Load` 第二个相同 RuleKey 的逻辑节点完成。

`RuleSet` 回调、`FactProvider` 和 `ObjectFactProvider` 在 owner goroutine 内同步执行，必须是不可变、无副作用且不可重入的函数；它们不得通过闭包再次调用所属 PhysicalNode 的 Add、Remove、Get 或 Tick。

LogicalNode 每次执行创建一个 Tick FactFrame：Tick Facts 生成并复制一次；ObjectFactProvider 对每个实际作为 seed 或评分 candidate 的 Ticket 按 TicketID 最多调用一次。Object Facts 随后通过 `CandidateScoreContext.Facts` 和 `GroupEvaluatorContext.Facts` 继续提供给评分、Join、Start、ForceStart，不局限于 Prefilter。

### 14.4 命令信封

```go
// 设计草案
type CommandEnvelope struct {
    Owner       OwnerRef
    CommandID   string
    TicketID    uint64
    Payload     any
}
```

LocalDispatcher 先校验 Owner，再把命令放入 owner goroutine 的顺序调用流，由其直接调用目标 LogicalNode。命令不得只带 RuleID。

## 15. 错误模型

建议使用稳定错误码：

| 错误码 | 含义 | 是否可换节点重试 |
| --- | --- | --- |
| `RULE_NOT_FOUND` | RuleKey 未部署 | 否 |
| `RULE_UNAVAILABLE` | ClientRouteTable 中没有启用的 Placement | 更新客户端配置后，新 Ticket 可重试 |
| `DUPLICATE_LOCAL_RULE_KEY` | 同物理节点重复加载 RuleKey | 否，修正部署配置 |
| `ROUTE_STALE` | ClientRouteTable 与 MatchService 本地 Placement 配置不一致 | 新 Ticket 更新客户端配置后可重试；不能因原节点暂时 NotReady 而改投 |
| `NODE_UNAVAILABLE` | 固定 PhysicalNode 尚未 Ready | 等待原节点恢复后重试 |
| `TICKET_NOT_FOUND` | Ticket 不存在，可能已完成、删除或随重启丢失 | 否；由上游决定是否重新 Add |
| `NO_LOGICAL_NODE_AVAILABLE` | 本轮没有仍可尝试 Seed 的逻辑节点 | MatchService 正常结束本轮 Tick |
| `UNKNOWN_RESULT` | Add 等状态变更请求可能已经执行，但调用方未收到结果 | 否，先查询原 Owner 状态 |
| `NODE_DRAINING` | 节点不接收新 Ticket | 新 Ticket 可重新 RouteNew |
| `IDEMPOTENCY_CONFLICT` | 同一操作 ID 对应不同请求内容 | 否 |

错误统一原则：

```text
路由错误 != 选择任意 RuleID
路由超时 != 原 Add 一定失败
Owner 失败 != 相同 RuleID 节点已经拥有旧数据
```

## 16. 可观测性

每条日志、指标和 trace 至少携带适用的身份维度：

```text
namespace
rule_id
placement_id
physical_node_id
rule_revision
plan_fingerprint
```

至少暴露：

- 每个 RuleID 的 Ready、Loading、Draining、NotReady、Failed Placement 数；
- 每个物理节点的逻辑节点数、Ticket 总数、内存、Tick 和 IO 使用量；
- 每个逻辑节点的 Ticket、Active、等待时间和最近被选择时间；
- MatchService.Tick 轮次数与目标/实际组数，PhysicalNode.ProduceMatch 调用数、逻辑节点选择分布、NoMatch 数、错误数和耗时；外层限速指标由 MatchService 自行定义；
- RouteNew 成功率、无节点、stale route、重试和哈希分布偏斜；
- 各调用节点本地熔断状态、探测延迟、节点不可用时长和进程重启次数；
- LogicalNodeSelector 的选择分布和公平性；
- 同 RuleID 的 RuleRevision/Fingerprint 不一致告警。

高基数的 TicketID 和 RequestID 只进入采样日志或 trace，不作为常规 metrics label（指标标签）。日志不得输出完整 Ticket 或任意动态业务字段值。

## 17. 测试与验收

### 17.1 身份与唯一性

- 一个 MatchService 启动时必须且只能创建一个 PhysicalNode，二者不能形成一对多或多对一关系。
- MatchService 暴露的 PhysicalNodeID 必须等于其唯一 PhysicalNode 的稳定 ID。
- 同一 PhysicalNode 顺序加载两个相同 RuleKey，第二次返回重复错误。
- 不同 PhysicalNode 可以各自加载相同 RuleKey。
- RuleID 相同但 PlacementID 或 PhysicalNodeID 不同的节点不会被混淆。
- PhysicalNode 重启前后 OwnerRef 保持不变，内存 Ticket 和匹配执行状态清空。
- 跨 Namespace 的同名 RuleID 不会发生本地 map 碰撞；进程重启保留 PlacementID，已删除 ID 不分配给其他分区。

### 17.2 路由性质

- 同一 ClientRouteTable 和 AffinityKey 的 Rendezvous 结果确定。
- ClientRouter 的选择结果是 PhysicalNodeID，并能唯一解析到承载它的 MatchService Endpoint。
- 增删 PhysicalNode 规则候选只改变预期比例的新 Ticket 归属。
- ClientRouter 本地熔断的节点不接收新 Ticket；尚未更新健康视图的其他调用节点可以继续尝试。
- 已有 RouteToken 不因 ClientRouteTable 更新自动改变。
- 固定 ClientRouteTable 内容和权重时，相同 AffinityKey 进入同一分区。
- 拓扑或权重变化后，严格 Party 亲和请求复用原 RouteDecision，不重新计算哈希。

### 17.3 隔离

- 两个逻辑节点允许重复 DocID，但 Prefilter 结果不能跨节点。
- 删除、单次匹配执行、索引重建和热更新只影响目标逻辑节点。
- 一个逻辑节点的 panic 或配置拒绝不会修改其他逻辑节点状态。
- 同一轮连续 ProduceMatch 按选择算法分布到符合资格的 LogicalNode；一个节点无结果时不会在同一次 ProduceMatch 中尝试第二个节点。
- 同一轮已经尝试过的 Ticket 不会重复成为 Seed；下一次 `BeginMatchRound` 后可重新尝试。
- 未调用 `PhysicalNode.ProduceMatch` 时不选择 LogicalNode，也不执行 Prefilter。
- Ticket 调用节点不调用 Tick；MatchService 只驱动本服务唯一的 PhysicalNode。
- 只有 PhysicalNode 内部的 LogicalNodeSelector 可以为 ProduceMatch 选择 LogicalNode。
- `PhysicalNode.ProduceMatch` 的接口和结果中不出现 TickRunner、RateLimiter client 或 token 状态。

### 17.4 故障注入

- ClientRouteTable 只在 owner goroutine 的命令边界整体替换；替换前后的 RouteNew 分别看到完整旧表或完整新表。
- 同一次进程运行期间，Add 响应丢失后使用已保存的同一 RouteDecision 和 RequestID 重试，不会创建重复 Ticket。
- 单次 RouteAndAdd facade 丢响应时从 admission binding 恢复原 Owner，不能重新选路；无绑定时返回 `UNKNOWN_RESULT`。
- MatchService 崩溃后只在原 PhysicalNodeID 重启，并使用空内存状态。
- 重启完成并重新 Ready 后，新 Add 自动进入恢复节点；旧 Remove 返回 NotFound。
- 重启后幂等记录重新开始；相同 RequestID 的 Add 被视为恢复节点上的新准入，并在新进程内保持幂等。
- 物理节点 Draining 时不再接收新 Add，但已有 Ticket 能完成或取消。
- Plan 更新失败保留旧 generation，且不改变路由归属。

## 18. 落地顺序

当前仓库已经完成 PhysicalNode 基线，包路径与依赖方向为：

```text
internal/identity      主键：PhysicalNodeID / RuleKey / LogicalNodeKey / OwnerRef
internal/common        唯一 Ticket 定义及跨边界 Match / Endpoint / Route DTO；不包含 DocID
internal/client        不可变 RouteTable 与 ClientRouter
internal/matchsystem   PhysicalNode / LogicalNode / 本地选择与匹配算法内核
```

已按以下顺序落地：

1. 固化 PhysicalNode 所需的 `PhysicalNodeID`、`PlacementID`、`RuleKey`、`LogicalNodeKey`、`OwnerRef` 和本地错误码。
2. 实现 `LogicalNode`，在同一个对象中维护主键、生命周期与完全隔离的匹配数据。
3. 实现 `PhysicalNode`，由它持有 `map[RuleKey]*LogicalNode`、本地唯一性、节点状态和 LogicalNodeSelector。
4. 在 PhysicalNode 上提供 Add、Remove、Get、Load、Drain、Describe 和一次 `Tick` 等同步入口。
5. 验证逻辑节点隔离、选择公平性、单次 Tick 至多尝试一个 LogicalNode，以及相同 RuleKey 可分布在不同 PhysicalNode。

尚未实现且不属于匹配核心的能力是 ClientRouteTable 配置分发、MatchService、LocalDispatcher、RouteToken、远程传输、健康探测、TickRunner、RateLimiter client、网络与共享 IO 编排。只有业务明确要求后，才另行设计持久化重放。

ClientRouter 当前实现为纯内存客户端库；它只消费完整 RouteTable 配置，不负责配置获取、服务发现或健康探测。MatchService 外部工程可以把远程请求适配到 `internal/matchsystem.PhysicalNode` 的同步入口。

上述步骤仍不包含跨逻辑节点候选查询。若未来要把同 RuleID 的所有 Ticket 当成一个全局候选池，应另立架构方案，因为它与本文“逻辑节点数据绝对隔离”的核心约束冲突。

## 19. 当前实现边界

本文同时包含已实现基线和后续目标设计。当前代码事实是：

- `internal/matchsystem.LogicalNode` 持有主键、生命周期、单个 TicketStore、已编译的 `prefilter.IndexStore`、RuleSet 和 seed 调度状态；Prefilter 核心索引初筛已接入单节点；
- `internal/identity` 实现可比较的物理、规则、逻辑分区和 Owner 主键；`internal/common.Ticket` 是唯一 Ticket 定义且不携带节点内 DocID，LogicalNode 以私有 `storedTicket` 关联 DocID；
- `internal/client` 实现单 owner Router、不可变 RouteTable、weighted rendezvous hashing（加权最高随机权重哈希）和 OwnerRef 精确解析；
- `internal/matchsystem` 实现一个 PhysicalNode 管理多个隔离 LogicalNode、本地 RuleKey 唯一、顺序 Add/Remove/Get、Draining、Describe、round-robin（轮询）选择与 Seed 游标；
- `PhysicalNode`、`LogicalNode` 和 Prefilter 不含锁，由外部 MatchService 的同一个 owner goroutine 串行驱动；`ProduceMatch` 最多产出一个组；
- MatchService、LocalDispatcher、远程路由、TickRunner 和 RateLimiter client 不属于本仓库匹配核心的实现目标；
- 固定 Index/Fact 契约下的 JSON 到 Prefilter Config/Plan 已实现；PlanGeneration 热更新和跨 Placement 发布仍属于后续目标架构。

PhysicalNode 仍是本仓库匹配代码的最高实现边界；后续工作不继续向 MatchService、TickRunner、RateLimiter client、网络服务器或共享 IO 编排扩张。
