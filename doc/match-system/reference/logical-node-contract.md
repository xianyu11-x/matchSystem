# `logical-node-contract/v3`

这是 RuleJSON 中 `contract` section 的唯一业务 Contract。`match-rule/v1` 的生产配置
由 `CompileRuleJSON` 统一接收；创建 LogicalNode 时解析、校验并冻结，再把同一份 schema
交给 Prefilter、Evaluation 和 Fact Provider 相关边界使用。

## JSON 形状

```json
{
  "schemaVersion": "logical-node-contract/v3",
  "attributes": [
    {"name": "mode", "type": "strings", "maxValues": 4},
    {"name": "rating", "type": "int64"}
  ],
  "facts": [
    {"name": "capacity", "type": "int64", "scope": "tick"},
    {"name": "tier", "type": "int64", "scope": "object"},
    {"name": "count", "type": "int64", "scope": "match"}
  ],
  "indexes": [
    {"type": "multi_value", "name": "mode",
     "keyType": "string", "maxDocumentValues": 4, "maxQueryValues": 4},
    {"type": "int64_range", "name": "rating"}
  ]
}
```

顶层必须有 `schemaVersion`、`attributes`、`facts`、`indexes`；`limits` 可选。未知
字段、重复键、重复名称、尾随 JSON、null、非法 UTF-8 和结构类型错误都会被拒绝。

## Attribute、Fact 和类型

可用值类型只有：

- `strings`：字符串集合，必须声明正数 `maxValues`；
- `uint64s`：无符号整数集合，必须声明正数 `maxValues`；
- `int64`：单值整数，不得声明 `maxValues`。

Attribute 名和 Fact 名在整个 Contract 中全局唯一；同一个名字不能在两类声明中重复。
Fact scope 的生命周期如下：

| scope | 生成位置 | 可被哪些阶段读取 |
| --- | --- | --- |
| `tick` | 一次 `ProduceMatch` 的 Tick `FactProvider` | Prefilter、`CanJoin`、`CanComplete` |
| `object` | 声明 Object Fact 时，Ticket 第一次作为 seed/candidate 使用由 `ObjectFactProvider` 通过 Writer 刷新 per-Ticket slot（每 generation 至多一次）；空声明不建 slot | Prefilter 的 seed、Scorer、`CanJoin` 的 seed/candidate |
| `match` | `MatchFactProvider.Initialize/OnJoin` | `CanJoin`、`CanComplete` |

具体读取权限由 [标量表达式](expression-scalar.md) 的 profile 再次收紧；scope
不会因为表达式写法而自动转换或 fallback。

## 索引

`indexes` 中的每项使用已声明的 Attribute 名作为唯一 `name`。这个名称同时是
Attribute 名、物理索引标识和 Prefilter 查询中的 `index` 引用；因此同一 Attribute
最多有一个索引，也不存在额外的 `field` 字段。

`multi_value` 的 `keyType` 只能是 `string` 或 `uint64`，且必须分别匹配
`strings` 或 `uint64s` Attribute；`maxDocumentValues` 和 `maxQueryValues` 都必须
为正数。`int64_range` 只能引用 `int64` Attribute，并且不接受 `keyType`、多值上限。

Prefilter 编译时会再次确认每个查询与索引的类型和上限一致；没有声明索引就不能通过
名称使用它。

## limits

Contract 的默认外层边界为：

| 字段 | 默认值 |
| --- | ---: |
| `maxBytes` | 1 MiB |
| `maxDepth` | 64 |
| `maxChildren` | 128 |
| `maxStringBytes` | 1024 |
| `maxIndexes` | 128 |
| `maxAttributes` | 256 |
| `maxFacts` | 256 |
| `maxValues` | 10000 |
| `maxDocumentValues` | 256 |
| `maxQueryValues` | 256 |

显式非零值覆盖默认值，负数无效。Contract 的结构边界会约束下游编译；下游调用方
可以进一步收紧表达式 JSON 限制，但不能放宽 Contract 的公共边界。

## API 与错误

生产入口是：

```go
compiled, err := matchsystem.CompileRuleJSON(ruleJSON)
schema := compiled.Contract()
```

统一编译器内部使用 `contract.Parse(data, contract.DefaultLimits())`。实现见
[contract/contract.go](../../../internal/matchsystem/contract/contract.go) 和
[RuleJSON 编译](../../../internal/matchsystem/rule_config.go)。

错误保留 `{Phase, Path, Code, Err}` 结构；调用方应按结构化 `Code` 判断，不依赖错误
文本。Contract 通过后，调用方仍不能修改其切片作为运行时配置；Prefilter/Evaluation
会各自保存防御性快照。
