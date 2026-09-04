# ADR：表达式简化与 Prefilter/Evaluation v3 契约

- 状态：Accepted（P0 冻结）
- 日期：2026-08-26
- 范围：`expression`、`prefilter`、`evaluation`、`LogicalNode` 和
  `MatchFactProvider` 的目标配置与运行边界
- 关联方案：[expression、prefilter、evaluation 拆分执行参考](expression-engine-simplification-refactor-plan.md)
- 业务 Contract：[logical-node-contract/v2](../logical-node-contract-v2.md)

本文是 P0 的规范性决策。方案文档中的目标版本、JSON shape、事件顺序、错误
边界和身份规则以本文为准；旧代码、旧文档示例和旧测试不是兼容依据。除非另有
说明，`MUST` 表示实现必须满足，`MUST NOT` 表示实现不得提供该路径。

## 1. 决策摘要

| 对象 | P0 目标 | 决策 |
| --- | --- | --- |
| 业务 Contract | `logical-node-contract/v2` | shape 和语义保持不变，继续接受，仍是唯一 Contract |
| 标量表达式 JSON | `expression-scalar/v1` | 只允许 `bool`、`int64`、`strings`、`uint64s`，不包含 Bitmap |
| Prefilter JSON | `prefilter/v3` | Prefilter 自己拥有 Bitmap AST、索引叶子和物理执行 |
| Evaluation JSON | `evaluation/v3` | 只声明 `canJoin`、`canComplete` 两个 Bool 谓词 |
| Prefilter 身份 | `prefilter-fingerprint/v5` | 由 Prefilter 逻辑、实际查询 sidecar、依赖、限制和运行参数派生 |
| 候选评分 | 每个 `LogicalNode` 一个 Go callback | 绑定在 `LogicalNodeSpec`，不属于 JSON-only 约束，不使用 registry |
| Match Fact 更新 | 唯一 `MatchFactProvider` | Provider 生成完整快照，LogicalNode 校验后原子提交 |

这是一次不保持运行时兼容的 breaking change。P0 不实现双读、双写、旧入口
fallback、隐式升级或运行时自动迁移。

## 2. 不变的业务 Contract

`logical-node-contract/v2` 的以下内容全部保持现有 shape 和语义：

- 顶层 `schemaVersion`、`attributes`、`facts`、`indexes` 和可选 `limits`；
- Attribute/Facts 的 `strings`、`int64`、`uint64s` 类型；
- Fact 的 `tick`、`object`、`match` scope，以及名称全局唯一规则；
- `multi_value`、`int64_range` Index 的字段、key type 和文档/查询上限；
- Contract 的严格 JSON 校验、默认限制、名字/类型/scope/Index 约束。

目标 loader 只接受 `logical-node-contract/v2`。本 ADR 不定义
`logical-node-contract/v3`，也不把 Prefilter/Evaluation 的 envelope 版本误当成
业务 Contract 版本。Prefilter 和 Evaluation 编译时必须使用同一份已经校验并快照的
Contract，不允许各自携带或覆盖第二份 Contract。

## 3. 领域所有权和简化边界

```text
expression (scalar AST / JSON / compiler / Program)
        ^                         ^
        |                         |
prefilter (Bitmap / index)   evaluation (Bool predicates)
        \                         /
             LogicalNode
              /       \
     CandidateScorer  MatchFactProvider
```

### 3.1 expression

`expression` 是标量表达式的唯一 parser、类型检查器、compiler、evaluator、依赖
收集器和 canonical 生成器。它保留四类标量结果及其结构节点，但目标公共 JSON 和
目标公共 Program 不再包含 Bitmap 结果、Bitmap lattice、Roaring 语义或通用
`domain_call`。

标量表达式只能读取由编译 profile 明确授权的 primitive lookup。它不能读取
Ticket、Match、成员列表、IndexStore 或 Provider，也不能写入任何 Fact。

### 3.2 prefilter

Prefilter 独占 Bitmap AST、候选集合、Index lookup、anchor、sidecar、scope 和
Roaring 执行。Prefilter 的 Bitmap parser/compiler 可以调用 expression 编译每个
标量动态 operand，但只保存 opaque 的标量 Program/句柄，不向公共 API 暴露
`InstructionID`、`NodeRef`、`DomainLeaf` 或另一套通用 IR。

