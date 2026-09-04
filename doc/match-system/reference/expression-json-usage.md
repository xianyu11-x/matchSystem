# `expression-scalar/v3` JSON 使用文档

本文是当前 MatchSystem 表达式 JSON 的编写规范。文中的 expression（用户有时会
写成 `expreesion`）均指共享的标量表达式编译器
`internal/matchsystem/expression`，不是 Prefilter 的 Bitmap 节点树。

本文以当前源码为准：

- 标量表达式的 JSON 入口是 `expression.CompileScalarJSON`；
- 只接受 `expression-scalar/v3`，不兼容旧版本和 Bitmap root；
- Prefilter 的 `prefilter/v3` 和 Evaluation 的 `evaluation/v3` 是外层文档，
  它们在指定字段中嵌入完整的标量表达式文档；
- `doc/design-decisions/archive/` 中的 Arena、Builder、共享 Bitmap expression 等内容属于历史
  设计，不适用于当前实现。

## 1. 最小可用文档

一个标量表达式必须是一个完整的 envelope（封装对象），不能只提交 `expr` 节点：

```json
{
  "schemaVersion": "expression-scalar/v3",
  "resultType": "bool",
  "expr": {
    "op": "int64_gte",
    "left": {
      "op": "int64_ref",
      "source": "match_facts",
      "name": "count"
    },
    "right": {
      "op": "int64_literal",
      "value": 2
    }
  }
}
```

三个顶层字段都是必填字段，含义如下：

| 字段 | JSON 类型 | 含义和规则 |
| --- | --- | --- |
| `schemaVersion` | 字符串 | 必须精确等于 `expression-scalar/v3`。缺失、拼写错误、旧版本或其他版本都会拒绝。 |
| `resultType` | 字符串 | 声明整个 `expr` 的结果类型，只能是 `bool`、`int64`、`strings`、`uint64s`。声明必须与根节点实际产生的类型一致。 |
| `expr` | 对象 | 表达式根节点。节点对象必须包含 `op`，每种 `op` 只接受下文列出的字段。 |

`expr` 节点本身不写 `resultType`。结果类型由 `op` 固定推导，再与 envelope 的
`resultType` 比较；不要在每个子节点上重复添加类型字段。

四种结果类型的含义和对应的 Go 求值入口是：

| `resultType` | 结果 | 求值入口 |
| --- | --- | --- |
| `bool` | 布尔判断结果 | `EvaluateBool` |
| `int64` | 有符号 64 位整数 | `EvaluateInt64` |
| `strings` | 字符串集合；输出排序、去重 | `EvaluateStrings` |
| `uint64s` | 无符号 64 位整数集合；输出排序、去重 | `EvaluateUint64s` |

虽然 Evaluation 的两个根必须是 `bool`，但 `int64`、`strings` 和 `uint64s` 节点
可以作为比较或集合谓词的内部 typed operand。

## 2. 通用 JSON 编写规则

### 2.1 节点对象和字段是严格的

每个节点必须是 JSON 对象，且必须有字符串字段 `op`。编译器按 `op` 选择唯一的
字段集合：

- 未知字段立即返回 `UNKNOWN_FIELD`；
- 未知 `op` 返回 `UNKNOWN_OP`；
- 必填字段缺失返回 `MISSING_FIELD`；
- 字段为 `null`、类型不匹配或节点不是对象都会失败；
- 不会忽略自定义字段，也不会把错误类型转换成另一个类型。

例如下面的 `comment` 不被当作注释，文档会被拒绝：

```json
{
  "op": "bool_literal",
  "value": true,
  "comment": "unknown field"
}
```

JSON 不支持注释。若需要说明规则，请写在配置文件旁的 Markdown 或 Go 注释中。

### 2.2 数值、字符串、布尔值和数组

- `bool` 字段必须是真正的 JSON `true` 或 `false`，`"true"` 不合法。
- `string` 字段必须是 JSON 字符串；`null`、数字和布尔值都不合法。
- `int64` 字段必须是十进制 JSON 整数，范围为
  `-9223372036854775808` 至 `9223372036854775807`，不能写成带引号的字符串、
  小数或科学计数法。JSON 本身不允许 `+1`。
