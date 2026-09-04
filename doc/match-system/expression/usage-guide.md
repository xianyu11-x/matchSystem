# internal/matchsystem/expression 使用指南

## 1. 编译一个 Bool root

~~~go
profile := expression.ProfileForRoots(expression.ResultBool)
profile.AllowedSources = expression.CapabilitySeedAttributes | expression.CapabilityTickFacts
profile.Attributes = []expression.AttributeSpec{
    {Name: "mode", Type: fact.TypeStrings, MaxValues: 4},
}
profile.Facts = []fact.Spec{
    {Name: "capacity", Type: fact.TypeInt64, Scope: fact.ScopeTick},
}

program, err := expression.CompileScalarJSON(data, expression.ScalarCompileOptions{
    Profile: profile,
})
if err != nil {
    var structured *expression.Error
    if errors.As(err, &structured) {
        log.Printf("%s %s", structured.Code, structured.Path)
    }
    return err
}
~~~

data 必须是完整的 expression-scalar/v3 envelope，例如：

~~~json
{
  "schemaVersion":"expression-scalar/v3",
  "resultType":"bool",
  "expr":{
    "op":"int64_gte",
    "left":{"op":"int64_ref","source":"tick_facts","name":"capacity"},
    "right":{"op":"int64_literal","value":1}
  }
}
~~~

Profile 的 AllowedRoots、AllowedSources、Attributes 和 Facts 要按实际阶段闭合；仅仅把
字段写入 Contract 不会自动授权 source。

## 2. 提供只读 Lookup 并求值

~~~go
type lookup struct {
    ticket *common.Ticket
    tick   fact.Values
}

func (l lookup) Strings(source expression.Source, name string) ([]string, bool) {
    if source == expression.SourceSeedAttributes && l.ticket != nil {
        values, ok := l.ticket.StringLists[name]
        return values, ok
    }
    return nil, false
}
func (l lookup) Uint64s(expression.Source, string) ([]uint64, bool) { return nil, false }
func (l lookup) Int64(source expression.Source, name string) (int64, bool) {
    if source == expression.SourceTickFacts {
        value, ok := l.tick.Int64Values[name]
        return value, ok
    }
    return 0, false
}

ok, err := program.EvaluateBool(lookup{ticket: seed, tick: tickFacts})
~~~

Lookup 返回 false 表示缺失；表达式若实际读取该键会返回 MISSING_VALUE。不要把 Ticket、
Fact map 或可写的业务对象暴露给 Lookup，也不要在 Evaluate 后修改传入 slice 期待影响
程序。

## 3. 集合和动态数值

strings_literal、strings_ref、strings_union 与 uint64 对应节点产生排序去重集合；
strings_contains_any/all、intersects、eq/neq 等节点消费集合。int64_add/sub 为饱和
运算，int64_step 的阈值必须严格递增，int64_clamp 在运行时拒绝反转边界。

集合 root 可以用 CollectionUpperBound 查询保守上限；Prefilter 编译器用该上限验证
索引 MaxQueryValues。动态表达式可作为 Prefilter 的 values/min/max 或 Evaluation 的
内部 operand，但不能直接成为 Bitmap 或 Match Fact 写入。

## 4. 读取 metadata 和 identity

~~~go
_ = program.ResultType()
_ = program.Canonical()
deps := program.Dependencies()
cost := program.Cost()
upper, known := program.CollectionUpperBound()
_ = deps
_ = cost
_ = upper
_ = known
~~~

Canonical 适合计划缓存身份；它不包含运行时 Ticket、Fact 值或 Lookup 实现。Dependencies
的访问器返回排序副本，调用方可安全读取。

## 5. 限制和错误处理

默认 JSON 限制可通过 CompileProfile.JSONLimits 或 Limits 进一步收紧；负数限制无效。
对于所有错误都用 errors.As 读取 expression.Error 的 Phase/Path/Code/Err。错误不能被
转换成默认 false、空集合或零值；由上层决定是否放弃当前匹配尝试。

Prefilter、Evaluation 应分别使用它们提供的 profile/adapter，不要在业务层创建第二个
语法解析器。实现见 [expression/json.go](../../../internal/matchsystem/expression/json.go)
和 [expression/program.go](../../../internal/matchsystem/expression/program.go)。
