# internal/matchsystem/fact 使用指南

## 1. 定义 Fact Contract

~~~go
specs := []fact.Spec{
    {Name: "capacity", Type: fact.TypeInt64, Scope: fact.ScopeTick},
    {Name: "tier", Type: fact.TypeInt64, Scope: fact.ScopeObject},
    {Name: "labels", Type: fact.TypeStrings, MaxValues: 4, Scope: fact.ScopeMatch},
}
validator, err := fact.NewValidator(specs)
if err != nil { return err }
~~~

int64 的 MaxValues 必须为零；strings/uint64s 必须有正的 MaxValues。名称在一层中必须
只出现于对应类型 map。

## 2. 创建 Frame 并读取 Tick/Object

~~~go
tick := fact.Values{Int64Values: map[string]int64{"capacity": 10}}
frame, err := fact.NewFrame(tick, specs)
if err != nil { return err }

object, err := frame.Object(ticket, now,
    func(t *common.Ticket, now int64, tick fact.Values) (fact.Values, error) {
        return fact.Values{Int64Values: map[string]int64{
            "tier": t.Int64Values["tier"],
        }}, nil
    })
if err != nil { return err }
_ = object
~~~

NewFrame 会复制 Tick 并按 ScopeTick 校验。Object provider 收到 Ticket/Tick 的副本；
Frame 会复制返回值、校验 ScopeObject 并按 TicketID 缓存。相同 TicketID 的后续 Object
请求不会再次调用 provider。没有 provider 时 Object layer 为空，但表达式实际读取
缺失字段仍会失败。

## 3. 校验跨层和完整 Match Fact

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

ValidateLayer 只要求“当前出现的字段”合法；缺少未被使用的字段不报错。完整 Match layer
则必须包含每个 scope: match 声明，空集合应写入空 slice，不能省略键。CloneValidatedMatch
成功后返回独立副本，可交给 owner 保存。

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
或修改它们。Provider 的 panic/error/cancel 由 LogicalNode 包装；fact 包只负责模型和校验。

## 5. 错误与生命周期

用 errors.As(err, *fact.Error) 读取 Path、Code、Err。常见 Code 有 FACT_TYPE_COLLISION、
UNDECLARED_FACT、FACT_TYPE_MISMATCH、FACT_SCOPE_MISMATCH、FACT_VALUE_LIMIT、
FACT_SCOPE_COLLISION、MATCH_FACT_INCOMPLETE 和 INVALID_FACT_LIMIT。

Frame、View、Values 的返回值只在当前同步调用中借用；跨 mutation barrier 或异步保存前
应使用 Clone。Fact 不负责 Ticket Attribute 校验、索引、评分和 Match 提交。
