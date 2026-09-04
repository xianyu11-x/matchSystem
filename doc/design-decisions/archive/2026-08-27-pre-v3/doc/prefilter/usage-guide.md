# Prefilter 使用指南

Prefilter 是索引驱动的候选初筛。表达式结构、值类型、JSON 语法和编译合法性由共享
`internal/matchsystem/expression` 拥有；Prefilter 只提供三种 bitmap DomainLeaf、索引
Contract 绑定、query sidecar 和 Roaring 执行。

## 1. 依赖与入口

```go
import (
    "matchSystem/internal/matchsystem/contract"
    "matchSystem/internal/matchsystem/expression"
    "matchSystem/internal/matchsystem/fact"
    "matchSystem/internal/matchsystem/prefilter"
)
```

必须先准备同一份 `contract.Contract`：

```go
schema := contract.Contract{
    Attributes: []contract.AttributeSpec{
        {Name: "mode", Type: fact.TypeStrings, MaxValues: 8},
        {Name: "rating", Type: fact.TypeInt64},
    },
    Facts: []contract.FactSpec{
        {Name: "capacity", Type: fact.TypeInt64, Scope: fact.ScopeTick},
        {Name: "tier", Type: fact.TypeInt64, Scope: fact.ScopeObject},
    },
    Indexes: []contract.IndexSpec{
        {
            Type: contract.IndexTypeMultiValue, Name: "mode_index", Field: "mode",
            KeyType: contract.KeyTypeString, MaxDocumentValues: 8, MaxQueryValues: 8,
        },
        {
            Type: contract.IndexTypeInt64Range, Name: "rating_index", Field: "rating",
        },
    },
}
```

`schema.Validate()` 会检查属性/Facts/index 的全局唯一性、类型、scope、maxValues 和
索引字段。Prefilter 不再有自己的 Attribute/Fact/Index Contract。

## 2. Typed Builder 与显式 Bitmap Root

`prefilter.NewBuilder(arena)` 只创建 Prefilter domain leaves；所有值表达式和 Bitmap
结构由同一座 `expression.Arena` 创建：

```go
arena := expression.NewArena()
builder := prefilter.NewBuilder(arena)

// string multi-value query：values 必须是 ResultStrings 节点
seedModes := arena.StringsLookup(expression.SourceSeedAttributes, "mode")
modeLeaf := builder.LookupString("mode_index", seedModes)

// int64 range query：边界必须是 ResultInt64 节点
min := arena.Int64Literal(100)
max := arena.Int64Lookup(expression.SourceSeedAttributes, "rating")
ratingLeaf := builder.LookupRange("rating_index", min, max)

positive := arena.BitmapAnd(modeLeaf, ratingLeaf)
root := builder.Root(positive) // Root.Result 明确为 ResultBitmap

plan, err := prefilter.Compile(prefilter.Config{
    Arena: arena,
    Root:  root,
}, schema)
if err != nil {
    return err
}
```

Builder 的叶子 API：

| 方法 | 叶子 | 动态值结果 |
| --- | --- | --- |
| `LookupString(index, values)` | string multi-value index | `ResultStrings` |
| `LookupUint64(index, values)` | uint64 multi-value index | `ResultUint64s` |
| `LookupRange(index, min, max)` | int64 闭区间 index | 两个 `ResultInt64` |

静态值用 `arena.StringsLiteral(...)`、`arena.Uint64sLiteral(...)`、
`arena.Int64Literal(...)`；动态值可以是 lookup、union、step、clamp、add/sub/min/max
等 shared expression 节点。它们会与所属 Bitmap Root 一起编译进同一个
`expression.Program`；sidecar 只保存 typed InstructionID，不创建 operand 子 Program。
源名称和 Fact scope 在 Compile 时以真实 Contract 校验。

### 2.1 Bitmap 结构

```go
andNode := arena.BitmapAnd(modeLeaf, ratingLeaf)
orNode := arena.BitmapOr(modeLeaf, ratingLeaf)
notNode := arena.BitmapExclude(ratingLeaf)
noneNode := arena.BitmapNone()
conditional := arena.BitmapIf(
    arena.GreaterOrEqualInt64(
        arena.Int64Lookup(expression.SourceTickFacts, "capacity"),
        arena.Int64Literal(1),
    ),
    andNode,
    noneNode,
)
root := builder.Root(conditional)
```

`BitmapExclude` 必须运行在已有正向 scope 上；根为单独 Exclude、或某个分支无法建立
scope，Compile/执行都会拒绝。`BitmapIf` 只执行选中的 then/else 分支，未选分支不会绑定
动态 query 或读取 Fact。`BitmapNone` 是合法的静态空结果，不是错误。

## 3. 编译结果与 Requirements

`prefilter.Compile` 的核心流程是：

```text
Contract.Validate
  -> register index names/slots
  -> build expression.StrictProfile(ResultBitmap)
  -> register Prefilter DomainLeafKindSpec + BitmapLeafCompiler
  -> expression.Compiler.Compile(Arena, Root)
  -> collect query sidecars, dependencies, Requirements
  -> canonical + prefilter-fingerprint/v4
  -> immutable Plan
```

```go
type prefilter.Plan struct {
    // 通过方法读取
}

plan.Program()          // shared *expression.Program
plan.Requirements()     // 实际使用的 Index/Fact/Attribute
plan.Fingerprint()      // Fingerprint
```

`Requirements` 只包含 Root 实际引用的 index、Fact 和 Attribute；index requirement 包含
字段、实现类型、KeyType 以及文档/查询 key 上限。Fingerprint 包含 Program canonical、
动态值依赖、实际 Requirements 和 `ContainsProbeThreshold`；And/Or/集合 canonical 会按
确定性顺序归一化。

