# internal/matchsystem/evaluation 架构说明

evaluation 是匹配流程中的谓词层，只编译并求值两个 Bool 根：

- CanJoin：判断当前 candidate 是否可以加入当前临时组；
- CanComplete：判断当前完整 Match Fact 是否已满足完成条件。

它不负责候选评分、不更新 Match Fact、不遍历已有 Match 成员，也不拥有 provider 或
scorer。表达式语法、类型和运行预算由共享 expression 包实现。

## 1. 配置和编译边界

输入是唯一的 evaluation/v3 envelope：

~~~json
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
    "expr": {"op": "bool_literal", "value": false}
  }
}
~~~

CompileJSON 复制并 Validate Contract，计算一份不可变 JSON/表达式 limits，然后分别
使用两个 source capability profile 编译嵌套的 expression-scalar/v3。顶层只允许
schemaVersion、canJoin、canComplete；两个字段都必须存在且 root resultType 为 bool。
旧版本会在解释旧字段前以 UNKNOWN_SCHEMA_VERSION 拒绝。

## 2. 两个阶段的数据权限

~~~text
CanJoin:
  seed_attributes + seed_facts + tick_facts
  + candidate_attributes + candidate_facts + match_facts(before join)

CanComplete:
  tick_facts + match_facts(current complete snapshot)
~~~

CanJoinInput 的 MatchFactsBefore 是 candidate 加入前的完整快照；没有 Match 成员
Ticket/属性。CanCompleteInput 没有 seed、candidate 或成员列表。Fact scope 还必须匹配
Contract：tick 只能来自 tick_facts，object 只能来自 seed/candidate_facts，match
只能来自 match_facts。

表达式 Lookup 只返回 primitive 值和存在标记。输入 Ticket、Fact map 和 slice 会在进入
评估前复制；错误、缺失值和越权 source 都 fail closed，不会从另一个 source 猜值。

## 3. 固定运行顺序

LogicalNode 对每个 seed：

1. Object Fact Frame 生成 seed Facts；
2. MatchFactProvider.Initialize 返回并校验完整 Match Fact；
3. 调用 CanComplete；
4. 若未完成，Prefilter 生成候选，评分后逐个调用 CanJoin；
5. CanJoin 为 true 时调用 MatchFactProvider.OnJoin，校验并原子接受新快照；
6. 对新快照再次调用 CanComplete。

Evaluation 只在这些边界读取快照；Provider 产生快照，LogicalNode 才能接受状态转换。

## 4. 错误与限制

统一错误结构是 Phase、Path、Code、Err：

- json：严格 JSON、未知字段、版本、资源限制；
- compile：Contract、source capability、类型/scope、root 类型或表达式节点错误；
- evaluate：输入 Ticket/Fact 校验、缺失值、source 越权、表达式运行错误。

CanJoin 和 CanComplete 的表达式实现不完整、validator 缺失或输入 scope 错误均直接
返回错误，不把结果改成 false。CompileJSON 的可选 expression.JSONLimits 只能进一步
收紧 Contract 边界；两个谓词合计的节点、instruction、literal 和 step 预算也会检查。

实现入口：[predicates.go](../../../internal/matchsystem/evaluation/predicates.go)、
[errors.go](../../../internal/matchsystem/evaluation/errors.go)。