- `uint64` 字段必须是十进制非负 JSON 整数，范围为
  `0` 至 `18446744073709551615`，负数、带引号的数字和溢出值不合法。
- 集合字段必须是数组。`strings` 集合数组的每项必须是字符串；`uint64s` 集合
  数组的每项必须是合法的 `uint64` 数字。
- `strings_literal` 和 `uint64s_literal` 的 `values` 可以是空数组；需要至少一
  个子节点的 `children`/`items` 和 `int64_step.steps` 不可以为空。

源码使用严格 JSON 扫描器，因此整个输入还必须满足：

- UTF-8 有效；
- 只能有一个 JSON 值，末尾不能再有第二个值；
- 对象键不能重复；
- 所有对象、数组和字符串都计入结构限制；
- 任意层级的 `null` 都会在 JSON 边界被拒绝。

### 2.3 JSON 中没有 `limits` 字段

`limits` 不是 `expression-scalar/v3` envelope 的合法字段。资源限制由 Go 调用者
通过 `expression.JSONLimits` 或 `CompileProfile.Limits` 传入；把下面内容放进表达式
JSON 会因为 `UNKNOWN_FIELD` 失败：

```json
{
  "schemaVersion": "expression-scalar/v3",
  "resultType": "bool",
  "limits": {"maxDepth": 10},
  "expr": {"op": "bool_literal", "value": true}
}
```

## 3. 完整节点和字段说明

下面的“子节点”均指另一个完整节点对象，不是字符串形式的 JSON。除特别说明外，
每个节点只允许表中列出的字段。

### 3.1 Bool 节点

| `op` | 输出 | 允许字段 | 编写示例 | 语义 |
| --- | --- | --- | --- | --- |
| `bool_literal` | `bool` | `op`、`value` | `{"op":"bool_literal","value":true}` | 返回固定布尔值。 |
| `bool_and` | `bool` | `op`、`children` | `{"op":"bool_and","children":[<bool>,<bool>]}` | 按数组顺序求值；遇到 `false` 立即返回 `false`，全部为真才返回 `true`。至少一个子节点。 |
| `bool_or` | `bool` | `op`、`children` | `{"op":"bool_or","children":[<bool>,<bool>]}` | 按数组顺序求值；遇到 `true` 立即返回 `true`，全部为假才返回 `false`。至少一个子节点。 |
| `bool_not` | `bool` | `op`、`value` | `{"op":"bool_not","value":<bool>}` | 对一个 Bool 子节点取反。 |

`bool_and`/`bool_or` 的 `children` 数组中每项都必须产生 `bool`。短路不仅影响性能，
还决定后续缺失值是否会被访问以及错误是否会出现；因此不要为了“规范化”而随意
调整数组顺序。

### 3.2 Int64 值节点

| `op` | 输出 | 允许字段 | 语义 |
| --- | --- | --- | --- |
| `int64_literal` | `int64` | `op`、`value` | 返回一个有符号 64 位常量。 |
| `int64_ref` | `int64` | `op`、`source`、`name` | 从指定 source 读取一个已在 Contract/Profile 注册的 int64 值。 |
| `int64_add` | `int64` | `op`、`left`、`right` | 两个 Int64 相加，溢出时饱和到 int64 最大值或最小值。 |
| `int64_sub` | `int64` | `op`、`left`、`right` | `left - right`，溢出时采用同样的饱和语义。 |
| `int64_min` | `int64` | `op`、`left`、`right` | 返回两个 Int64 中较小者。 |
| `int64_max` | `int64` | `op`、`left`、`right` | 返回两个 Int64 中较大者。 |
| `int64_step` | `int64` | `op`、`input`、`steps` | 对输入按递增阈值查表，详见下文。 |
| `int64_clamp` | `int64` | `op`、`value`、`min`、`max` | 将 value 限制在闭区间 `[min,max]`，详见下文。 |

