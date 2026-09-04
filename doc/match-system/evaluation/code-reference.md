# internal/matchsystem/evaluation 代码索引

## 1. 公共版本、输入和结果

来自 predicates.go：

| 符号 | 说明 |
| --- | --- |
| SchemaVersion | evaluation/v3，RuleJSON.evaluation section 的内部版本 |
| CanJoinInput | Now、seed/candidate Ticket、seed/candidate Object Facts、Tick Facts、加入前 MatchFactsBefore |
| CanCompleteInput | TickFacts 和当前完整 MatchFacts；不含 seed/candidate/成员 |
| Predicates | 保存两个私有 ScalarProgram 和 Attribute validator 的不可变结果；Fact 声明仅用于编译 |
| CompileJSON(data, schema, supplied...) | 复制 Contract、严格解析并编译 RuleJSON.evaluation 中的两个 Bool root |
| (Predicates).CanJoin(input) | 校验上下文并执行 canJoin |
| (Predicates).CanComplete(input) | 读取可信 Tick/Match Fact 并执行 canComplete |

生产调用方通过 `matchsystem.CompileRuleJSON` 统一编译完整 RuleJSON；本包的
`CompileJSON` 只作为聚合编译器内部的 Evaluation 阶段实现。

## 2. 编译辅助

| 私有函数/区域 | 责任 |
| --- | --- |
| evaluationProfile | 为 CanJoin/CanComplete 设置不同 source capability、Attribute/Fact 声明和 Fact scope policy |
| evaluationLimits | 合并 Contract 默认限制与可选 JSONLimits |
| limit tightening helper | 只取更严格的非零 caller limit |
| validateJSON / checkFields | 结构和顶层字段校验 |
| checkAggregateCost | 检查两个谓词合计的 nodes/instructions/literals/steps |
| adaptCompileError / adaptExpressionError | 保留 Phase、Path、Code 的跨包错误适配 |

CanJoin capability 是 seed_attributes、seed_facts、tick_facts、candidate_attributes、
candidate_facts、match_facts；CanComplete 只有 tick_facts、match_facts。

## 3. 运行时 Lookup

scalarLookup 实现 expression.Lookup。它按 source 从已复制的 Ticket/Facts 读取
strings、uint64s 或 int64；未授权 source 记录 SOURCE_NOT_ALLOWED，缺失键记录
MISSING_VALUE。它不暴露 Ticket、Fact map 的可写引用或 Match 成员。

CanJoin 评估前仍调用 AttributeValidator.ValidateTicket；Tick/Object/Match Fact 快照
来自可信 Provider，生产路径只 clone 后读取，不调用 Fact Validator。表达式实际读取
缺失或放错类型 map 的值时仍返回 `MISSING_VALUE`；Provider 契约应在测试阶段显式验证。

## 4. 与根包运行时的边界

本包只提供不可变的 `Predicates`。`seedEvaluator` 负责把 Fact Frame、Prefilter
候选、内置评分和 MatchFactProvider 结果映射为这些输入，并处理 provider 的普通
error/cancel；`ticketStore` 不属于本包，负责成功 Match 的 Ticket 原子消费。Provider
panic 不由运行时转换，直接传播。

## 5. 错误类型

errors.go 的 Error 结构为：

~~~go
type Error struct {
    Phase string // json | compile | evaluate
    Path  string
    Code  string
    Err   error
}
~~~

常见 Code 包括 UNKNOWN_SCHEMA_VERSION、UNKNOWN_FIELD、ROOT_NOT_ALLOWED、
SOURCE_NOT_ALLOWED、MISSING_VALUE、ATTRIBUTE_TYPE_MISMATCH、EXPRESSION；Provider 的
普通 error/cancel 由根包 `seedEvaluator`/`provider_runtime` 包装。Fact Validator 的
契约错误只应出现在测试/调试调用中。使用 errors.As 读取结构化错误；Provider panic
不转换为错误。

实现链接：[predicates.go](../../../internal/matchsystem/evaluation/predicates.go)、
[errors.go](../../../internal/matchsystem/evaluation/errors.go)、
[RuleJSON 编译](../../../internal/matchsystem/rule_config.go)。
