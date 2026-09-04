# logical-node-contract/v2

`logical-node-contract/v2` 是 Prefilter 与 Evaluation 共享的唯一业务契约。它定义
Attributes、物理索引和三类 Fact；`prefilter/v2` 与 `evaluation/v2` 只是各自的 JSON
envelope，和 Contract 版本不是同一概念，所有语义编译都回到这份 Contract。

## 1. JSON 结构

```json
{
  "schemaVersion": "logical-node-contract/v2",
  "attributes": [
    {"name": "mode", "type": "strings", "maxValues": 8},
    {"name": "rating", "type": "int64"}
  ],
  "indexes": [
    {"type": "multi_value", "name": "mode_index", "field": "mode",
     "keyType": "string", "maxDocumentValues": 8, "maxQueryValues": 8},
    {"type": "int64_range", "name": "rating_index", "field": "rating"}
  ],
  "facts": [
    {"name": "capacity", "type": "int64", "scope": "tick"},
    {"name": "tier", "type": "int64", "scope": "object"},
    {"name": "count", "type": "int64", "scope": "match"}
  ]
}
```

Attributes/Facts 的 `type` 为 `strings`、`uint64s` 或 `int64`。多值声明必须有正数
`maxValues`；int64 不接受该字段。所有 Attribute 名和 Fact 名在整个 Contract 中唯一，
Attribute 与 Fact 不能重名。

Index 规则：

- `multi_value` 的 `keyType` 为 `string` 或 `uint64`，并要求正数
  `maxDocumentValues`/`maxQueryValues`；KeyType 必须匹配 Attribute type；
- `int64_range` 只能指向 int64 Attribute，不接受 `keyType` 或多值上限；
- Index name 唯一，`field` 必须引用已声明 Attribute。

## 2. 解析入口与限制

顶层入口：

```go
schema, err := matchsystem.ParseLogicalNodeContract(contractJSON)
```

需要自定义限制时使用唯一子包入口：

```go
schema, err := contract.Parse(contractJSON, contract.DefaultLimits())
```

解析会先用 `jsonstrict` 检查 JSON bytes/depth/string/duplicate/unknown/trailing/null 等，
再由 `Contract.Validate` 校验跨条目的名字、类型、scope、索引字段和上限。错误包含
`Phase`、`Path`、`Code`、`Err`；不要依赖错误文本做兼容判断。

`Contract` 在进入 Prefilter/Evaluation 前会先验证并快照；`AttributeSpecs()` 和 `FactSpecs()` 是共享表达式 profile 的转换入口，索引则直接来自 `Contract.Indexes`。这些数据在进入 Prefilter/Evaluation 前都会
复制/冻结。Contract 的 limits 是外层上限，Expression JSON/Compiler 和 Prefilter index
limits 不能自行扩大它。

## 3. Fact scope

| Scope | 生成时机 | 合法读取位置 |
| --- | --- | --- |
| `tick` | 每次 `ProduceMatch` 的 Tick FactFrame | Prefilter Tick、Evaluation 各 phase 按 capability |
| `object` | Ticket 首次作为 Seed/Candidate 使用时 | Prefilter Seed、Evaluation Seed/Candidate |
| `match` | Seed 初始化或 Candidate Join 后 | Evaluation Join/OnJoin/Complete |

Fact 名全局唯一，不能以 scope 重载同名字段。运行时还检查值的 type、MaxValues、层内
map 唯一性和 scope；不存在空 scope 通配或“先读 Seed 再读 Tick”的 fallback。

## 4. 与 Expression 的关系

Contract 不定义表达式 Kind；它提供 expression Compiler 的 namespace 和 source 约束：

```text
contract.Contract
  -> expression.CompileProfile.Attributes/Facts/FactAllowed
  -> expression.Compiler
```

Expression Root 仍必须显式声明 `ResultType`。Prefilter 为 Bitmap Root 注册 index
DomainLeaf；Evaluation 为 Bool/value Root 注册 phase profile。合法 Kind、child result、
cycle、limits、canonical 和 Program 只有 expression 一个实现。DomainLeaf 的动态 operand
也由这个 Compiler 编译到所属 Program 的 typed InstructionID；领域包只消费该 handle，
不创建子 Program 或遍历通用 AST。

## 5. Prefilter 适配

Typed 入口：

```go
arena := expression.NewArena()
builder := prefilter.NewBuilder(arena)
values := arena.StringsLiteral("ranked")
leaf := builder.LookupString("mode_index", values)
config := prefilter.Config{
    Arena: arena,
    Root:  builder.Root(leaf), // ResultBitmap
}
plan, err := prefilter.Compile(config, schema)
```

JSON 入口：

```go
compiler, err := prefilter.NewJSONCompiler(schema)
plan, err := compiler.Compile(prefilterJSON)
```

Prefilter 只允许 object Fact 映射到 `SourceSeedFacts`、tick Fact 映射到
`SourceTickFacts`，拒绝 match Fact；索引字段和 key 上限由同一 Contract 校验。

## 6. Evaluation 适配

Evaluation 用同一 Contract 编译 shared Arena：

```go
options := matchsystem.EvaluationCompileOptions{Scorers: scorers}
config, err := matchsystem.ParseEvaluationJSONWithDefaults(evaluationJSON, options)
if err != nil {
    return err
}
plan, err := matchsystem.CompileEvaluation(config, schema, options)
```

`ParseEvaluationJSONWithDefaults` 内部复用 `expression.DecodeRootInto`，只完成 JSON shape
和 Root；`CompileEvaluation` 再以真实 Contract 检查 Fact 名/type/scope、phase capability
和 scorer。`CompileEvaluationJSON` 只是这两步的 facade，不是另一套 parser/compiler。

## 7. Breaking 迁移与 fingerprint

这是一次 breaking migration：Contract 只有 v2，source 必须显式绑定，schema 不再分叉。
Prefilter fingerprint 为 `prefilter-fingerprint/v4`，会记录 Program 实际
依赖的 Attribute/Facts、索引 requirements、Domain sidecar canonical 和运行参数。旧
fingerprint、旧 Contract 或旧 root JSON 不能用于新 generation 的 no-op；升级时清理缓存并
重新编译。

依赖方向：

```text
contract -> expression / fact
prefilter -> contract / expression / fact
evaluation -> contract / expression / fact
matchsystem -> contract / prefilter / evaluation
```

Contract 不导入 Prefilter；物理 index slot、Roaring、scorer、事务和 Match 生命周期仍由
领域包拥有。

## 8. 常见错误

`UNKNOWN_FIELD`、`DUPLICATE_NAME`、`INVALID_FACT_SCOPE`、`MISSING_ATTRIBUTE`、
`ATTRIBUTE_TYPE_MISMATCH`、`INVALID_INDEX_FIELD` 和 `INVALID_KEY_LIMIT` 属于 Contract
边界错误。表达式中的 unknown op、root result、source capability、cycle 和 Domain leaf
错误由 Expression/Evaluation/Prefilter 在各自 boundary 保留 Path 并包装。

Fact 值的所有权、Frame、Provider 和缓存见 [Fact 生命周期](fact-lifecycle.md)；匹配轮次
和 Ticket/DocID 见 [Ticket 生命周期](ticket-lifecycle.md)。