常量、引用、算术和边界节点的完整形状例如：

```json
{
  "op": "int64_add",
  "left": {"op": "int64_ref", "source": "tick_facts", "name": "base"},
  "right": {"op": "int64_literal", "value": 10}
}
```

#### `int64_step`

`steps` 是至少一个对象组成的数组，每个对象只能有 `at` 和 `value` 两个 int64
字段，且所有 `at` 必须严格递增：

```json
{
  "op": "int64_step",
  "input": {"op": "int64_ref", "source": "tick_facts", "name": "rating"},
  "steps": [
    {"at": 0, "value": 1},
    {"at": 1000, "value": 2},
    {"at": 2000, "value": 3}
  ]
}
```

求值规则是选择“最后一个满足 `at <= input` 的 step”；如果输入小于第一个阈值，
则返回第一个 step 的 `value`；大于等于最后一个阈值则返回最后一个值。因此上例
中 `rating=-1` 返回 1，`rating=1000` 返回 2，`rating=2500` 返回 3。重复或倒序
阈值返回 `INVALID_STEPS`。

#### `int64_clamp`

`value`、`min`、`max` 都必须是 Int64 子节点，可以是动态引用。运行时按顺序求值，
若 `min > max` 返回 `INVALID_CLAMP_BOUNDS`，不会自动交换边界；否则小于 min 返回
min，大于 max 返回 max，区间内的值原样返回。

### 3.3 Int64 比较节点

以下节点都只接受两个 `int64` 子节点，输出 `bool`；`left` 和 `right` 是必填字段：

| `op` | 判断 |
| --- | --- |
| `int64_eq` | `left == right` |
| `int64_neq` | `left != right` |
| `int64_lt` | `left < right` |
| `int64_lte` | `left <= right` |
| `int64_gt` | `left > right` |
| `int64_gte` | `left >= right` |

例如：

```json
{
  "op": "int64_gte",
  "left": {"op": "int64_ref", "source": "candidate_facts", "name": "tier"},
  "right": {"op": "int64_literal", "value": 2}
}
```

### 3.4 Strings 值节点

| `op` | 输出 | 允许字段 | 语义 |
| --- | --- | --- | --- |
| `strings_literal` | `strings` | `op`、`values` | 返回字符串数组代表的集合；数组可以为空，可以含重复项。 |
| `strings_ref` | `strings` | `op`、`source`、`name` | 从指定 source 读取一个已声明为 strings 的值。 |
| `strings_union` | `strings` | `op`、`items` | 拼接所有 Strings 子节点后形成集合；`items` 至少一个元素。 |

示例：

```json
{
  "op": "strings_union",
  "items": [
    {"op": "strings_literal", "values": ["ranked", "new"]},
    {"op": "strings_ref", "source": "seed_attributes", "name": "tags"}
  ]
}
```

### 3.5 Strings 判断节点

| `op` | 允许字段 | 子节点要求 | 语义 |
| --- | --- | --- | --- |
| `strings_eq` | `op`、`values`、`other` | `values`、`other` 都是 `strings` | 两个集合相等。 |
| `strings_neq` | `op`、`values`、`other` | 同上 | 两个集合不相等。 |
| `strings_is_empty` | `op`、`values` | `values` 是 `strings` | `values` 去重后没有元素时为 true。 |
| `strings_contains` | `op`、`values`、`needle` | `values` 是 `strings`，`needle` 是字符串常量 | `values` 是否包含精确匹配的 needle。 |
| `strings_contains_any` | `op`、`values`、`other` | 两个都是 `strings` | 两个集合是否有至少一个共同元素。 |
| `strings_contains_all` | `op`、`values`、`other` | 两个都是 `strings` | `values` 是否包含 `other` 的全部元素。 |
| `strings_intersects` | `op`、`values`、`other` | 两个都是 `strings` | 两个集合是否相交；与 `contains_any` 的结果相同。 |

`needle` 是普通字符串字段，不是 `strings_literal` 节点。例如：

