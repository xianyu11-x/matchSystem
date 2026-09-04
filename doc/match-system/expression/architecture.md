# internal/matchsystem/expression 架构说明

expression 是 Prefilter 和 Evaluation 共用的、与业务领域无关的标量表达式内核。它接受
严格的 expression-scalar/v3 JSON，生成不可变且不透明的 ScalarProgram；节点数组、编译
IR 和执行句柄均为私有。包不拥有 Bitmap、索引、Ticket、Match 成员或 Fact 写入。

## 1. 结果类型与 source

四种 ResultType：

| 类型 | 语义 | 求值入口 |
| --- | --- | --- |
| bool | 布尔判断 | EvaluateBool |
| int64 | 单值有符号整数 | EvaluateInt64 |
| strings | 排序去重后的字符串集合 | EvaluateStrings |
| uint64s | 排序去重后的无符号整数集合 | EvaluateUint64s |

六种显式 Source：

| source | 内容 |
| --- | --- |
| seed_attributes | seed Ticket Attribute |
| seed_facts | seed 的 object Fact |
| tick_facts | 当前 Tick Fact |
| candidate_attributes | candidate Ticket Attribute |
| candidate_facts | candidate 的 object Fact |
| match_facts | 当前完整 Match Fact |

CompileProfile 通过 AllowedRoots、AllowedSources、Attribute/Facts 声明以及
FactAllowed 回调把闭合语言收紧到具体阶段；表达式不会因缺失值自动切换 source。

## 2. 编译管线

~~~text
expression-scalar/v3 bytes
  -> jsonstrict 结构检查（大小、深度、重复 key、尾随值）
  -> envelope/version/resultType 校验
  -> scalarBuilder 按闭合 op 集合构建私有节点
  -> lookup 的 Contract type/scope/capability 校验
  -> nodes/instructions/cost/collection bound
  -> immutable ScalarProgram（canonical + Dependencies + Cost）
~~~

CompileScalarJSON 会先复制 Profile 并计算有效 JSONLimits，再检查 root 是否被 profile
允许。所有数组、深度、literal、step、节点和 instruction 限制都在编译期计数。

## 3. 节点闭合集合

| 类别 | op |
| --- | --- |
| Bool | bool_literal、bool_and、bool_or、bool_not |
| Int64 值 | int64_literal、int64_ref、int64_add/sub/min/max、int64_step、int64_clamp |
| Int64 判断 | int64_eq、int64_neq、int64_lt、int64_lte、int64_gt、int64_gte |
| Strings 值 | strings_literal、strings_ref、strings_union |
| Strings 判断 | strings_eq、strings_neq、strings_is_empty、strings_contains、strings_contains_any/all、strings_intersects |
| Uint64s 值 | uint64s_literal、uint64s_ref、uint64s_union |
| Uint64s 判断 | uint64s_eq、uint64s_neq、uint64s_is_empty、uint64s_contains、uint64s_contains_any/all、uint64s_intersects |

Bool and/or 与集合 union 不能是空数组；int64_step 的 at 必须严格递增。Int64
加减采用饱和语义。集合求值和比较先排序去重，因此输入顺序和重复值不会改变集合
结果，但 Bool children 的顺序仍决定短路和错误返回路径。

## 4. 运行时边界

调用方实现只读 Lookup：

~~~go
type Lookup interface {
    Strings(source Source, name string) ([]string, bool)
    Uint64s(source Source, name string) ([]uint64, bool)
    Int64(source Source, name string) (int64, bool)
}
~~~

ScalarProgram 只从 Lookup 读取 primitive。nil Lookup、类型入口不匹配、Lookup 缺失值、
clamp 动态边界反转或非法节点返回 evaluate Error；不会猜测默认值。集合结果返回新切片，
不会把 Lookup 的可变 slice 作为程序状态保存。

## 5. 身份与资源

Canonical 与 JSON 空白、对象字段顺序无关，包含规范化节点和依赖集合；literal 集合按
集合语义排序去重，Bool/union 子节点顺序保留。Dependencies 的 Facts/Attributes
访问器按名称排序并返回副本。Cost 暴露 Nodes、Instructions、MaxDepth、MaxChildren、
LiteralValues、Steps、Values 和 StringBytes；CollectionUpperBound 可报告集合 root
的保守有限上界。

Prefilter/Evaluation 将 ScalarProgram 作为不透明 operand 保存，各自拥有运行时 adapter。
新增领域能力应通过 profile 或外部 domain 编译器接入，不应向 expression 放入 Bitmap 或
物理 index 逻辑。

实现入口：[schema.go](../../../internal/matchsystem/expression/schema.go)、
[json.go](../../../internal/matchsystem/expression/json.go)、
[compiler.go](../../../internal/matchsystem/expression/compiler.go)、
[program.go](../../../internal/matchsystem/expression/program.go)。
