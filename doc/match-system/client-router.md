# Client Router 路由与选择器

`pkg/client` 负责在调用方进程内为新 Ticket 选择 PhysicalNode（物理节点），并解析已保存
的 Owner。它只返回目标身份和 Endpoint，不执行网络请求、服务发现或健康探测。宿主应让
同一 owner goroutine（状态所有者协程）串行调用 Router 的 `Replace`、
`UpdatePhysicalNode`、`RouteNew` 和 `ResolveOwner`。

## 路由身份

路由器、匹配核心和模拟器共用 `pkg/identity` 定义的规则键、逻辑节点键和 Owner。
宿主可直接在路由配置、请求和匹配核心之间传递这些值。

| 路由类型或字段 | 语义与约束 |
| --- | --- |
| `identity.RuleKey` | 包含非零 `RuleID uint64` 和可选的 `Namespace string` 别名 |
| `identity.LogicalNodeKey` | 由 `Rule identity.RuleKey` 和非零 `PlacementID` 组成，ID 的底层类型为 `uint64` |
| `identity.OwnerRef` | 由 `LogicalNode identity.LogicalNodeKey` 和非零 `PhysicalNodeID` 组成，ID 的底层类型为 `uint64` |
| `RouteRequest` | 包含 Rule、非零 `TicketID`、`AffinityKey uint64` 和 `RequestID uint64`；后两者可为零 |
| `RouteDecision` | 包含非零 `DecisionID uint64`、Owner 和 Endpoint |
| `ResolvedOwner` | 包含已验证的 Owner 和当前 Endpoint |

`RuleID` 唯一确定规则。`Namespace` 用于展示。同一 `RuleID` 可以部署到多个物理节点，
部署位置由 `PlacementID` 区分。

`AffinityKey` 为零时，`RouteNew` 使用 `TicketID` 作为亲和键。默认策略下，相同规则和
亲和键在同一路由表中得到确定性路由；调用方可以用非零亲和键让不同 Ticket 共享选择依据。
`RequestID` 为零表示未提供，它参与 DecisionID 的计算，但不是默认策略的节点选择依据。

`DecisionID` 依次将 RuleID、TicketID、实际 AffinityKey、RequestID、
选中节点的 PlacementID 和 PhysicalNodeID 写为六个大端 uint64，取 SHA-256 摘要前
64 位，结果为零时映射到 1。该摘要可能碰撞，只适合作为决策标识。应用应保存完整
Owner，并使用 `ResolveOwner` 按原节点解析当前 `Endpoint`。