```json
{
  "op": "strings_contains",
  "values": {"op": "strings_ref", "source": "candidate_attributes", "name": "labels"},
  "needle": "premium"
}
```

### 3.6 Uint64s 值和判断节点

`uint64s` 与 `strings` 的结构和集合语义完全对应，只是元素类型变为无符号 64 位
整数：

| `op` | 输出 | 允许字段 / 语义 |
| --- | --- | --- |
| `uint64s_literal` | `uint64s` | `op`、`values`；values 是 uint64 数组，可为空。 |
| `uint64s_ref` | `uint64s` | `op`、`source`、`name`；引用已注册的 uint64s 值。 |
| `uint64s_union` | `uint64s` | `op`、`items`；至少一个 `uint64s` 子节点。 |
| `uint64s_eq` / `uint64s_neq` | `bool` | `op`、`values`、`other`；集合相等或不等。 |
| `uint64s_is_empty` | `bool` | `op`、`values`；去重后的集合是否为空。 |
| `uint64s_contains` | `bool` | `op`、`values`、`needle`；needle 是 uint64 数字。 |
| `uint64s_contains_any` | `bool` | `op`、`values`、`other`；是否存在共同元素。 |
| `uint64s_contains_all` | `bool` | `op`、`values`、`other`；左集合是否包含右集合全部元素。 |
| `uint64s_intersects` | `bool` | `op`、`values`、`other`；是否相交，结果同 contains_any。 |

例如：

```json
{
  "op": "uint64s_contains",
  "values": {"op": "uint64s_ref", "source": "seed_facts", "name": "allowed_ids"},
  "needle": 42
}
```

## 4. 集合和求值语义

### 4.1 集合输出总是排序去重

`EvaluateStrings` 和 `EvaluateUint64s` 返回新的、排序后的 distinct slice：

- 字符串使用字符串排序；
- uint64s 按数值升序；
- literal、ref 和 union 的重复元素都会被去掉；
- 输入顺序和重复次数不会改变集合谓词的最终布尔结果。

集合谓词在比较前同样会排序去重。对于 `contains_all`，右侧为空集合时“全部包含”
成立；对于 `eq`，两个都为空集合时相等；对于 `is_empty`，只有去重后没有元素才为
true。

`strings_union` 和 `uint64s_union` 的子节点按数组顺序求值。集合值本身不保留顺序，
但当某个子节点运行时出错时，先后顺序决定第一个错误及其路径。

对于集合根，`CollectionUpperBound()` 能在编译期证明的上界为：literal 去重后的
元素数、已在 Contract 中声明 `MaxValues` 的引用上界，以及各 union 子节点上界的
总和；只要其中一部分无界，结果的 `known` 就是 false。Prefilter 会用这个保守上界
检查索引的 `MaxQueryValues`，所以动态集合即使 JSON 语法正确，也可能因查询键上限
而被外层编译器拒绝。

### 4.2 引用值缺失会报错

Lookup 对被引用的值返回 `ok=false` 时，求值返回 `MISSING_VALUE`。表达式不会：

- 用零值、空集合或 false 代替缺失值；
- 从其他 source 猜测同名字段；
- 因为条件不满足而把已经访问到的错误静默掉。

Bool 的 `and/or` 短路可能使某些后续子节点根本不访问，这正是有意设计的求值语义。

### 4.3 算术边界

`int64_add` 和 `int64_sub` 使用饱和算术，不发生 Go int64 溢出：

- 正向溢出结果为 `9223372036854775807`；
- 负向溢出结果为 `-9223372036854775808`。

动态 `int64_clamp` 的反向边界不是编译期常量错误，而是在实际求值时返回
`INVALID_CLAMP_BOUNDS`。如果它被 Prefilter 的 `lookup_range` 使用，Prefilter 还会对
静态 min/max 在编译期执行自己的范围检查。

## 5. `source`、`name` 和 Contract 约束

引用节点必须显式写出 `source` 和 `name`。合法 source 只有六种：