### 3.3 evaluation

Evaluation 只编译和求值 `CanJoin`、`CanComplete` 两个 Bool 谓词。它不负责
Match Fact 更新、workflow、可配置 phase、scorer registry 或 Provider registry。
Evaluation 的自定义叶子（如确有需要）必须是标量 typed leaf，且由所属阶段声明
允许的 source；不得产生 Bitmap、访问 Match 成员或修改 Match Fact。P0 不再定义
通用 `domain_call` JSON 逃生口；新增 JSON leaf 需要所属包和后续 ADR 同时登记其
字段、结果类型和错误边界。

### 3.4 LogicalNode、scorer 和 Provider

- `LogicalNode` 拥有固定事件顺序、候选 Top-L、当前 Match Fact 快照和提交时机。
- 每个 `LogicalNodeSpec` 必须直接绑定且仅绑定一个非 nil 的
  `CandidateScorer` Go callback。不存在 scorer registry，也不存在 JSON 中的
  scorer 名称。
- JSON-only 约束只适用于声明式配置；`CandidateScorer` 和
  `MatchFactProvider` 是明确的 Go 编排边界，不把 Go callback 编码进 JSON。
- 声明了 `match` scope Fact 的规则必须绑定且仅绑定一个
  `MatchFactProvider`。没有 match-scope Fact 时不调用 Provider；不得用旧
  Evaluation 更新表达式或 LogicalNode 直接写入替代 Provider。

## 4. `expression-scalar/v1` JSON

### 4.1 Root shape

一个标量 Root 的规范 shape 是：

```json
{
  "schemaVersion": "expression-scalar/v1",
  "resultType": "bool|int64|strings|uint64s",
  "expr": { "op": "..." }
}
```

三个字段均为必填；对象字段顺序、空白和 JSON 数字/字符串的表示空格不影响
语义。未知字段、重复字段、`null`、尾随 JSON 值和缺失字段都拒绝。Root 的
`resultType` 必须等于 `expr` 的实际结果；`bitmap`、未知 result type 和没有
`schemaVersion` 的旧 Root 均拒绝。

`expression-scalar/v1` 是标量语言版本。嵌入 Prefilter/Evaluation 的每个标量
operand 仍使用完整的上述 Root shape，不能使用裸 AST 或裸数组/数字作为动态
operand。

### 4.2 P0 合法内建节点

节点的结果由 `op` 固定，父节点只接受下表指定的 child result：

| 结果 | `op` |
| --- | --- |
| Bool | `bool_literal`、`bool_and`、`bool_or`、`bool_not` |
| Int64 | `int64_literal`、`int64_ref`、`int64_step`、`int64_clamp`、`int64_add`、`int64_sub`、`int64_min`、`int64_max` |
| Strings | `strings_literal`、`strings_ref`、`strings_union` |
| Uint64s | `uint64s_literal`、`uint64s_ref`、`uint64s_union` |
| Bool predicate | `int64_eq`、`int64_neq`、`int64_lt`、`int64_lte`、`int64_gt`、`int64_gte` |
| Bool predicate | `strings_eq`、`strings_neq`、`strings_is_empty`、`strings_contains`、`strings_contains_any`、`strings_contains_all`、`strings_intersects` |
| Bool predicate | `uint64s_eq`、`uint64s_neq`、`uint64s_is_empty`、`uint64s_contains`、`uint64s_contains_any`、`uint64s_contains_all`、`uint64s_intersects` |

`*_ref` 的 `source` 只能取：
`seed_attributes`、`seed_facts`、`tick_facts`、`candidate_attributes`、
`candidate_facts`、`match_facts`。实际可读 source 由使用方 profile 再收紧。

`bool_and`/`bool_or`、`strings_union`、`uint64s_union` 的 children/items 必须
非空；`int64_step` 的 steps 必须非空且按 `at` 严格递增；集合 literal 在求值
前排序去重。所有比较、集合 contains/intersects 等判断节点返回 Bool。

