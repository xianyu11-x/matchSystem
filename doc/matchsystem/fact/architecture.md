# internal/matchsystem/fact 架构说明

fact 是中立的 Fact 模型与生命周期包，负责 Values 容器、Fact declaration、三层
scope、深拷贝、结构检查、Contract validator、一次匹配尝试的 Frame，以及两个 Provider
接口。它不依赖 Prefilter/Evaluation，不保存 Ticket 池或 Match 状态。

## 1. 数据模型

~~~text
Values
├── StringLists  map[string][]string
├── Uint64Lists  map[string][]uint64
└── Int64Values  map[string]int64

Spec = Name + Type + MaxValues + Scope
Type = strings | int64 | uint64s
Scope = tick | object | match
~~~

同一个名称不能同时出现在三个 Values map；Inspect 会按类型和名称排序返回 Field 与
NameSet。Values 的 map/slice 在接收方 API 中均视为只读，Clone 用于建立 owner 副本。

## 2. 三层 Fact 生命周期

| 层 | 生成/拥有者 | 典型读取方 |
| --- | --- | --- |
| Tick | FactProvider，一次 ProduceMatch | Frame、Prefilter、CanJoin、CanComplete |
| Object | ObjectProvider，每个 Ticket/Frame 至多一次 | seed/candidate 的 Prefilter 和 Evaluation |
| Match | MatchFactProvider.Initialize/OnJoin | CanJoin/CanComplete |

Fact scope 是结构约束，不是 fallback 选择。Tick 与 Object 同名会被
ValidateScopes 拒绝；Match snapshot 还必须包含 Contract 声明的所有 match-scoped
键（空集合必须保留键）。

## 3. Frame 与所有权

NewFrame 先建立不可变 Validator，深拷贝并验证 Tick layer；Frame.Object 首次请求某个
Ticket 时给 ObjectProvider 传入 Ticket/Tick 的副本，复制和验证返回值后缓存。后续同一
TicketID 直接复用成功值；失败也缓存 error，防止一次调用中重复执行不稳定的 provider。

Frame.View 只提供同步只读视图。Tick、Object 返回的 Values 不应跨 owner mutation barrier
保存；LogicalNode 在调用结束前消费它们。Frame 不负责 Match Fact，后者由 MatchFactProvider
和上层原子提交拥有。

## 4. Validator

NewValidator 检查每个 Spec 的名称、Type、Scope、MaxValues 和重复声明。ValidateLayer
检查当前层实际出现的字段是否已声明、类型/scope 是否匹配、集合是否超限，但不要求
该 scope 的每个声明都出现。ValidateCompleteMatch 在此基础上要求所有 match-scoped
Fact 都出现；CloneValidatedMatch 校验成功后再深拷贝。

Fact 包只做 Fact contract 校验；Ticket Attribute 的命名空间由 contract.AttributeValidator
负责，不能用 Fact validator 代替。

## 5. Provider 边界

Provider 类型：

- Provider(ctx, now) 创建 Tick Values；
- ObjectProvider(ticket, now, tick) 创建 Object Values；
- MatchFactProvider.Initialize/OnJoin 返回完整 Match Values。

上层 LogicalNode 会捕获 Match Provider error/panic/cancel，包装为 Evaluation error，并
在校验成功后再接受快照。fact 包本身不执行重试、评分、索引查询或事务提交。

实现入口：[fact.go](../../../internal/matchsystem/fact/fact.go)、
[frame.go](../../../internal/matchsystem/fact/frame.go)、
[provider.go](../../../internal/matchsystem/fact/provider.go)。