fingerprint schema 是 `prefilter-fingerprint/v4`。本次 breaking change 使旧 fingerprint 全部失效；旧 fingerprint、旧 canonical 或旧 Contract
产生的缓存不能做 no-op 比较，升级时必须清理并重建。

## 4. IndexStore 生命周期

```go
store, err := prefilter.New(plan)
if err != nil {
    return err
}

if err := store.Add(docID, ticket); err != nil {
    return err
}
// Remove(docID) 会同步清理 Active 与所有 posting。

session, err := store.BeginTick(prefilter.Facts{
    Int64Values: map[string]int64{"capacity": 10},
})
if err != nil {
    return err
}
set, err := session.Candidates(seedDocID, seedTicket, seedFacts)
```

`Add` 校验非零 DocID、Ticket、属性上限和每个索引后才写入；Store 不保存第二套
Document。`BeginTick` 校验 tick Fact、准备 range index 并借用一层只读 Tick Facts。
`Candidates` 还会校验 seed Ticket、Active DocID、object Fact 与 Tick Fact scope；返回
调用方拥有的 `*prefilter.DocSet`。

IndexStore/TickSession 没有锁，必须由同一个 LogicalNode owner goroutine 串行调用
Add、Remove、BeginTick 和 Candidates。TickSession 不得跨 mutation barrier 使用。

## 5. 执行策略

- 小 scope（默认 `ContainsProbeThreshold=4096`）逐 DocID 调用 index `contains`。
- 大 scope 直接 `lookup` posting，再与当前 scope 做 Bitmap AND。
- And 估算可 anchor 子树，优先执行最小候选集；空 accumulator 立即停止。
- Or 合并分支；已有 scope 且 union 已覆盖 scope 时短路。
- Exclude 克隆 scope 后执行 AND-NOT；无 scope 直接报错。
- If 只执行选中分支。

Prefilter 只返回候选 DocID，不读取完整 Ticket、不评分、不执行 Join/Complete，也不在
索引缺失时扫描全池 Ticket。

## 6. JSON 计划

```json
{
  "schemaVersion": "prefilter/v2",
  "plan": {
    "resultType": "bitmap",
    "expr": {
      "op": "bitmap_and",
      "children": [
        {
          "op": "domain_call",
          "tag": "prefilter",
          "kind": "prefilter.lookup.string",
          "resultType": "bitmap",
          "fields": {"index": "mode_index", "values": ["ranked"]}
        },
        {
          "op": "domain_call",
          "tag": "prefilter",
          "kind": "prefilter.lookup.int64_range",
          "resultType": "bitmap",
          "fields": {"index": "rating_index", "min": 100, "max": 200}
        }
      ]
    }
  },
  "runtime": {"containsProbeThreshold": 4096}
}
```

```go
compiler, err := prefilter.NewJSONCompiler(schema)
if err != nil {
    return err
}
plan, err := compiler.Compile(raw)
```

`NewJSONCompiler` 固化一份 Contract；`Parse` 严格校验 `prefilter/v2` envelope，然后把
`plan` 交给 shared `expression.DecodeRootInto`，使用 `DomainDescriptors()` 解析三种
Prefilter leaf。它不创建第二个 expression tree，也不提前用临时 Contract 解析名称。
`Compile` 再以同一份真实 Contract 完成索引绑定、query sidecar、Requirements 和
Fingerprint。

JSON expression 的 operator、child result 和 `domain_call.resultType` 必须和 typed
Builder 产生的 Root 完全一致；JSON/typed 等价计划应得到相同 canonical 和 fingerprint。
`prefilter/v1` 不属于当前输入，加载时返回 `UNKNOWN_SCHEMA_VERSION`，不会回退到旧格式。

## 7. 错误与限制

Prefilter 错误是 `Error{Phase, Path, Code, Err}`：

- `json`：JSON 语法、未知字段、重复 key、尾随值、root/resultType、资源限制；
- `compile`：Contract/index/Fact/type/capability、非法 Kind、cycle、空节点、无 anchor；
- `evaluate`：seed/tick Fact 不满足 Contract、动态 query 超过 MaxQueryValues、范围
  Min 大于 Max、inactive DocID 或 IndexStore 生命周期错误。

错误不会自动改为 `BitmapNone`，不会切换 Fact source，不会触发 Universe 或全池扫描。

## 8. 与 Evaluation 的边界

Prefilter 和 Evaluation 共享 `expression.Arena`/`Root`/`Program` 的编译语义，但不共享
runtime：

```text
expression.Compiler
  ├─ Prefilter: Bitmap leaf handle -> query sidecar -> Roaring executor
  └─ Evaluation: primitive Lookup -> scorer/Match Fact/Join runtime
```

Roaring、索引 posting、scorer callback、TicketStore 和事务提交不能放入 Expression Core；
Prefilter/Evaluation 后续只需要各自增加新的叶子描述/编译器和编排规则。
### JSON 动态 Query Operand

`values`、`min`、`max` 如果对应的 `LeafFieldSpec.DynamicResult` 已声明，
可以使用共享表达式 Root envelope；其 `resultType` 必须与字段声明一致：

```json
{
  "resultType": "strings",
  "expr": {
    "op": "strings_union",
    "items": [
      {"op": "strings_ref", "source": "seed_attributes", "name": "mode"},
      {"op": "strings_ref", "source": "seed_facts", "name": "labels"}
    ]
  }
}
```

Prefilter JSON 只接受 `prefilter/v2` envelope。动态子表达式由
`expression.DecodeRootInto` 放入同一 Arena，并由同一个 shared Compiler 编译
进所属 Bitmap Program；Prefilter 只在运行时通过该 Program 的
`EvaluateStringsAt`、`EvaluateUint64sAt` 或 `EvaluateInt64At` 绑定 Query。
