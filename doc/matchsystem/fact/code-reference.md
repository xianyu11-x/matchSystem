# internal/matchsystem/fact 代码索引

## 1. fact.go

| 符号 | 语义 |
| --- | --- |
| Type / TypeStrings / TypeInt64 / TypeUint64s | Fact 值类型 |
| Scope / ScopeTick / ScopeObject / ScopeMatch | 生命周期层 |
| Spec | Name、Type、MaxValues、Scope 声明 |
| Values | 三个 typed map 的 Fact layer |
| Clone(values) | 深拷贝 map 和 slice |

## 2. frame.go / object_slot.go：一次尝试访问与 per-Ticket slot

| API | 语义 |
| --- | --- |
| ObjectProvider | func(*common.Ticket, int64, Values, Writer) error，同步写入 Object layer |
| Error | Path、Code、Err；Fact contract/scope 错误 |
| ObjectLayout | 共享的 Object Fact schema；由 LogicalNode 编译一次 |
| ObjectSlot.Init(layout) | 声明 Object Fact 的规则在 Ticket Add 时建立 slot 及 list buffer 槽位；空 layout 不建 slot |
| NewFrame(tick, generation, observe) | 复制 Tick，建立 generation Frame；生产路径不重复执行 Fact contract 校验 |
| Frame.Tick() | 返回 Frame-owned Tick Values |
| Frame.Object(slot, ticket, now, provider) | 每个 Ticket/generation 至多执行一次 provider，成功/失败都缓存；返回 ObjectAccess |
| Writer.SetStrings/AppendString | 按 schema 写入/追加 strings，复制输入并检查 MaxValues |
| Writer.SetUint64s/AppendUint64 | 按 schema 写入/追加 uint64s，复制输入并检查 MaxValues |
| Writer.SetInt64 / CopyFrom | 按 schema 写入 int64 或适配已有 Values |
| ObjectSlot.ValuesFor / Invalidate | 读取当前 generation 的 ready 快照；Remove/Commit 时失效 |
| NameSet / Field / Inspect | Validator 使用的字段类型、数量和名称检查 |
| ValidateTypes / ValidateScopes / ValidateLayer | 测试/调试用的类型、跨层重名和 Contract layer 检查 |
| NewValidator(specs) | 创建不可变按名称查找 validator，供 Provider 契约测试/调试复用 |
| Validator.ValidateLayer | 测试/调试时校验指定 scope 中实际出现的字段 |
| Validator.ValidateCompleteMatch | 测试/调试时额外要求所有 match Fact 键都出现 |
| Validator.CloneValidatedMatch | 测试/调试时校验后返回独立拥有的完整 Match layer |
| ValidateCompleteMatch / CloneValidatedMatch | 测试/调试用便捷函数，临时建立 validator |
| SameSpecs | 按 Name/Spec 比较两组声明 |

Contract 的 Fact 声明在配置/编译阶段由 `contract.Contract.Validate` 校验。生产运行时的
Tick、Match Provider 都是同一代码库内与规则配套的可信实现；Object Provider 必须通过
Writer 写入，Writer 在热路径只做名称、类型和 `MaxValues` 边界检查。Provider 契约应在
对应测试中显式使用 `NewValidator` 验证；生产路径不调用这些 Validator API。

## 3. provider.go

| 符号 | 语义 |
| --- | --- |
| MatchFactProvider.Initialize | 用 Now、seed Ticket、seed Object Fact、Tick Fact 生成初始完整快照 |
| MatchFactProvider.OnJoin | 另加 candidate Ticket/Object Fact 与加入前完整快照，生成下一个完整快照 |
| InitializeInput | Provider 的初始只读输入 |
| JoinInput | Provider 的加入阶段只读输入 |

Provider 接口只定义数据边界，不规定实现如何聚合；seedEvaluator 负责复制输入、调用
顺序以及 cancel/error 处理，ticketStore 负责成功 Match 的原子 Ticket 消费；Provider
panic 直接传播。

实现链接：[fact.go](../../../internal/matchsystem/fact/fact.go)、
[frame.go](../../../internal/matchsystem/fact/frame.go)、
[object_slot.go](../../../internal/matchsystem/fact/object_slot.go)、
[provider.go](../../../internal/matchsystem/fact/provider.go)。