这里的“评估表达式返回 Bool”指 `CanJoin`、`CanComplete` 的 Root 以及它们的
谓词出口必须返回 Bool。比较所需的 Int64/Strings/Uint64s literal/ref 是受父节点
约束的操作数，不是绕过 Bool 决策的第三种结果。

## 5. `prefilter/v3` JSON

### 5.1 Envelope 和 Bitmap Root

Prefilter v3 的唯一目标 envelope 是：

```json
{
  "schemaVersion": "prefilter/v3",
  "bitmap": {
    "resultType": "bitmap",
    "expr": { "op": "..." }
  },
  "runtime": {
    "containsProbeThreshold": 4096
  }
}
```

`bitmap` 必填，`runtime` 可省略；`runtime` 目前只允许
`containsProbeThreshold`，省略或为零使用实现默认值。目标 Bitmap 节点不再
使用旧的 `plan` wrapper、`bitmap_*` 前缀、`tag`、`kind`、`fields` 或 generic
`domain_call`。

### 5.2 Bitmap 节点

`bitmap.expr` 是闭合的 Bitmap AST：

```text
none
and(children: BitmapNode[])
or(children: BitmapNode[])
exclude(value: BitmapNode)
if(when: BoolRoot, then: BitmapNode, else: BitmapNode)
lookup_string(index: string, values: StringsRoot)
lookup_uint64(index: string, values: Uint64sRoot)
lookup_range(index: string, min: Int64Root, max: Int64Root)
```

对应 JSON 示例：

```json
{
  "schemaVersion": "prefilter/v3",
  "bitmap": {
    "resultType": "bitmap",
    "expr": {
      "op": "and",
      "children": [
        {
          "op": "lookup_string",
          "index": "mode_index",
          "values": {
            "schemaVersion": "expression-scalar/v1",
            "resultType": "strings",
            "expr": {
              "op": "strings_literal",
              "values": ["ranked"]
            }
          }
        },
        { "op": "none" }
      ]
    }
  }
}
```

`lookup_string` 只能绑定 Contract 中 `string` `multi_value` Index，
`lookup_uint64` 只能绑定 `uint64` `multi_value` Index，`lookup_range` 只能绑定
`int64_range` Index。`index` 必须是 Contract 已声明的名字。

`values`、`min`、`max` 始终是完整 scalar Root；静态查询也写成对应的 literal
Root，不保留另一套静态数组/裸数字语法。Prefilter 动态 scalar 的 source 只能
是 `seed_attributes`、合法的 Seed Object Fact 和当前 `tick_facts`；Candidate
和 Match source 拒绝。动态 operand 只能产生 `strings`、`uint64s` 或 `int64`，
不能产生 Bitmap。

`and`/`or` 要求非空 children；`exclude` 的 child 必须满足现有 Prefilter 的
scope/anchor 规则，非法嵌套或没有正向 scope 在 compile/runtime 边界拒绝；`if`
的 `when` 必须是 BoolRoot，两个分支必须是 BitmapNode。所有 Bitmap 执行、
anchor、sidecar、Index 和 Roaring 逻辑只属于 Prefilter。

## 6. `evaluation/v3` JSON

Evaluation v3 只声明两个 Bool predicate：

```json
{
  "schemaVersion": "evaluation/v3",
  "canJoin": {
    "schemaVersion": "expression-scalar/v1",
    "resultType": "bool",
    "expr": {
      "op": "int64_lt",
      "left": {
        "op": "int64_ref",
        "source": "match_facts",
        "name": "count"
      },
      "right": { "op": "int64_literal", "value": 4 }
    }
  },
  "canComplete": {
    "schemaVersion": "expression-scalar/v1",
    "resultType": "bool",
    "expr": {
      "op": "int64_gte",
      "left": {
        "op": "int64_ref",
        "source": "match_facts",
        "name": "count"
      },
      "right": { "op": "int64_literal", "value": 4 }
    }
  }
}
```

顶层只允许 `schemaVersion`、`canJoin`、`canComplete`，三个字段均必填。v2 的
`candidateScorer`、`matchFacts`、`join`、`complete` 以及任何 workflow/phase
字段在 v3 都是未知字段，不会被转换或忽略。