| `source` | 读取内容 | 对应注册声明 |
| --- | --- | --- |
| `seed_attributes` | seed Ticket 的 Attribute | Contract `attributes` |
| `seed_facts` | seed 对象的 Fact | Contract `facts`，scope 为 `object` |
| `tick_facts` | 当前 Tick 的 Fact | Contract `facts`，scope 为 `tick` |
| `candidate_attributes` | candidate Ticket 的 Attribute | Contract `attributes` |
| `candidate_facts` | candidate 对象的 Fact | Contract `facts`，scope 为 `object` |
| `match_facts` | 当前完整 Match 的 Fact | Contract `facts`，scope 为 `match` |

引用 op 与声明类型必须一一对应：

| 引用 op | 需要的声明类型 | 允许的值形态 |
| --- | --- | --- |
| `int64_ref` | `int64` | 单个有符号整数 |
| `strings_ref` | `strings` | 字符串数组 |
| `uint64s_ref` | `uint64s` | uint64 数组 |

Fact 的 scope 与 source 的关系是固定的：

| Fact `scope` | 唯一允许的 source |
| --- | --- |
| `tick` | `tick_facts` |
| `object` | `seed_facts` 或 `candidate_facts` |
| `match` | `match_facts` |

同名的 Fact 和 Attribute 不允许在 Contract 中同时存在。`name` 不能为空，且必须在
当前编译 Profile 的声明列表中；表达式 JSON 不会自行声明字段，也不支持动态字段名。

### 5.1 不同调用场景的 source 权限

`CompileScalarJSON` 本身只执行传入 `CompileProfile` 的权限。仓库内两个业务调用方
使用的 profile 如下：

| 调用场景 | 允许 source | 根结果要求 |
| --- | --- | --- |
| 直接调用 `CompileScalarJSON` | 由 `AllowedSources` 决定 | 由 `AllowedRoots` 决定；空列表表示不限制根类型 |
| Prefilter 的 scalar operand | `seed_attributes`、`seed_facts`、`tick_facts` | 由外层 `lookup_string`/`lookup_uint64`/`lookup_range` 期望的类型决定 |
| Evaluation `canJoin` | 六种 source 全部可用，但仍须满足 Fact scope | 必须为 `bool` |
| Evaluation `canComplete` | `tick_facts`、`match_facts` | 必须为 `bool` |

Prefilter 根文档是 Bitmap，不应把 `resultType:"bitmap"` 传给
`CompileScalarJSON`。反过来，`bool`、`int64`、`strings`、`uint64s` 是标量结果，不能
直接作为 Prefilter 的 Bitmap root。

## 6. 严格 JSON 和资源限制

### 6.1 默认限制

`expression.DefaultJSONLimits()` 返回以下默认值。`JSONLimits` 中的零值会使用默认
值；负值无效。限制由调用方在 Go 中传入，不写在 JSON 文档内。

| `JSONLimits` 字段 | 默认值 | 约束对象 |
| --- | ---: | --- |
| `MaxBytes` | 1,048,576（1 MiB） | 完整 JSON 字节数 |
| `MaxDepth` | 64 | JSON 嵌套深度和表达式节点深度 |
| `MaxObjectFields` | 64 | 任意 JSON 对象字段数 |
| `MaxArrayItems` | 10,000 | 任意 JSON 数组元素数 |
| `MaxValues` | 10,000 | 完整文档中的非复合 JSON 值数量 |
| `MaxStringBytes` | 1,024 | JSON 字符串和值键的 UTF-8 字节数 |
| `MaxChildren` | 128 | 一个表达式变长 children/items 数组；固定节点也受同一边界保护 |
| `MaxLiteralValues` | 256 | 一个 strings/uint64s literal 的 values 元素数；外层还可统计总量 |
| `MaxSteps` | 128 | 一个 `int64_step` 的 steps 元素数；外层还可统计总量 |
| `MaxNodes` | 10,000 | 一个标量文档的表达式节点数；外层调用方可按多个 root 汇总 |
| `MaxInstructions` | 10,000 | 标量编译指令数；当前标量实现每个节点对应一条指令 |

