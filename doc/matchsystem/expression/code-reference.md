# internal/matchsystem/expression 代码索引

## 1. schema.go：公共契约

| 符号 | 说明 |
| --- | --- |
| ResultType | ResultInvalid、ResultBool、ResultInt64、ResultStrings、ResultUint64s |
| Source | 六种显式输入 source |
| Capabilities | 按 bit mask 声明允许读取的 source；Allows 检查 |
| AttributeSpec | Attribute 名、fact.Type、MaxValues |
| Lookup | Strings、Uint64s、Int64 三个只读 primitive 入口 |
| Path | Child/Item 生成稳定错误路径 |
| Error | Phase、Path、Code、Err 结构化错误 |
| Dependencies | 按名称排序的 Fact/Attribute 依赖副本 |
| Limits | MaxDepth、MaxChildren、MaxLiteralValues、MaxSteps、MaxNodes、MaxInstructions |
| CompileProfile | AllowedRoots/Sources、声明、FactAllowed、Limits、JSONLimits |
| ProfileForRoots / StrictProfile | 创建 root 限制 profile |

## 2. json.go：严格 JSON 入口

| API | 语义 |
| --- | --- |
| ScalarSchemaVersion | expression-scalar/v3 |
| JSONLimits | 文档、数组、字符串和表达式资源限制 |
| DefaultJSONLimits() | 返回默认限制 |
| ScalarCompileOptions | 持有 CompileProfile |
| CompileScalarJSON(data, options) | 解析、校验并返回不透明 ScalarProgram |

顶层必须有 schemaVersion、resultType、expr；未知字段、null、错误类型、旧版本或尾随
值都会失败。resultType 为 bitmap 会明确返回 ROOT_NOT_ALLOWED；ScalarProgram 不提供
构造节点或读取内部图的方法。

## 3. compiler.go：私有编译和 metadata

scalarBuilder 将 op 翻译为私有 scalarKind/scalarNode，解析引用时校验 source capability、
Fact/Attribute 名称、Type 和 scope，并收集 Dependencies。ProgramCost.Fits/Within 可
用于检查 limits；CollectionUpperBound 对 literals、lookup bound 和 union 提供保守
上界。

canonicalProgram 生成稳定的表达式身份；依赖项包含类型、MaxValues 和 Fact scope。
短路节点的子节点顺序属于身份的一部分。

## 4. program.go：求值 API

| API | 语义 |
| --- | --- |
| ResultType() | 根结果类型；nil program 返回 ResultInvalid |
| EvaluateBool/Int64/Strings/Uint64s | 只能调用与根类型匹配的入口 |
| Canonical() | 稳定字符串身份；nil 返回空串 |
| Dependencies() | Fact/Attribute 依赖集合 |
| Cost() | ProgramCost 资源摘要 |
| CollectionUpperBound() | 集合根的有限上界和 known 标记 |

Bool and/or 在遇到结果时短路；字符串/uint64 集合每次输出排序去重；int64 add/sub
饱和到边界；step 按严格递增阈值查找；clamp 在运行时检查 min <= max。

## 5. 错误 Code

json 阶段常见 UNKNOWN_SCHEMA_VERSION、UNKNOWN_OP、UNKNOWN_FIELD、MISSING_FIELD、
NULL_FIELD、TYPE_MISMATCH、DEPTH_LIMIT、CHILD_LIMIT、NODE_LIMIT、VALUE_LIMIT、
STEP_LIMIT。compile 阶段常见 SOURCE_NOT_ALLOWED、MISSING_ATTRIBUTE、MISSING_FACT、
ATTRIBUTE_TYPE_MISMATCH、FACT_TYPE_MISMATCH、FACT_SCOPE_MISMATCH、ROOT_NOT_ALLOWED。
evaluate 阶段常见 NIL_PROGRAM、NIL_LOOKUP、MISSING_VALUE、INVALID_CLAMP_BOUNDS、
CYCLE。调用方应使用 errors.As(err, *expression.Error)。

实现链接：[schema.go](../../../internal/matchsystem/expression/schema.go)、
[json.go](../../../internal/matchsystem/expression/json.go)、
[compiler.go](../../../internal/matchsystem/expression/compiler.go)、
[program.go](../../../internal/matchsystem/expression/program.go)。
