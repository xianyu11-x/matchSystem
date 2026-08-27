# internal/matchsystem/evaluation 代码索引

## 1. 公共版本、输入和结果

来自 predicates.go：

| 符号 | 说明 |
| --- | --- |
| SchemaVersion | evaluation/v3，唯一 Evaluation envelope |
| CanJoinInput | Now、seed/candidate Ticket、seed/candidate Object Facts、Tick Facts、加入前 MatchFactsBefore |
| CanCompleteInput | TickFacts 和当前完整 MatchFacts；不含 seed/candidate/成员 |
| Predicates | 保存两个私有 ScalarProgram 和 Attribute/Fact validator 的不可变结果 |
| CompileJSON(data, schema, supplied...) | 复制 Contract、严格解析并编译两个 Bool root |
| (Predicates).CanJoin(input) | 校验上下文并执行 canJoin |
| (Predicates).CanComplete(input) | 校验 Tick/Match Fact 并执行 canComplete |

根包通过 evaluation_api.go 再导出 EvaluationPredicates、EvaluationCanJoinInput、
EvaluationCanCompleteInput、EvaluationError 和 CompileEvaluationJSON。

## 2. 编译辅助

| 私有函数/区域 | 责任 |
| --- | --- |
| evaluationProfile | 为 CanJoin/CanComplete 设置不同 source capability、Attribute/Fact 声明和 Fact scope policy |
| evaluationLimits | 合并 Contract 默认限制与可选 JSONLimits |
| tightenEvaluationJSONLimits | 只取更严格的非零 caller limit |
| validateJSON / checkFields | 结构和顶层字段校验 |
| checkAggregateCost | 检查两个谓词合计的 nodes/instructions/literals/steps |
| adaptCompileError / adaptExpressionError | 保留 Phase、Path、Code 的跨包错误适配 |

CanJoin capability 是 seed_attributes、seed_facts、tick_facts、candidate_attributes、
candidate_facts、match_facts；CanComplete 只有 tick_facts、match_facts。

## 3. 运行时 Lookup

scalarLookup 实现 expression.Lookup。它按 source 从已复制的 Ticket/Facts 读取
strings、uint64s 或 int64；未授权 source 记录 SOURCE_NOT_ALLOWED，缺失键记录
MISSING_VALUE。它不暴露 Ticket、Fact map 的可写引用或 Match 成员。

CanJoin 评估前调用 AttributeValidator.ValidateTicket、Validator.ValidateLayer（tick、
seed、candidate）和 ValidateCompleteMatch（match）。CanComplete 只执行 tick 和
complete match 的对应校验。所有校验均通过 evaluation.Error 返回。

## 4. 错误类型

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
SOURCE_NOT_ALLOWED、MISSING_VALUE、FACT_SCOPE_MISMATCH、ATTRIBUTE_TYPE_MISMATCH、
MATCH_FACT_INCOMPLETE、EXPRESSION、MISSING_VALIDATOR、PROVIDER 相关错误由上层
LogicalNode 负责包装。使用 errors.As 读取结构化错误。

实现链接：[predicates.go](../../../internal/matchsystem/evaluation/predicates.go)、
[errors.go](../../../internal/matchsystem/evaluation/errors.go)、
[根包 facade](../../../internal/matchsystem/evaluation_api.go)。
