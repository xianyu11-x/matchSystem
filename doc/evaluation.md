# `evaluation/v3` section

Evaluation 是 RuleJSON 中 `evaluation` section 的两个 Bool 谓词编译和求值层：`canJoin`
判断一个 candidate 是否可以加入当前临时 Match，`canComplete` 判断当前 Match Fact 是否
已经满足完成条件。完整生产配置由 `matchsystem.CompileRuleJSON` 接收；Evaluation section
不会作为独立规则文件提交，也不负责候选评分、更新 Match Fact 或遍历 Match 成员。

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

该 section 只接受 `schemaVersion`、`canJoin`、`canComplete`，三个字段都必须存在。两个
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
RuleJSON 的 `scoring` 配置。Evaluation 不能读取 Match 内已有成员的 Ticket、属性或
Object Fact。

### CanComplete

`CanCompleteInput` 只有 `TickFacts` 和当前完整 `MatchFacts`。`CanComplete` 不能读取
seed、candidate 或 Match 成员；它的 Bool 结果只回答当前聚合快照是否完成。

两个阶段的 source capability、Fact 名称、类型和 scope 检查在编译期完成。Tick、Object
和 Match Fact 由同一代码库内与规则配套的可信 Provider 提供，运行时只 clone 输入并
执行表达式，不重复验证 Provider 快照的 schema、类型、scope、完整性或 `MaxValues`。
表达式实际读取不存在或放错类型 map 的值时，Lookup 仍返回 `MISSING_VALUE`，不会从其它
Fact 层猜测值。Provider 契约由对应测试显式使用 `fact.Validator` 检查。

## 内置评分与 Match Fact

评分配置位于同一 RuleJSON 的 `scoring` section，而不是 Evaluation section。当前支持
`constant`（固定值）、`created_at`（按 Ticket 创建时间和方向，可选权重）和
`int64_field`（按 Contract 声明的 int64 Attribute，可选权重和缺失值分数）。评分只
接收 seed、candidate、`Now`、Tick/seed/candidate Fact 的只读快照，不接收 Match 或
Match Fact；它只负责候选排序，不能替代 `canJoin`。

Match Fact 的产生和更新由 [Match Fact Provider](match-fact-provider.md) 独占：
`Initialize` 和 `OnJoin` 返回完整快照。Evaluation 只读取快照，绝不返回 patch 或
修改聚合状态。

## 编译与求值边界

生产调用方只需把完整 RuleJSON 交给 `matchsystem.CompileRuleJSON`，再由
`NewLogicalNode` 使用其中已编译的 Evaluation。Evaluation 包内的 `CompileJSON` 接收
同一份 `contract.Contract`，仅作为聚合编译器的内部阶段；它可用
`expression.JSONLimits` 进一步收紧限制，但不能放宽 Contract 边界。编译后的 `Predicates`
和内部 `ScalarProgram` 只读，可在多次求值中复用，但输入快照由调用方和运行时边界负责
拥有。

实现见 [evaluation/predicates.go](../internal/matchsystem/evaluation/predicates.go)、
[RuleJSON 编译](../internal/matchsystem/rule_config.go)；固定调用顺序见
[runtime-flow](architecture/runtime-flow.md)。