`canJoin` 的 Root 必须是 Bool，编译期允许的 source 为：Seed 属性/Fact、Tick
Fact、当前 Candidate 属性/Fact、候选加入前的 Match Fact。它只能看到一个当前
候选，不得读取/遍历已有 Match 成员。

`canComplete` 的 Root 必须是 Bool，唯一允许的 source 为 Match Fact 和 Tick
Fact；它不能读取 Seed/Candidate 属性或 Fact，也不能读取 Match 内成员数据。
两个谓词都只能读，不能调用 Provider、改变 Fact 或改变事件顺序。

## 7. CandidateScorer 和 MatchFactProvider

### 7.1 CandidateScorer

候选评分是 `LogicalNodeSpec` 的直接 Go 依赖，概念接口为：

```go
type CandidateScorer func(CandidateScoreContext) (float64, error)
```

一个 LogicalNode 只选择一个 scorer。输入可以包含 Seed/Candidate 的只读属性、
Seed/Candidate Object Fact、当前 Tick Fact 和 `Now`；不包含 Match Fact、已有成员
列表、Match 对象或 Provider。返回值必须是有限的 `float64`；panic、错误和非有限
值不能触发 Join、Provider 或 Match 提交。scorer 不参与 JSON canonical，也不放入
Evaluation registry。

### 7.2 唯一 MatchFactProvider

声明了 match-scope Fact 的 LogicalNode 必须直接持有一个 Provider；不新增
Provider registry、workflow、transaction 或 snapshot 对象。接口和输入边界为：

```go
type MatchFactProvider interface {
    Initialize(context.Context, InitializeInput) (fact.Values, error)
    OnJoin(context.Context, JoinInput) (fact.Values, error)
}

type InitializeInput struct {
    Now            int64
    SeedAttributes *common.Ticket // 只读借用
    SeedFacts      fact.Values     // 只读借用
    TickFacts      fact.Values     // 只读借用
}

type JoinInput struct {
    Now              int64
    SeedAttributes   *common.Ticket // 只读借用
    SeedFacts        fact.Values     // 只读借用
    TickFacts        fact.Values     // 只读借用
    Candidate        *common.Ticket // 只读借用，只有当前候选
    CandidateFacts   fact.Values     // 只读借用
    MatchFactsBefore fact.Values     // 候选加入前的完整只读快照
}
```

输入中的 map/slice/pointer 只在同步回调期间借用；Provider 不得修改、留存、
通过句柄回查或把它们转换成已有成员访问。Provider 不接收 `Match`、成员列表、
成员迭代器、LogicalNode、Prefilter 状态、expression AST/Program/Value、patch
或 transaction。

`Initialize` 必须返回 seed 建立后的完整 Match Fact 快照；`OnJoin` 必须返回当前
候选加入后的完整快照。完整快照的含义是：每个声明的 match-scope Fact 都出现在
正确的 type map 中，空的多值 Fact 也必须以空 slice/键表示；不得缺字段、加未
声明字段或把一个名字放进多个 type map。所有值必须通过 Contract 的 type、scope
和 MaxValues 校验。

提交规则只有一条：Provider 成功且快照校验通过后，LogicalNode 复制并一次性提交
“候选 + 新 fact.Values”。提交前不修改旧快照；Provider 返回 patch、半快照或
原地修改均不合法。提交成功后，后续 Provider/CanJoin 只能使用新的聚合 Match
Fact 和新的单个当前候选，不能再次使用已有成员的属性或 Fact。

失败语义固定为 fail closed：

| 失败位置 | 必须结果 |
| --- | --- |
| Provider 缺失（有 match Fact） | 编译/创建 LogicalNode 失败，`MISSING_MATCH_FACT_PROVIDER` |
| `Initialize` 返回 error、panic、取消或非法/不完整快照 | 当前 seed 尝试不产出 Match，不提交任何 Match Fact；seed 继续遵守当前轮次的已消费规则 |
| `OnJoin` 返回 error、panic、取消或非法/不完整快照 | 当前候选不加入，旧 Match Fact 不变，当前 seed 尝试结束并返回错误；不尝试 patch、重试或旧表达式 fallback |
| `CanJoin == false` | 不调用 OnJoin，不改变候选组和 Match Fact |
| `CanComplete == false` | 保留已经成功原子提交的中间状态，继续下一个候选 |
| `CanComplete` 或其它运行时错误 | 不产生后续副作用，当前 seed 尝试 fail closed |