`client.RuleKey`、`client.LogicalNodeKey` 和 `client.OwnerRef` 是共享类型的别名，
路由请求、决策和解析结果由 `common` 定义。
`identity` 提供数值身份、校验和组合键的字符串表示。共享结构用 Go 标准库字段标签
输出十进制字符串，文本输入由读取端解析。
场景与 HTTP 以规范十进制字符串承载这些 ID，详见
[模拟器架构](../simulator/architecture.md#4-多物理节点运行时)。

## 路由表

用 `NewRouteTable(RouteTableConfig)` 构造不可变路由表，再调用 `NewRouter(table)`。
物理节点和规则路由声明 Endpoint、启用状态、逻辑节点、物理节点、权重和各逻辑部署的
`TicketCount`。构造时检查身份、节点引用、重复部署、正权重和非负 Ticket 数。
`TicketCount` 的零值表示空队列。`Replace` 用新表整体替换路由视图。

### 从物理节点快照发布路由

宿主可调用 `NewRouteTable(client.RouteTableConfig{})` 创建空路由表，并在 `NewRouter`
创建时绑定选择器。首次发布部署前，`RouteNew` 返回 `ErrNoRoute`。

在节点的 owner goroutine 中调用 `PhysicalNode.RouteSnapshot()`，一次取得物理节点 ID
和所有逻辑部署的身份、生命周期及等待 Ticket 数。宿主为上报的部署补充 Endpoint、节点
和部署启用开关及权重，将 `Ready` 且启用的部署发布为可选候选。`Draining` 部署以禁用
状态保留，供 `ResolveOwner` 解析已有 Owner。部署从完整快照消失后，下一次发布移除
其路由，原 Owner 返回 `ErrOwnerNotFound`。

宿主按各自的部署方式采集、传输并发布快照，并在 Router 的 owner goroutine 中提交更新。
完整的进程内接入见[独立 Go 消费者](../../examples/go-consumer)。

### 按物理节点更新路由

`router.UpdatePhysicalNode(physical, routes)` 以一个 PhysicalNode 为最小更新单位。
`physical.PhysicalNodeID` 指定目标，必须大于零。`routes` 非空时，方法新增或替换该
物理节点的完整配置及其全部 RuleRoute；未列出的该节点旧部署会被移除。其他物理节点及其
部署保持原状。Router 零值也可通过此方法注册第一个节点，并使用默认选择器。

例如，在节点 42 上只保留规则 7 的部署：

```go
err := router.UpdatePhysicalNode(
    client.PhysicalRoute{PhysicalNodeID: 42, Endpoint: "match-42:9000", Enabled: true},
    []client.RuleRoute{{
        LogicalNode: identity.LogicalNodeKey{
            Rule:        identity.RuleKey{Namespace: "ranked", RuleID: 7},
            PlacementID: 103,
        },
        PhysicalNodeID: 42,
        Weight:         2,
        Enabled:        true,
        TicketCount:    10,
    }},
)
if err != nil {
    return err
}
```

传入空切片或 `nil` 表示节点下线，会移除该物理节点及其全部部署。此时只需提供非零
PhysicalNodeID，Endpoint 和 Enabled 不参与校验；对不存在的节点重复下线也会成功：

```go
if err := router.UpdatePhysicalNode(client.PhysicalRoute{PhysicalNodeID: 42}, nil); err != nil {
    return err
}
```

`routes` 非空时，每条 RuleRoute 都必须指向目标节点。更新后的完整路由表仍需满足
`NewRouteTable` 的约束，包括非零身份、非空 Endpoint、正权重、非负 Ticket 数、全表 LogicalNodeKey 唯一，
以及同一 PhysicalNode 上 RuleKey 唯一。新部署不能与其他节点已有的 LogicalNodeKey
冲突。`RouteNew` 仅使用物理节点和规则部署均已启用的候选。

变更视图前先完成完整校验，再发布新的不可变路由视图；任何错误都保留原表。即使多个 Router
最初共享同一 RouteTable，更新也只影响当前 Router。原子性由同一 owner goroutine 的命令
边界保证，宿主须串行调用路由、解析和更新方法。

同一规则在该节点更新前后均存在时，新 RuleRoute 保留该规则原来的候选位置，即使
PlacementID 发生变化。新加入的规则部署追加到该规则已有候选之后；其他节点的候选保持
相对顺序。新节点加入或下线后重新加入时，其部署也追加到对应规则的候选末尾。此顺序供
依赖候选顺序的自定义选择器使用。

`ResolveOwner` 继续按完整 Owner 精确解析：节点或部署被禁用时仍可解析，Endpoint 更新
后返回当前地址；节点下线后，该节点已有 Owner 返回 `ErrOwnerNotFound`。如果部署被删除
或迁移到其他物理节点，原 Owner 也可能无法解析。需要继续解析已有 Owner 时，可保留完整
部署列表并禁用物理节点或对应部署。宿主应在移除旧部署前处理其已有 Ticket。更改
PhysicalNodeID，或需要原子更新多个节点时，应构造完整新表并调用 `Replace`。

## 路由选择策略

| 构造器 | 行为 |
| --- | --- |
| `NewWeightedRendezvousSelector()` | 默认策略，按配置权重和亲和键选择目标。 |
| `NewQueueWeightedRandomSelector()` | 每次按 `1 / (1 + TicketCount)` 抽样。队列数为 0 和 1 的两个候选分别有约 ⅔ 和 ⅓ 的选中概率。配置权重和亲和键不参与这次抽样。 |
| `NewWeightedPrioritySelector(ticketLimit)` | 阈值须为正。优先从队列数低于阈值的候选中选取配置权重最高的部署。同权重取物理节点 ID 最小者。所有候选均达到阈值时，取队列最短者，并按权重降序和物理节点 ID 升序处理并列。 |

默认策略是 weighted rendezvous hashing（加权最高随机权重哈希）。它按照规则、归一化后
的亲和键、物理节点 ID 和权重选择候选；哈希输入以固定大端字节序编码，同分时选择数值更小
的 PhysicalNodeID。
相较旧的字符串编码和排序，相同业务请求在升级后可能被分配到不同节点。
已保存的 Owner 应使用完整身份解析；已有 Ticket 需要继续使用原 Owner。

两种队列策略使用宿主最近发布的可选部署及其 `TicketCount`。权重优先策略在高权重节点
上报达到阈值时转向其他合格部署，后续上报回落到阈值以下时恢复原有优先级。`RouteNew`
只做选路，Ticket 数由下一次快照提供。两次同步间可能连续选择同一节点，因此阈值用于
选路偏好，实际队列数可以超过阈值。

在创建 Router 时选择策略。后续通过 `Replace` 或 `UpdatePhysicalNode` 更新路由表，
Router 继续使用创建时传入的选择器。

```go
selector, err := client.NewWeightedPrioritySelector(50)
if err != nil {
    return nil, err
}
return client.NewRouter(table, client.WithRouteSelector(selector))
```

宿主可通过 `NewRouter(table, WithRouteSelector(selector))` 注入实现了
`RouteSelector` 的选择器：

```go
type RouteSelector interface {
    Select(RouteSelection) (OwnerRef, error)
}
```

`RouteSelection.Request` 是已经将零亲和键替换为 TicketID 的请求。
选择器收到的请求和候选使用数值身份，路由返回的 Owner 携带配置中的展示别名。
`RouteSelection.Candidates` 是按路由表构造或更新后维护的顺序提供的独立候选快照，包含该规则下已启用
的物理节点和路由。每个 `RouteCandidate` 包含 `Owner`、`Weight` 和 `TicketCount`。选择器只负责
从这些候选中返回 Owner，修改快照不会改变路由表。无候选时 Router 返回 `ErrNoRoute`；
选择器错误原样返回，返回不合格 Owner 时返回 `ErrInvalidRouteSelection`。候选过滤、
结果校验、Endpoint 解析和 `ResolveOwner` 仍由 Router 负责；后者也允许解析已禁用但仍
在表中的 Owner，供已有 Ticket 使用。自定义策略应自行保证需要的确定性和并发约束，
不得假设 Router 会重试其他节点。

例如，以下策略按表中顺序选择首个合格目标：

```go
type firstCandidate struct{}

func (firstCandidate) Select(selection client.RouteSelection) (client.OwnerRef, error) {
    if len(selection.Candidates) == 0 {
        return client.OwnerRef{}, client.ErrNoRoute
    }
    return selection.Candidates[0].Owner, nil
}

func newRouter(table *client.RouteTable) (*client.Router, error) {
    return client.NewRouter(table, client.WithRouteSelector(firstCandidate{}))
}
```

## 主要入口

| 符号 | 用途 |
| --- | --- |
| `PhysicalRoute`、`RuleRoute`、`RouteTableConfig` | 声明部署路由 |
| `NewRouteTable`、`RouteTable` | 校验并保存不可变路由视图 |
| `NewRouter`、`RouterOption`、`WithRouteSelector` | 创建路由器并可选配置选择策略 |
| `Replace` | 原子切换到完整路由表，适用于多节点变更 |
| `UpdatePhysicalNode` | 新增或完整替换单个物理节点及其部署；空部署列表使节点下线 |
| `RouteSelector`、`RouteSelection`、`RouteCandidate` | 宿主提供的节点选择策略及其输入 |
| `WeightedRendezvousSelector`、`NewWeightedRendezvousSelector` | 默认的无状态加权 Rendezvous 选择器 |
| `NewQueueWeightedRandomSelector` | 以逻辑部署的等待 Ticket 数计算随机抽样权重 |
| `NewWeightedPrioritySelector` | 以队列阈值筛选候选，再按配置权重选择并在全满时回退到最短队列 |
| `RouteNew` | 为新 Ticket 返回包含 Owner 和 Endpoint 的路由决策 |
| `ResolveOwner` | 按完整已保存 Owner 查找 Endpoint，不重新选路 |

源码入口见[路由器](../../pkg/client/router.go)、[路由表](../../pkg/client/route_table.go)、
[选择器](../../pkg/client/route_selector.go)和[队列选择器](../../pkg/client/queue_route_selector.go)。
示例与跨模块验证见[独立 Go 消费者](../../examples/go-consumer)；身份与选择器的取舍见
[共享规则身份与选择器 ADR](../design-decisions/adr/client-router-identities.md)和
[路由快照与队列策略 ADR](../design-decisions/adr/client-router-queue-strategies.md)。
