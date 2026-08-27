# internal/matchsystem/evaluation 使用指南

## 1. 准备同一份 Contract

Evaluation 不拥有自己的字段声明。先解析或构造并 Validate Contract，再将同一实例传给
CompileJSON：

~~~go
schema, err := contract.Parse(contractJSON, contract.DefaultLimits())
if err != nil {
    return err
}
predicates, err := evaluation.CompileJSON(evaluationJSON, schema)
if err != nil {
    return err
}
~~~

可选的 expression.JSONLimits 只能收紧边界；超过一个 limits 参数、负数限制或使用
旧版本都会在 compile/json 阶段拒绝。

## 2. Evaluation JSON

~~~json
{
  "schemaVersion": "evaluation/v3",
  "canJoin": {
    "schemaVersion": "expression-scalar/v3",
    "resultType": "bool",
    "expr": {
      "op": "int64_gte",
      "left": {"op": "int64_ref", "source": "candidate_facts", "name": "tier"},
      "right": {"op": "int64_literal", "value": 2}
    }
  },
  "canComplete": {
    "schemaVersion": "expression-scalar/v3",
    "resultType": "bool",
    "expr": {
      "op": "int64_gte",
      "left": {"op": "int64_ref", "source": "match_facts", "name": "count"},
      "right": {"op": "int64_literal", "value": 2}
    }
  }
}
~~~

顶层不能加入 scorer、provider 或自定义字段；这两个依赖由 LogicalNodeSpec 的 Go
字段绑定。两个 root 的 resultType 都必须为 bool；int64/strings/uint64s 只能作为
Bool 谓词内部 operand。

## 3. 调用 CanJoin

~~~go
joined, err := predicates.CanJoin(evaluation.CanJoinInput{
    Now:              now,
    SeedAttributes:   seed,
    SeedFacts:        seedFacts,
    TickFacts:        tickFacts,
    Candidate:        candidate,
    CandidateFacts:   candidateFacts,
    MatchFactsBefore: matchFacts,
})
if err != nil {
    return err // 不要把错误当成 false 继续提交
}
if !joined {
    continue
}
~~~

CanJoin 允许读取六类 source，但每个 Fact 仍需满足 Contract 的 type/scope。输入的
Ticket/Facts 会被复制；缺少表达式实际引用的键返回 MISSING_VALUE，而不是默认零值。

## 4. 调用 CanComplete

~~~go
complete, err := predicates.CanComplete(evaluation.CanCompleteInput{
    TickFacts:  tickFacts,
    MatchFacts: matchFacts,
})
if err != nil {
    return err
}
if complete {
    commit()
}
~~~

CanComplete 只接受 Tick Facts 和完整 Match Facts；Match scope Fact 必须全部出现，空
集合应提供空 slice 作为键，不能省略。它不接受 seed、candidate 或成员列表。

## 5. 与 LogicalNode 的配合

推荐让 LogicalNode 负责固定顺序：Initialize 完整 Match Fact → 第一次 CanComplete →
Prefilter/Scorer → CanJoin → OnJoin 返回完整下一个快照 → 第二次 CanComplete。不要在
Evaluation callback 中修改 Match Fact，也不要用 scorer 代替 CanJoin。

## 6. 错误和排查

用 errors.As(err, *evaluation.Error) 读取 Phase、Path、Code、Err。看到
SOURCE_NOT_ALLOWED 时检查阶段 profile；看到 FACT_SCOPE_MISMATCH 或
ATTRIBUTE_TYPE_MISMATCH 时检查 Contract 与 source；看到 MISSING_VALUE 时补齐正确
层的键。Evaluation 不会在错误时 fallback 到其它层或静默放行。

实现参考：[predicates.go](../../../internal/matchsystem/evaluation/predicates.go)、
[根包调用示例](../../../cmd/app/main.go)。