Provider 失败不能静默跳过、隐式重试、半更新、回退到 Evaluation 或合并旧值。

## 8. 固定事件顺序

目标运行时只允许以下顺序；配置不能重排、插入或重复这些阶段：

```text
BeginMatchRound / reserve seed
  -> Tick Fact Provider + FactFrame 校验
  -> seed Object Fact
  -> MatchFactProvider.Initialize
  -> 完整 MatchFact 校验并提交初始快照
  -> CanComplete（seed-only）
       true  -> 提交 seed Match
       false -> Prefilter
              -> Candidate Object Fact
              -> CandidateScorer（候选安全超集，bounded Top-L）
              -> 对 Top-L 候选逐个：
                   CanJoin
                     false -> 下一个候选
                     true  -> MatchFactProvider.OnJoin
                           -> 完整快照校验
                           -> 原子提交候选 + MatchFact
                           -> CanComplete
                                true  -> 提交 Match 并结束 seed
                                false -> 下一个候选
  -> 没有成立 Match 时结束 seed 尝试
```

实现上 scorer 必须在每个候选进入 `CanJoin` 前执行；Provider.OnJoin 必须在
`CanJoin == true` 后执行；`CanComplete` 只能出现在 seed-only 或成功 OnJoin
原子提交之后。v3 不保留旧实现循环末尾的重复 Complete；旧重复调用只可作为
迁移期间的 trace oracle，不是目标运行时行为。

## 9. Limits 汇总

所有 loader/compile 阶段使用一个不可变的 effective limit snapshot。零值先使用
默认值，显式负值拒绝；调用方限制只能收紧，不能扩大 Contract 的重叠上限。
P0 的 v3 envelope 不增加第二份 `limits` 字段，limits 由 Contract 和一次性
loader/compile options 提供。

| 层级 | 限制 | 适用范围 |
| --- | --- | --- |
| JSON 结构 | `MaxBytes`、`MaxDepth`、`MaxObjectFields`、`MaxArrayItems`、`MaxValues`、`MaxStringBytes` | 整个 raw JSON envelope，含所有嵌套 scalar Root |
| Scalar AST/compiler | `MaxDepth`、`MaxChildren`、`MaxLiteralValues`、`MaxSteps`、`MaxNodes`、`MaxInstructions` | expression-scalar/v1 的所有节点 |
| Prefilter Bitmap | Bitmap 节点、嵌套 scalar operand 和 lookup 字段 | 同一份文档/编译预算，不得用 operand 子编译绕过限制 |
| Evaluation | `canJoin` 与 `canComplete` 两个 Root 的总文档预算 | 两个 Root 共享同一份 limits snapshot |
| Contract | `MaxIndexes`、`MaxAttributes`、`MaxFacts`、`MaxValues`、`MaxDocumentValues`、`MaxQueryValues` | Contract 条目、Fact/Attribute 值和 Index 查询 |
| Prefilter runtime | 每个 Index 的 `maxDocumentValues`、`maxQueryValues`；`containsProbeThreshold` | Index bind/estimate/lookup，不改变 AST 上限 |
| Provider 输出 | match Fact 的 type、scope、完整性和 `MaxValues` | 每次 Initialize/OnJoin 返回值 |

Contract 的 `MaxBytes`、`MaxDepth`、`MaxChildren`、`MaxStringBytes` 和 `MaxValues`
是共同外边界；标量专有的 AST 限制使用共享 expression 默认值或调用方更紧的
值。动态 operand、两个 Evaluation Root 和 Bitmap 结构不得通过拆分文档或建立
子 Program 逃避总量限制。

## 10. Canonical、Fingerprint 和发布身份

### 10.1 Scalar canonical

expression-scalar/v1 的 canonical 是由编译后的 typed scalar AST 产生的确定性
字节序列/字符串：包含语言版本、Root result、op、source/name、primitive literal
和 child 结构；不包含 JSON 空白、对象字段顺序、运行时 Fact、Provider 输出、
scorer callback 地址、Arena numeric ID 或 opaque handle。集合 literal/运行时集合
按语言的排序去重规则归一化；具有顺序语义的 children 保持顺序。