`MaxDepth` 既由 `jsonstrict` 检查整个 JSON 结构，也由 scalar builder 检查表达式
节点深度，所以 envelope 和节点嵌套都会消耗深度预算。`MaxValues` 统计结构扫描看
到的非对象、非数组值，不等同于去重后的集合大小。

### 6.2 外层文档的汇总限制

Evaluation 编译 `canJoin` 和 `canComplete` 后，会额外检查两个根的节点、指令、
literal 值和 step 总和；Prefilter 会把 Bitmap 节点与所有嵌套 scalar operand 一起
统计。一个单独的 scalar 文档可以通过自己的限制，并不代表嵌入外层文档后一定通过
总预算。

业务调用方提供的 `expression.JSONLimits` 只能收紧 Contract 或业务默认边界，不能
通过传入更大的值放宽已有边界。超过一个 limits 参数、任何负限制或不一致的 Profile
声明都会在编译期失败。

### 6.3 直接编译示例

```go
profile := expression.ProfileForRoots(expression.ResultBool)
profile.AllowedSources = expression.CapabilitySeedAttributes |
    expression.CapabilityTickFacts
profile.Attributes = []expression.AttributeSpec{
    {Name: "mode", Type: fact.TypeStrings, MaxValues: 4},
}
profile.Facts = []fact.Spec{
    {Name: "capacity", Type: fact.TypeInt64, Scope: fact.ScopeTick},
}

limits := expression.DefaultJSONLimits()
limits.MaxNodes = 100
profile.JSONLimits = limits

program, err := expression.CompileScalarJSON(data,
    expression.ScalarCompileOptions{Profile: profile})
if err != nil {
    // 用 errors.As 读取 expression.Error 的 Phase/Path/Code/Err。
    return err
}
ok, err := program.EvaluateBool(lookup)
```

`CompileProfile.Limits` 可以提供 `MaxDepth`、`MaxChildren`、`MaxLiteralValues`、
`MaxSteps`、`MaxNodes`、`MaxInstructions` 的标量预算；若同名的
`JSONLimits` 字段非零，则使用 JSONLimits 中的值。所有未指定的 JSON 限制使用默认值。

## 7. 在 Evaluation 和 Prefilter 中嵌套

### 7.1 Evaluation

Evaluation 外层只接受下面的精确形状，两个字段都必须是完整的
`expression-scalar/v3` envelope：

```json
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
```

`canJoin` 可以读取六种 source，`canComplete` 只能读取 Tick 和 Match Fact；两者的
root `resultType` 都必须是 `bool`。Scorer、Match Fact Provider、成员列表和自定义
callback 不写进此 JSON，它们由 `LogicalNodeSpec` 的 Go 字段绑定。

### 7.2 Prefilter

Prefilter 的 Bitmap root 使用自己的 op；只有 `values`、`min`、`max`、`when` 等
指定位置接受完整 scalar envelope：

```json
{
  "schemaVersion": "prefilter/v3",
  "bitmap": {
    "resultType": "bitmap",
    "expr": {
      "op": "lookup_string",
      "index": "partition",
      "values": {
        "schemaVersion": "expression-scalar/v3",
        "resultType": "strings",
        "expr": {
          "op": "strings_union",
          "items": [
            {"op": "strings_ref", "source": "seed_attributes", "name": "partition"},
            {"op": "strings_literal", "values": ["fallback"]}
          ]
        }
      }
    }
  }
}
```

Prefilter 只允许 `seed_attributes`、`seed_facts`、`tick_facts` 的 scalar source；
`lookup_string` 要求嵌套文档结果为 `strings`，`lookup_uint64` 要求为 `uint64s`，
`lookup_range` 的 `min`/`max` 要求为 `int64`。`bitmap.resultType` 必须是 `bitmap`，
不能用标量编译器直接编译。

## 8. 编译、运行时和身份信息

成功编译后得到不可变且不透明的 `ScalarProgram`。调用者不能构造、修改或遍历内部
节点，只能使用以下只读 API：

