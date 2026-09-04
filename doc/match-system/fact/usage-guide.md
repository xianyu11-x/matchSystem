# internal/matchsystem/fact 使用指南

## 1. 定义 Fact Contract 与测试校验器

~~~go
specs := []fact.Spec{
    {Name: "capacity", Type: fact.TypeInt64, Scope: fact.ScopeTick},
    {Name: "tier", Type: fact.TypeInt64, Scope: fact.ScopeObject},
    {Name: "labels", Type: fact.TypeStrings, MaxValues: 4, Scope: fact.ScopeMatch},
}
// Provider 契约测试中复用同一份声明做显式检查。
validator, err := fact.NewValidator(specs)
if err != nil { return err }
~~~

int64 的 MaxValues 必须为零；strings/uint64s 必须有正的 MaxValues。名称在一层中必须
只出现于对应类型 map。

## 2. 创建 Frame 并读取 Tick/Object

~~~go
tick := fact.Values{Int64Values: map[string]int64{"capacity": 10}}
layout, err := fact.NewObjectLayout(specs)
if err != nil { return err }
var slot fact.ObjectSlot
slot.Init(layout)

frame := fact.NewFrame(tick, 1, false)
object, access, err := frame.Object(&slot, ticket, now,
    func(t *common.Ticket, now int64, tick fact.Values, out fact.Writer) error {
        return out.SetInt64("tier", t.Int64Values["tier"])
    })
if err != nil { return err }
_ = object
_ = access
~~~

NewFrame 会复制 Tick 并建立一次 generation 的访问边界。Object provider 收到借用的只读
Ticket/Tick，只能通过 Writer 发布结果；Writer 把值复制进 Ticket 自己的可复用 ObjectSlot，
并检查字段名称、类型和 `MaxValues`。同一个 slot 在同一 generation 的后续请求不会再次调用
provider；新 generation 会刷新。Frame 不执行完整 Contract/scope 校验，这些约束由同仓库
内与规则配套的 Provider 保证，并在 Provider 契约测试中显式检查。没有 Object Fact 时
LogicalNode 不创建 slot，Object layer 为空；表达式读取缺失字段仍会失败。

## 3. 在测试阶段校验跨层和完整 Match Fact

~~~go
tickNames, err := validator.ValidateLayer("tick", tick, fact.ScopeTick)
if err != nil { return err }
objectNames, err := validator.ValidateLayer("object", object, fact.ScopeObject)
if err != nil { return err }
if err := fact.ValidateScopes("object", "Tick", "Object", tickNames, objectNames); err != nil {
    return err
}

match := fact.Values{
    StringLists: map[string][]string{"labels": {}},
}
owned, err := validator.CloneValidatedMatch("match", match)
if err != nil { return err }
_ = owned
~~~

这些 Validator 调用属于 Provider 契约测试或调试，不属于生产热路径。生产代码只接收
可信 Provider 的快照并 clone；完整 Match 快照仍应由 Provider 负责生成。`CloneValidatedMatch`
成功后返回独立副本，适合在测试中同时检查契约和 ownership。

## 4. MatchFactProvider

~~~go
type provider struct{}

func (provider) Initialize(context.Context, fact.InitializeInput) (fact.Values, error) {
    return fact.Values{Int64Values: map[string]int64{"count": 1}}, nil
}
func (provider) OnJoin(_ context.Context, in fact.JoinInput) (fact.Values, error) {
    next := in.MatchFactsBefore.Int64Values["count"] + 1
    return fact.Values{Int64Values: map[string]int64{"count": next}}, nil
}
~~~

两个方法都必须返回完整快照，不是 patch。输入是 call-scoped 副本，Provider 不应保存
或修改它们。Provider 自己负责返回值符合 Contract；可以在对应测试中用上面的 Validator
显式检查。Provider 的 error/cancel 由 seedEvaluator 包装；Provider panic 直接传播，
fact 包只负责模型、clone 和测试/调试校验工具。

## 5. 错误与生命周期

Provider 契约测试使用 `errors.As(err, *fact.Error)` 读取 Path、Code、Err。常见 Code 有
`FACT_TYPE_COLLISION`、`UNDECLARED_FACT`、`FACT_TYPE_MISMATCH`、`FACT_SCOPE_MISMATCH`、
`FACT_VALUE_LIMIT`、`FACT_SCOPE_COLLISION`、`MATCH_FACT_INCOMPLETE` 和
`INVALID_FACT_LIMIT`。生产表达式读取不存在或类型不匹配的 map 时，仍通过对应 Lookup
返回 `MISSING_VALUE`，不会从其它 Fact 层猜测。

Frame、View、Values 的返回值只在当前同步调用中借用；跨 mutation barrier 或异步保存前
应使用 Clone。Fact 不负责 Ticket Attribute 校验、索引、评分和 Match 提交；这些职责分别
由 contract、ticketStore 和 seedEvaluator/LogicalNode 完成。