相同标量语义必须产生相同 canonical；canonical 变化即表示需要重新编译。P0 不
要求为了交换律对 `and/or` 做额外重排，不能把这种重排当成兼容保证。

### 10.2 Prefilter fingerprint

Prefilter 使用 `prefilter-fingerprint/v5` 的 SHA-256 十六进制值。输入至少包括：

- v3 Bitmap 结构 canonical；
- 每个实际使用的 Index query sidecar（Index 名、类型、key type、查询限制、
  动态 scalar canonical 或静态规范化值）；
- 实际依赖的 Attribute/Fact 和 scope/type；
- effective limits；
- `containsProbeThreshold` 等会改变物理执行的运行参数；
- `logical-node-contract/v2` 和 `prefilter/v3` 的身份标识。

Fingerprint 不包含当前候选 Bitmap、Tick/Match Fact 值、Provider 逻辑、scorer
逻辑或 Match 生命周期状态。旧 `prefilter-fingerprint/v4` 不能与 v5 混用；
发布/缓存必须重新编译。Evaluation 不另造 workflow fingerprint；如部署需要
绑定 Provider/scorer 代码版本，由部署系统在外部 release bundle 记录，不进入
四个核心包的契约。

## 11. 错误边界

所有边界错误保留 `{Phase, Path, Code, Err}`；`Err` 文本不属于兼容协议，调用方
只能依赖 `Phase`、结构化 `Path` 和下列稳定 Code。上层包装错误时必须保留原 Code
并只增加 owner 前缀（例如 `$.bitmap`、`$.canJoin`、`matchFacts.onJoin`）。

| Phase | P0 稳定 Code（非穷尽的实现内部错误不得改变这些边界含义） |
| --- | --- |
| json | `UNKNOWN_SCHEMA_VERSION`、`MISSING_FIELD`、`UNKNOWN_FIELD`、`DUPLICATE_KEY`、`TRAILING_JSON`、`INVALID_JSON`、`INVALID_UTF8`、`NULL_NOT_ALLOWED`、`TYPE_MISMATCH`、`UNKNOWN_OP`、`UNKNOWN_RESULT_TYPE`、`ROOT_NOT_ALLOWED`、`DEPTH_LIMIT`、`CHILD_LIMIT`、`NODE_LIMIT`、`INSTRUCTION_LIMIT`、`VALUE_LIMIT`、`STRING_SIZE_LIMIT`、`INVALID_NUMBER` |
| compile | `ROOT_TYPE_MISMATCH`、`SOURCE_NOT_ALLOWED`、`UNKNOWN_SOURCE`、`MISSING_ATTRIBUTE`、`MISSING_FACT`、`ATTRIBUTE_TYPE_MISMATCH`、`FACT_TYPE_MISMATCH`、`FACT_SCOPE_MISMATCH`、`DYNAMIC_RESULT_MISMATCH`、`MISSING_INDEX`、`QUERY_INDEX_MISMATCH`、`QUERY_KEY_CONTRACT`、`INVALID_RANGE`、`INVALID_BITMAP`、`MISSING_MATCH_FACT_PROVIDER` |
| evaluate | `MISSING_VALUE`、`INVALID_PROGRAM`、`QUERY_BIND`、`INDEX_LOOKUP`、`NONFINITE_SCORE`、`PROVIDER_ERROR`、`PROVIDER_CANCELED`、`MATCH_FACT_INCOMPLETE`、`FACT_VALUE_LIMIT` |

Provider 返回的 Contract 值错误继续使用对应的 Fact error code（例如
`FACT_TYPE_MISMATCH`、`FACT_SCOPE_MISMATCH`、`FACT_VALUE_LIMIT`），但必须按
`evaluate` 边界包装并遵守原子失败语义。

## 12. 版本正负矩阵和兼容规则

### 12.1 正向矩阵

