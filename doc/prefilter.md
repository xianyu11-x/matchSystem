# `prefilter/v3`

Prefilter 把 seed 的查询条件编译为索引驱动的 Bitmap 计划。它拥有私有 Bitmap expression、
查询 sidecar、索引 slot 和 Roaring 执行；scalar operand 交给共享的
`expression-scalar/v3` compiler，并以不透明 Program 保存。Prefilter 不决定 Match
是否成立，也不提供候选 Ticket 扫描兜底。

## JSON envelope

```json
{
  "schemaVersion": "prefilter/v3",
  "bitmap": {
    "resultType": "bitmap",
    "expr": {
      "op": "lookup_string",
      "index": "mode",
      "values": {
        "schemaVersion": "expression-scalar/v3",
        "resultType": "strings",
        "expr": {"op": "strings_ref", "source": "seed_attributes", "name": "mode"}
      }
    }
  },
  "runtime": {"containsProbeThreshold": 4096}
}
```

顶层只接受 `schemaVersion`、`bitmap` 和可选 `runtime`；Bitmap 对象必须声明
`resultType: "bitmap"` 与 `expr`。`runtime.containsProbeThreshold` 为零或省略时
使用 4096，表示小候选 scope 可以逐个调用索引 contains；它不改变逻辑结果，但属于
计划身份的一部分。

## Bitmap 节点

Bitmap expression 的合法节点只有：

| 节点 | 结构与约束 |
| --- | --- |
| `none` | 空候选集；可作为静态空 root |
| `and` / `or` | `children` 为至少一个 Bitmap 节点 |
| `exclude` | `value` 为 scope-free Bitmap；不能嵌套另一个 exclude，执行时必须有正向 scope |
| `if` | `when` 是 scalar Bool root，`then`/`else` 都是 Bitmap 节点 |
| `lookup_string` | `index` + `values`，values 必须是 `strings` scalar root |
| `lookup_uint64` | `index` + `values`，values 必须是 `uint64s` scalar root |
| `lookup_range` | `index` + `min`/`max`，边界必须是 `int64` scalar root |

除 `none` 外，root 必须既能从空 scope 开始，又能建立正向候选 scope；因此单独的
`exclude`、不能建立 anchor 的组合或非法 scope lattice 会在编译期拒绝。所有
Bitmap 子数组、嵌套深度、scalar operand 和指令预算共享一次有效限制快照。

## 索引与 source 限制

查询必须引用 Contract 中已声明的索引，并严格匹配类型。Contract 索引的 `name`
同时就是被索引的 Attribute 名和查询中的 `index` 引用：

- `lookup_string` 只能使用 `multi_value` + `keyType: string`，查询集合由
  `strings` scalar 产生；
- `lookup_uint64` 只能使用 `multi_value` + `keyType: uint64`，查询集合由
  `uint64s` scalar 产生；
- `lookup_range` 只能使用 `int64_range`，`min`/`max` 由 `int64` scalar 产生。

每个查询集合的可证明上限和静态值数量都不能超过索引的 `maxQueryValues`。静态空
集合会在编译结果中保留为空查询，不需要运行时 lookup。

Prefilter scalar profile 只允许读取：

| source | 含义 |
| --- | --- |
| `seed_attributes` | 当前 seed Ticket 的 Attribute |
| `seed_facts` | 当前 seed 的 object Fact |
| `tick_facts` | 本次 `ProduceMatch` 的 Tick Fact |

candidate 属性、candidate Fact、Match Fact 和 Match 成员不属于 Prefilter 输入。
类型、scope、名称和 Fact 值由 [共享 Contract](logical-node-contract.md) 校验。

## 编译与执行

```go
compiler, err := prefilter.NewJSONCompiler(schema)
if err != nil { return err }
plan, err := compiler.Compile(prefilterJSON)
if err != nil { return err }

store, err := prefilter.New(plan)
if err != nil { return err }
if err := store.Add(docID, ticket); err != nil { return err }
session, err := store.BeginTick(tickFacts)
candidates, err := session.Candidates(seedDocID, seed, seedFacts)
```

`Plan` 在编译后不可变；`IndexStore`、`TickSession`、`DocSet` 的 Add/Remove/执行由
所属 LogicalNode 的单一 owner 串行调用，包内不提供锁或并发快照。Add 会校验并 clone
Ticket，BeginTick 会校验 Tick Fact；Candidates 会校验 seed、seed Fact 及 scope。

实现入口：[JSON facade](../internal/matchsystem/prefilter/json.go)、
[compiler](../internal/matchsystem/prefilter/compiler.go)、
[私有 expression](../internal/matchsystem/prefilter/expression.go)、
[Plan/fingerprint](../internal/matchsystem/prefilter/plan.go)、
[IndexStore/TickSession](../internal/matchsystem/prefilter/store.go)。

## Fingerprint

`Plan.Fingerprint()` 返回 `prefilter-fingerprint/v5` 的 SHA-256 十六进制身份。输入
包括规范化 Bitmap 结构、实际查询 sidecar、索引和 Fact/Attribute 依赖、有效限制、
Contract 身份、schema 版本和 probe 参数；JSON 空白、字段顺序和声明顺序不改变它。
运行时 Tick/Candidate/Match Fact 值、Provider 实现、CandidateScorer 和当前候选集合
不进入 fingerprint。

当前核心只提供 immutable compile Plan 和这个 fingerprint；发布、缓存、版本绑定和
回滚由上层负责，详见 [发布与验证](release-validation.md)。
