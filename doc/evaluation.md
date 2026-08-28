# `evaluation/v3`

Evaluation 是两个 Bool 谓词的编译和求值层：`canJoin` 判断一个 candidate 是否可以
加入当前临时 Match，`canComplete` 判断当前 Match Fact 是否已经满足完成条件。
它不负责候选评分、不更新 Match Fact、不遍历 Match 成员。

## JSON 形状

```json
{
  "schemaVersion": "evaluation/v3",
  "canJoin": {
    "schemaVersion": "expression-scalar/v3",
    "resultType": "bool",
    "expr": {"op": "bool_literal", "value": true}
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
```

顶层只接受 `schemaVersion`、`canJoin`、`canComplete`，三个字段都必须存在。两个
字段都必须是完整的 `expression-scalar/v3` envelope，且 `resultType` 必须为 `bool`。
`int64`、`strings`、`uint64s` root 只能作为 Bool 谓词内部的 typed operand，不能
直接成为 Evaluation root。

## 可用数据

### CanJoin

`CanJoinInput` 的表达式数据权限是：

| source | 可读内容 |
| --- | --- |
| `seed_attributes` | seed Ticket 的 Attribute |
| `seed_facts` | seed 的 object Fact |
| `tick_facts` | 本次 Tick 的 Fact |
| `candidate_attributes` | 当前 candidate Ticket 的 Attribute |
| `candidate_facts` | 当前 candidate 的 object Fact |
| `match_facts` | candidate 加入前的完整 Match Fact |

这是加入判断所需的全部 primitive 数据。`CanJoinInput.Now` 会由运行时传入，但当前
scalar source 没有 `now` 引用；需要使用时间或其它非标量业务信息时应放入 Fact 或
CandidateScorer。Evaluation 不能读取 Match 内已有成员的 Ticket、属性或 Object Fact。

### CanComplete

`CanCompleteInput` 只有 `TickFacts` 和当前完整 `MatchFacts`。`CanComplete` 不能读取
seed、candidate 或 Match 成员；它的 Bool 结果只回答当前聚合快照是否完成。

两个阶段的 source capability、Fact 名称、类型和 scope 检查在编译期完成。Tick、Object
和 Match Fact 由同一代码库内与规则配套的可信 Provider 提供，运行时只 clone 输入并
执行表达式，不重复验证 Provider 快照的 schema、类型、scope、完整性或 `MaxValues`。
表达式实际读取不存在或放错类型 map 的值时，Lookup 仍返回 `MISSING_VALUE`，不会从其它
Fact 层猜测值。Provider 契约由对应测试显式使用 `fact.Validator` 检查。

## CandidateScorer 与 Match Fact

评分函数不是 Evaluation JSON 的一部分。每个 `LogicalNodeSpec` 必须直接绑定一个
非 nil `CandidateScorer` Go callback；它只接收 seed、candidate、`Now`、Tick/seed/
candidate Fact 的 clone，不接收 Match 或 Match Fact。它只负责为候选排序，不能替代
`CanJoin`。

Match Fact 的产生和更新由 [Match Fact Provider](match-fact-provider.md) 独占：
`Initialize` 和 `OnJoin` 返回完整快照。Evaluation 只读取快照，绝不返回 patch 或
修改聚合状态。

## 编译与求值 API

```go
predicates, err := evaluation.CompileJSON(evaluationJSON, schema)
if err != nil { return err }

join, err := predicates.CanJoin(evaluation.CanJoinInput{
    SeedAttributes: seed,
    SeedFacts: seedFacts,
    TickFacts: tickFacts,
    Candidate: candidate,
    CandidateFacts: candidateFacts,
    MatchFactsBefore: matchFacts,
})
complete, err := predicates.CanComplete(evaluation.CanCompleteInput{
    TickFacts: tickFacts,
    MatchFacts: matchFacts,
})
```

`CompileJSON` 接收同一份 `contract.Contract`，可选一个 `expression.JSONLimits` 只
用于进一步收紧限制；不能放宽 Contract 边界。编译后的 `Predicates` 和内部
`ScalarProgram` 只读，可在多次求值中复用，但输入快照由调用方和运行时边界负责拥有。

实现见 [evaluation/predicates.go](../internal/matchsystem/evaluation/predicates.go)、
[公共 facade](../internal/matchsystem/evaluation_api.go)；固定调用顺序见
[runtime-flow](architecture/runtime-flow.md)。