| 输入组合 | 结果 |
| --- | --- |
| `logical-node-contract/v2` + 合法 `prefilter/v3` | loader 成功，compile 成功，实际可进入 Bitmap bind/estimate/lookup/evaluate |
| `logical-node-contract/v2` + 合法 `evaluation/v3` + 必要的 Go scorer/Provider | loader 成功，compile 成功，`CanJoin` 和 `CanComplete` 实际可求值 |
| `expression-scalar/v1` BoolRoot | expression loader/compiler 成功并返回 Bool Program |
| `expression-scalar/v1` Int64/Strings/Uint64s Root 作为合法 operand | 只在父节点声明的 expected type 下成功；不能作为 Evaluation 顶层谓词 |
| Contract 无 match-scope Fact + nil Provider | 合法；不发生 MatchFactProvider 调用 |
| Contract 有 match-scope Fact + 一个非 nil Provider | 合法；每次 seed/Join 按固定顺序生成完整快照 |

正向测试必须证明配置真正进入 compile，并且谓词/Bitmap 真正执行；仅证明没有
被拒绝不算通过。

### 12.2 负向矩阵

| 输入 | 必须结果 |
| --- | --- |
| `prefilter/v1`、`prefilter/v2`、未知或缺失版本 | 在 plan 解析前以 `UNKNOWN_SCHEMA_VERSION` 拒绝 |
| `evaluation/v1`、`evaluation/v2`、未知或缺失版本 | 在谓词解析前以 `UNKNOWN_SCHEMA_VERSION` 拒绝 |
| `logical-node-contract/v1`、未知或缺失版本 | 以 `UNKNOWN_SCHEMA_VERSION` 拒绝；`logical-node-contract/v2` 不拒绝 |
| 无版本、Bitmap root、`domain_call` 或旧 mixed expression Root | 以对应 `MISSING_FIELD`/`ROOT_NOT_ALLOWED`/`UNKNOWN_OP` 拒绝，不进入 scalar compiler |
| v3 Prefilter 中的 `plan`、旧 `bitmap_*`、`tag/kind/fields` | `UNKNOWN_FIELD` 或 `UNKNOWN_OP`，不转换为 v3 |
| v3 Evaluation 中的 `candidateScorer`、`matchFacts`、`join`、`complete` 或 phase/workflow 字段 | `UNKNOWN_FIELD`，不保留旧入口 |
| 动态 operand 不是完整 scalar Root、声明 Bitmap、类型与 lookup 不匹配 | `TYPE_MISMATCH`/`DYNAMIC_RESULT_MISMATCH`，不建立子 Program fallback |
| CanJoin 使用 Candidate 之外的成员数据；CanComplete 使用 Seed/Candidate 或成员数据 | compile/runtime `SOURCE_NOT_ALLOWED`，不隐式降级 |
| 有 match-scope Fact 但 Provider 缺失、输出缺字段/多字段/错误类型/超限 | `MISSING_MATCH_FACT_PROVIDER` 或 `MATCH_FACT_INCOMPLETE`/Fact error；候选和旧快照不变 |
| 旧 fingerprint、旧 typed Config/Builder/Arena/Root authoring 入口作为生产输入 | 不接受为 v3 生产配置；只可作为离线迁移/对照工具，不能被 runtime loader 调用 |

任何版本不匹配都不能触发双读、双写、旧 parser、旧 executor、运行时升级、静默
忽略未知字段或自动生成 Provider/scorer。迁移由规则拥有者离线逐项完成；切换后
线上只有 v3 loader 与 `logical-node-contract/v2`。

## 13. P0 验收门槛

- loader 只接受上述正向矩阵，所有负向版本/shape 均在正确边界 fail closed；
- expression 的目标公共语言没有 Bitmap，Prefilter 的目标 Bitmap 没有 generic
  `domain_call`，Evaluation 只有两个 Bool predicate；
- 每个需要 Match Fact 的规则都有且只有一个 Provider；无 Evaluation 更新、
  LogicalNode 直接写入、patch/merge fallback 或重复成员遍历；
- trace 证明固定事件顺序、CanJoin/OnJoin/原子提交/CanComplete 的条件调用；
- Provider 成功/失败/超时/非法快照、limits、canonical、fingerprint 和错误
  Code 均有 golden/单元验证；
- no dual-read/compat 搜索、依赖扫描和发布缓存清理完成后，才允许切换 runtime。
