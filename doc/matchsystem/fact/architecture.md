# internal/matchsystem/fact 架构说明

fact 是中立的 Fact 模型与生命周期包，负责 Values 容器、Fact declaration、三层
scope、深拷贝、测试/调试用 Contract validator、一次匹配尝试的 Frame，以及两个 Provider
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
| Tick | `matchsystem.FactProvider`，一次 ProduceMatch | Frame、Prefilter、CanJoin、CanComplete |
| Object | ObjectProvider，每个 Ticket/Frame 至多一次 | seed/candidate 的 Prefilter 和 Evaluation |
| Match | MatchFactProvider.Initialize/OnJoin | CanJoin/CanComplete |

Fact scope 是 Contract 和表达式编译期的结构约束，不是 fallback 选择。可信 Provider
必须让 Tick/Object 不产生冲突、让 Match snapshot 包含 Contract 声明的所有 match-scoped
键（空集合必须保留键）；这些 Provider 契约由测试中的 Validator 检查，生产运行时不重复
执行 ValidateScopes 或完整性校验。

## 3. Frame 与所有权

NewFrame 深拷贝 Tick layer 并建立本次尝试的所有权；Frame.Object 首次请求某个 Ticket
时给 ObjectProvider 传入 Ticket/Tick 的副本，复制返回值后缓存。后续同一 TicketID
直接复用成功值；失败也缓存 error，防止一次调用中重复执行不稳定的 provider。Frame
不在生产路径重复执行 Fact schema/type/scope/max-values 校验。

Frame.View 只提供同步只读视图。Tick、Object 返回的 Values 不应跨 owner mutation barrier
保存；`seedEvaluator` 在一次 ProduceMatch 中消费它们。Frame 不负责 Match Fact，后者由
MatchFactProvider 生成、由 evaluator clone 并传递，成功返回后由上层 `ticketStore.Commit`
与 `common.Match` 输出边界共同完成提交。Provider 契约由测试阶段校验。

## 4. Validator

NewValidator 检查每个 Spec 的名称、Type、Scope、MaxValues 和重复声明。ValidateLayer
检查当前层实际出现的字段是否已声明、类型/scope 是否匹配、集合是否超限，但不要求
该 scope 的每个声明都出现。ValidateCompleteMatch 在此基础上要求所有 match-scoped
Fact 都出现；CloneValidatedMatch 校验成功后再深拷贝。

Validator 只用于 Provider 契约测试和调试；生产路径信任同仓库内与规则配套的 Provider。
Ticket Attribute 的命名空间仍由 contract.AttributeValidator 负责，不能用 Fact validator
代替。

## 5. Provider 边界

Tick provider 由上层 `matchsystem` facade 定义，以避免 fact 子包依赖节点运行时：

- `matchsystem.FactProvider(context.Context, matchsystem.TickFactInput)` 创建 Tick Values；
- `ObjectProvider(ticket, now, tick)` 创建 Object Values；
- MatchFactProvider.Initialize/OnJoin 返回完整 Match Values。

上层 seedEvaluator 会将 Match Provider error/cancel 包装为 Evaluation error，并在
clone 后持有本次评估快照、构造完整 Match；ticketStore 只消费该 Match 的 Ticket 和
Prefilter membership。Provider panic 不被捕获，直接传播。Provider 的 Fact 契约由测试
显式检查；fact 包本身不执行重试、评分、索引查询或事务提交。

实现入口：[fact.go](../../../internal/matchsystem/fact/fact.go)、
[frame.go](../../../internal/matchsystem/fact/frame.go)、
[provider.go](../../../internal/matchsystem/fact/provider.go)。
