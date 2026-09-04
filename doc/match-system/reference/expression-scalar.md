# `expression-scalar/v3`

这是 Prefilter 和 Evaluation 共用的唯一标量表达式语言。输入是严格 JSON envelope，
输出是不可变、类型明确的 `ScalarProgram`；expression 节点、编译器内部节点和执行句柄不对外暴露。

## Envelope 与结果类型

```json
{
  "schemaVersion": "expression-scalar/v3",
  "resultType": "bool",
  "expr": {"op": "int64_gte",
    "left": {"op": "int64_ref", "source": "match_facts", "name": "count"},
    "right": {"op": "int64_literal", "value": 2}}
}
```

顶层必须含 `schemaVersion`、`resultType`、`expr`。`resultType` 只能是：

| 结果 | 说明 | 求值入口 |
| --- | --- | --- |
| `bool` | 布尔判断结果 | `EvaluateBool` |
| `int64` | 单值有符号整数 | `EvaluateInt64` |
| `strings` | 字符串集合；结果排序并去重 | `EvaluateStrings` |
| `uint64s` | 无符号整数集合；结果排序并去重 | `EvaluateUint64s` |

Evaluation 的 `canJoin` 和 `canComplete` root 强制为 `bool`。这不表示所有表达式
叶子都是 Bool：`int64_literal`、`int64_ref`、集合 literal/ref 是合法的 typed
operand，通常由比较或集合谓词消费；最终的比较/集合判断节点才产生 Bool。

## 合法节点

编译器只接受下列闭合节点集合，所有子节点类型都必须匹配：

| 类型 | 节点 |
| --- | --- |
| Bool | `bool_literal(value)`、`bool_and(children)`、`bool_or(children)`、`bool_not(value)` |
| Int64 值 | `int64_literal(value)`、`int64_ref(source,name)`、`int64_add(left,right)`、`int64_sub(left,right)`、`int64_min(left,right)`、`int64_max(left,right)`、`int64_step(input,steps)`、`int64_clamp(value,min,max)` |
| Int64 判断 | `int64_eq`、`int64_neq`、`int64_lt`、`int64_lte`、`int64_gt`、`int64_gte`（均为两个 Int64 子节点） |
| Strings 值 | `strings_literal(values)`、`strings_ref(source,name)`、`strings_union(items)` |
| Strings 判断 | `strings_eq`、`strings_neq`、`strings_contains_any`、`strings_contains_all`、`strings_intersects`（`values` 与 `other`）；`strings_is_empty(values)`；`strings_contains(values,needle)` |
| Uint64s 值 | `uint64s_literal(values)`、`uint64s_ref(source,name)`、`uint64s_union(items)` |
| Uint64s 判断 | `uint64s_eq`、`uint64s_neq`、`uint64s_contains_any`、`uint64s_contains_all`、`uint64s_intersects`（`values` 与 `other`）；`uint64s_is_empty(values)`；`uint64s_contains(values,needle)` |

`bool_and`、`bool_or`、两个集合 `union` 的数组不能为空；所有数组和 `int64_step`
的数量受编译限制，step 的 `at` 必须严格递增。整数加减使用饱和语义；集合判断按
排序去重后的集合执行。

节点出现未知字段、未知 op、错误字段类型、空必需数组或子节点结果类型不匹配时，
编译失败，不会自动转换成另一个类型。

## Source 与 profile

引用节点必须显式填写 `source` 和 `name`。合法 source 为：

| source | 可引用的 Contract 命名空间 |
| --- | --- |
| `seed_attributes` | seed Ticket Attribute |
| `seed_facts` | seed 的 object Fact |
| `tick_facts` | 本次 Tick 的 Fact |
| `candidate_attributes` | candidate Ticket Attribute |
| `candidate_facts` | candidate 的 object Fact |
| `match_facts` | 当前完整 Match Fact |

compiler 的 `CompileProfile` 同时接收 `AllowedRoots`、`AllowedSources`、Attribute/Fact
声明、可选 `FactAllowed(source,name)` 策略以及 `Limits`/`JSONLimits`。因此它会在
编译期检查：

- source 是否被当前阶段授权；
- name 是否在同一份 Contract 中声明；
- Attribute/Fact 的类型是否与节点结果一致；
- Fact 的 scope 是否允许从该 source 读取；
- root、深度、子节点、literal、step、节点和 instruction 预算是否有效。

缺失值、非法 source、越权 source 和 scope/type 不匹配都不会 fallback 到其他层。
运行时 lookup 只返回 primitive 值，不暴露 Ticket、Match 成员、Fact map 的写权限或
Provider。

### Prefilter profile

Prefilter 的标量 operand 只允许 `seed_attributes`、`seed_facts`、`tick_facts`。它的
root 不是 scalar Bool，而是由 [Prefilter](prefilter.md) 私有 Bitmap expression 使用
这些 scalar operand 产生候选集合。

### Evaluation profile

Evaluation 的 `CanJoin` 允许六种 source，输入包括 seed、Tick、candidate 和加入前
的完整 Match Fact；`CanComplete` 只允许 `tick_facts` 与 `match_facts`。两个 root 都
必须是 Bool，其他结果类型只能作为内部 typed operand。

## 编译与运行时 API

```go
program, err := expression.CompileScalarJSON(data, expression.ScalarCompileOptions{
    Profile: expression.ProfileForRoots(expression.ResultBool),
})
if err != nil { return err }
ok, err := program.EvaluateBool(lookup)
```

常用 metadata 是 `ResultType()`、`Dependencies()`、`Cost()`、`Canonical()`；它们只读。
表达式错误统一带 `Phase`、`Path`、`Code`、`Err`，调用方应按结构化字段处理。

实现入口：[schema.go](../../../internal/matchsystem/expression/schema.go)、
[json.go](../../../internal/matchsystem/expression/json.go)、
[compiler.go](../../../internal/matchsystem/expression/compiler.go)、
[program.go](../../../internal/matchsystem/expression/program.go)。