| API | 用途 |
| --- | --- |
| `ResultType()` | 查询根结果类型。nil program 返回 `ResultInvalid`。 |
| `EvaluateBool` / `EvaluateInt64` / `EvaluateStrings` / `EvaluateUint64s` | 使用只读 Lookup 求值；入口必须与根类型匹配。 |
| `Dependencies()` | 返回按名称排序的 Fact/Attribute 依赖副本。 |
| `Cost()` | 查询节点、深度、literal、step、字符串字节等资源统计。 |
| `CollectionUpperBound()` | 对 strings/uint64s 根查询可证明的保守元素上界。 |
| `Canonical()` | 获取与 JSON 空白和对象字段顺序无关的稳定身份字符串。 |

Lookup 只提供 primitive 读取接口：

```go
type Lookup interface {
    Strings(source Source, name string) ([]string, bool)
    Uint64s(source Source, name string) ([]uint64, bool)
    Int64(source Source, name string) (int64, bool)
}
```

Lookup 返回的 slice 会被表达式当作只读输入；表达式不会写回 Ticket、Fact map、Match
成员或 Provider。集合求值会产生自己的排序去重结果。

## 9. 错误和排查

所有表达式错误都是结构化的 `expression.Error`：

```go
var expressionErr *expression.Error
if errors.As(err, &expressionErr) {
    fmt.Println(expressionErr.Phase, expressionErr.Path,
        expressionErr.Code, expressionErr.Err)
}
```

`Path` 使用 JSONPath 风格。例如直接编译时，左操作数错误通常位于
`$.expr.left`；运行时路径为 `root.left`。Evaluation 和 Prefilter 会在适配时加上
`$.canJoin`、`$.canComplete` 或 `$.bitmap.expr` 前缀。

### 9.1 JSON/语法阶段

| Code | 常见原因 |
| --- | --- |
| `INVALID_UTF8`、`INVALID_JSON`、`TRAILING_JSON` | 输入不是有效 UTF-8/JSON，或末尾存在第二个 JSON 值。 |
| `DUPLICATE_KEY` | 同一 JSON 对象重复键。 |
| `JSON_SIZE_LIMIT`、`DEPTH_LIMIT`、`OBJECT_FIELD_LIMIT`、`ARRAY_ITEM_LIMIT`、`VALUE_LIMIT`、`STRING_SIZE_LIMIT` | 触发结构资源限制。 |
| `NULL_NOT_ALLOWED`、`NULL_NODE`、`NULL_FIELD` | envelope、节点或字段为 null。严格扫描通常会先返回 `NULL_NOT_ALLOWED`。 |
| `MISSING_FIELD` | `schemaVersion` 等必填字段缺失；标量 envelope 缺失 `schemaVersion` 时属于此类。 |
| `UNKNOWN_SCHEMA_VERSION` | `schemaVersion` 已提供但不是 `expression-scalar/v3`（旧版本、拼写错误或其他版本）。 |
| `UNKNOWN_FIELD` | envelope 或节点含不在当前 op 允许列表中的字段。 |
| `INVALID_OBJECT` | 需要对象的位置不是对象。必填字段缺失见上面的 `MISSING_FIELD`。 |
| `TYPE_MISMATCH` | 字段、数组元素或子节点类型不符合 op 规则。 |
| `UNKNOWN_RESULT_TYPE`、`ROOT_NOT_ALLOWED` | resultType 未知、Bitmap 被当成 scalar，或调用 profile 不允许该根类型。 |
| `UNKNOWN_OP` | op 不在闭合的节点集合中。 |
| `EMPTY_CHILDREN`、`CHILD_LIMIT` | `bool_and`、`bool_or`、集合 union 的 children/items 为空或过多。 |
| `EMPTY_STEPS`、`STEP_LIMIT`、`INVALID_STEPS` | step 表为空、超限或阈值不是严格递增。 |
| `INVALID_NUMBER` | int64/uint64 非十进制整数、溢出、负 uint64 或使用了字符串形式数字。 |
| `UNKNOWN_SOURCE`、`EMPTY_NAME` | source 未知或引用 name 为空。 |

