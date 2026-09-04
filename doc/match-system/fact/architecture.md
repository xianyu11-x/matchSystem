# internal/matchsystem/fact 架构说明

fact 是中立的 Fact 模型与生命周期包，负责 Values 容器、Fact declaration、三层
scope、深拷贝、测试/调试用 Contract validator、一次匹配尝试的 Frame，以及两个 Provider
接口。它不依赖 Prefilter/Evaluation，不保存 Ticket 池或 Match 状态；Object Facts 的
持久 slot 由上层 Ticket store 持有。

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
| Object | ObjectProvider，每个 Ticket/Produce generation 至多一次 | seed/candidate 的 Prefilter、Scorer 和 Evaluation |
| Match | MatchFactProvider.Initialize/OnJoin | CanJoin/CanComplete |

Fact scope 是 Contract 和表达式编译期的结构约束，不是 fallback 选择。可信 Provider
必须让 Tick/Object 不产生冲突、让 Match snapshot 包含 Contract 声明的所有 match-scoped
键（空集合必须保留键）；这些 Provider 契约由测试中的 Validator 检查，生产运行时不重复
执行 ValidateScopes 或完整性校验。

## 3. Frame、slot 与所有权

NewFrame 深拷贝 Tick layer，并携带本次 ProduceMatch 的 generation。Object Facts 不再
由 Frame 建立 map 或复制 Provider 返回值，而是由声明 Object Fact 的规则在 Ticket 加入
store 时初始化的可选 `ObjectSlot` 持有；没有 Object Fact 的规则不创建 per-Ticket slot。
Frame.Object 首次请求某个 slot 时把借用的 Ticket/Tick 传给同步
ObjectProvider，并要求 Provider 通过 schema-bound `Writer` 写入 slot；同一 generation
后续请求直接返回 slot Values，成功和失败都不会重复调用 Provider。下一 generation
会清除 map 中的 presence、复用 list capacity，并允许第一次写入扩容。

Provider 不得保存或修改 Ticket、Tick、Writer 或 Writer.Values；评分器同样收到借用的
只读视图。Frame 不在生产路径重复执行 Fact schema/type/scope/max-values 校验，Writer
只在写入未知名称、错误类型或超过 MaxValues 时返回错误；失败不会发布半成品。
Frame 不负责 Match Fact，后者由 MatchFactProvider 生成、由 evaluator clone 并传递。
需要输出 detached Object/Match Fact 的调用方显式选择 DeepCopy 快照模式。

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
- `ObjectProvider(ticket, now, tick, writer)` 同步写入 Object Values；Writer 的
  `SetStrings`/`AppendString`、`SetUint64s`/`AppendUint64` 和 `SetInt64` 方法按 Contract
  检查名称、类型与 `MaxValues`；
- MatchFactProvider.Initialize/OnJoin 返回完整 Match Values。

上层 seedEvaluator 会将 Match Provider error/cancel 包装为 Evaluation error，并在
clone 后持有本次评估快照、构造完整 Match；ticketStore 只消费该 Match 的 Ticket 和
Prefilter membership。Provider panic 不被捕获，直接传播。Writer 的 Object schema 错误
在生产调用中直接失败并在下一 generation 重试；Fact 契约仍可由测试阶段用 Validator
显式检查。fact 包本身不执行评分、索引查询或事务提交。

实现入口：[fact.go](../../../internal/matchsystem/fact/fact.go)、
[frame.go](../../../internal/matchsystem/fact/frame.go)、
[object_slot.go](../../../internal/matchsystem/fact/object_slot.go)、
[provider.go](../../../internal/matchsystem/fact/provider.go)。