### 9.2 Profile/编译阶段

| Code | 常见原因 |
| --- | --- |
| `SOURCE_NOT_ALLOWED` | 当前 profile 没有授权该 source。 |
| `MISSING_ATTRIBUTE`、`MISSING_FACT` | name 没有在当前 Contract/Profile 注册。 |
| `ATTRIBUTE_TYPE_MISMATCH`、`FACT_TYPE_MISMATCH` | 引用 op 的结果类型与声明类型不同。 |
| `FACT_SCOPE_NOT_ALLOWED`、`FACT_SCOPE_MISMATCH` | 业务阶段或 Fact scope 不允许从当前 source 读取。 |
| `ROOT_NOT_ALLOWED`、`ROOT_TYPE_MISMATCH` | profile 不允许 envelope 根，或根节点实际类型与 resultType 声明不同。 |
| `INVALID_LIMIT`、`INVALID_CAPABILITIES` | Profile/JSONLimits 含负限制或未知 capability 位。 |
| `DUPLICATE_ROOT`、`INVALID_RESULT`、`INVALID_FACT`、`INVALID_ATTRIBUTE` | Profile 声明本身无效。 |
| `DUPLICATE_FACT`、`DUPLICATE_ATTRIBUTE`、`DUPLICATE_NAME` | Profile 中有重复声明或 Fact/Attribute 同名。 |
| `NODE_LIMIT`、`INSTRUCTION_LIMIT` | 表达式节点或编译指令超过预算。 |

### 9.3 运行时阶段

| Code | 常见原因 |
| --- | --- |
| `NIL_PROGRAM`、`NIL_LOOKUP` | 没有提供有效的编译程序或 Lookup。 |
| `TYPE_MISMATCH` | 调用了与根类型不匹配的 Evaluate 方法，或内部节点类型不一致。 |
| `MISSING_VALUE` | Lookup 对实际访问的引用返回 false。 |
| `INVALID_CLAMP_BOUNDS` | 动态 clamp 的 min 大于 max。 |
| `INVALID_NODE`、`CYCLE` | 程序内部节点无效或检测到循环；正常 JSON 编译不会生成这类程序。 |

错误必须被调用方处理，不能把编译/求值错误转换成 false、空集合、零值或“全量匹配”。
在 Evaluation 中，`CanJoin`/`CanComplete` 出错时不应继续提交 Match；在 Prefilter 中，
查询绑定错误也不会静默转为空候选集。

## 10. 编写检查清单

提交配置前按以下顺序检查：

1. 顶层是否有且只有 `schemaVersion`、`resultType`、`expr`，并且版本是
   `expression-scalar/v3`。
2. 每个节点是否为对象，`op` 是否拼写准确，是否添加了该 op 不支持的字段。
3. 每个子节点的结果类型是否与父节点要求一致；根节点实际类型是否等于
   `resultType`。
4. `int64`/`uint64` 是否写成 JSON 数字而不是字符串；step 的 `at` 是否严格递增。
5. `source` 是否属于当前调用阶段，`name` 是否在同一份 Contract/Profile 声明，
   Fact scope 和类型是否匹配。
6. `bool_and`/`bool_or`/union/items/steps 是否非空，数组、深度、节点和 literal
   数量是否未超限。
7. 如果表达式嵌入 Evaluation 或 Prefilter，是否把嵌入位置写成完整 scalar
   envelope，并满足外层对根类型和 source 的额外约束。

实现依据：

- [表达式公共类型与 Profile](../../../internal/matchsystem/expression/schema.go)
- [expression-scalar/v3 JSON 解析与严格校验](../../../internal/matchsystem/expression/json.go)
- [节点类型、类型检查与资源统计](../../../internal/matchsystem/expression/compiler.go)
- [运行时求值语义](../../../internal/matchsystem/expression/program.go)
- [Prefilter 标量 operand 嵌套方式](../../../internal/matchsystem/prefilter/compiler.go)
- [Evaluation 两个 Bool root 的 profile](../../../internal/matchsystem/evaluation/predicates.go)
