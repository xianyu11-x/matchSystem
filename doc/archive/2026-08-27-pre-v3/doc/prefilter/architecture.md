# Prefilter 当前实现架构

本文描述 `internal/matchsystem/prefilter` 当前实现，以及它与 shared Expression Core、
Contract、Evaluation 的边界。

相关文档：[Expression Core](../expression-core.md)、[共享 Contract](../logical-node-contract-v2.md)、
[使用指南](usage-guide.md)、[代码索引](code-reference.md)、[索引初筛设计](../index-prefiltering.md)、
[JSON/热更新边界](../json-prefilter-hot-reload.md)。

## 1. 职责边界

Prefilter 负责：

- 把 `expression.ResultBitmap` Root 编译成共享 `expression.Program`；
- 注册 `prefilter.lookup.string`、`prefilter.lookup.uint64`、
  `prefilter.lookup.int64_range` 三种 DomainLeaf；
- 将 leaf 绑定到共享 Contract 的 index name/slot；
  - 保存动态 query 对共享 Program 的 typed InstructionID、静态 query、Requirements、canonical 和 fingerprint；
- 在 `IndexStore`/`TickSession` 中执行 Roaring Bitmap 的 estimate、lookup、contains、
  AND/OR/AND-NOT；
- 校验 Seed/Tick Fact 层并返回 DocID `DocSet`。

Prefilter 不负责 Ticket 生命周期、Seed 调度、scorer、Top-L、Join/Complete、Match Fact
事务或配置发布服务，也不在缺少索引时扫描 Ticket。

## 2. 分层图

```text
logical-node-contract/v2 + raw prefilter/v2
                  │
                  ▼
prefilter.NewBuilder / NewJSONCompiler
                  │  typed Arena Root{ResultBitmap}
                  ▼
prefilter.Compile
  ├─ contract validation + index slots
  ├─ expression.StrictProfile(ResultBitmap)
  ├─ DomainLeafKindSpec + BitmapLeafCompiler
  └─ expression.Compiler -> immutable Program
                  │
                  ├─ Program.BitmapInstructions
                  ├─ compiledIndexQuery sidecars
                  ├─ Requirements / Fingerprint
                  ▼
IndexStore (Active + physical indexes)
                  │
                  ▼
TickSession.Candidates -> DocSet
```

Expression 负责所有结构/类型/合法节点检查；Prefilter 只实现 leaf compiler、物理绑定和
Bitmap executor。`Program` 不保留 Arena，也不携带 Roaring 对象。

## 3. Typed 声明层

### 3.1 Shared Arena + Builder

`Builder` 的状态只有一座 `expression.Arena`：

```go
arena := expression.NewArena()
builder := prefilter.NewBuilder(arena)
values := arena.StringsLookup(expression.SourceSeedAttributes, "mode")
leaf := builder.LookupString("mode_index", values)
root := builder.Root(leaf)
```

Builder 不提供 `And`、`Or`、`If`、`Exclude`、literal 或 lookup value 的第二套方法；
这些全部使用 `expression.Arena`。这样 Bitmap 根、Bool condition、动态边界和领域叶子
永远位于同一 closed Arena，Root 也明确标注 `ResultBitmap`。

### 3.2 三种 DomainLeaf

| Kind | Contract index | 字段 | 结果 |
| --- | --- | --- | --- |
| `prefilter.lookup.string` | `multi_value + keyType=string` | `index:string`、`values:strings` | Bitmap |
| `prefilter.lookup.uint64` | `multi_value + keyType=uint64` | `index:string`、`values:uint64s` | Bitmap |
| `prefilter.lookup.int64_range` | `int64_range` | `index:string`、`min/max:int64` | Bitmap |

Typed Builder 的动态字段使用 `LeafField.Node` 绑定 `NodeRef`；JSON descriptor 使用静态
literal 集合/数值。两者进入同一 `DomainLeafKindSpec` 校验和同一 `compileBitmapLeaf`。

## 4. 编译层

`prefilter.Compile(config, schema)` 的关键步骤：

1. `Contract.Validate`，复制 Contract 并建立 index name/slot 表。
2. 由 `StrictProfile(ResultBitmap)` 建立内建闭合；只合并调用方要求的更严格 limits/kinds。
3. 注入 Seed Attributes、Seed/Object Facts、Tick Facts 的 source/capability 和
   `FactAllowed`；Candidate/Match source 不会暴露给 Prefilter。
4. 注入三种 `DomainLeafKindSpec` 及 `BitmapLeafFunc(state.compileBitmapLeaf)`。
5. 调用 shared `expression.Compiler`，校验 Bitmap/Bool/value 子节点、cycle、类型和
   limits。
6. 每个 DomainLeaf 解析 index、动态 query 的 typed InstructionID 或静态值，产生 opaque
   handle 和 `LeafProperties`；sidecar 顺序就是 handle 的 O(1) lookup 表。动态 operand
   已经在同一轮由 shared Compiler 编译进所属 Program，Prefilter 不创建子 Program。
7. 收集 Program dependencies、实际 Requirements，生成 canonical 与
   `prefilter-fingerprint/v4`。

Compiler 不遍历 Ticket，不创建 Roaring，也不在 compile 阶段读取动态 Seed/Tick 值；只有
完全静态 query 才会在 sidecar 生成时预计算为空/非空。

## 5. Leaf sidecar

每个 Bitmap DomainLeaf 对应一个 `compiledIndexQuery`：

```text
LeafHandle
   │
   ▼
compiledIndexQuery
  ├─ index slot / key kind / max query keys
  ├─ static string/uint64/range values
  ├─ dynamic values: shared Program InstructionID
  ├─ canonical
  └─ BitmapProperties
```

sidecar 只保存 index 查询信息，不保存 Bitmap 或子 Program。运行时按当前 Seed、Seed Facts、
Tick Facts，通过同一 shared Program 的 typed `Evaluate*At(InstructionID, ...)` 读取动态值，
检查 MaxQueryValues 或 Min<=Max，然后调用物理 index。静态 query
不经过通用 Value evaluator 热路径。

## 6. Bitmap executor

`IndexStore` 为每个 Contract index 创建一个物理实现：

- string MultiValue：`map[string]*roaring.Bitmap` posting 和每 DocID 的 key 列表；
- uint64 MultiValue：原生 uint64 posting 和每 DocID 的 key 列表；
- Int64Range：value posting、DocID value 和排序后的 distinct values。

`TickSession.evalBitmap` 消费 `Program.BitmapInstructions` 的结构视图：

```text
BitmapNone       -> empty bitmap
DomainLeaf       -> bind sidecar -> contains/lookup
BitmapAnd        -> estimate 最小 anchor -> AND
BitmapOr         -> branch OR，可在覆盖 scope 时短路
BitmapExclude    -> clone(scope) AND-NOT child
BitmapIf         -> eval Bool condition -> only selected branch
```

`BitmapProperties` 提供 `StaticNone`、`ScopeFree`、`NeedsScope`、`EstablishesScope` 状态，
用于 Compile 和 runtime 检查 Exclude/anchor。Prefilter 自己拥有 Roaring 运算，Expression
只输出结构和属性。

## 7. Fact 与 source

Prefilter 的 profile 只允许：

| Source | 运行时输入 | Contract scope |
| --- | --- | --- |
| `SourceSeedAttributes` | Seed `common.Ticket` | Contract Attribute |
| `SourceSeedFacts` | 当前 Seed 的 Object Facts | `fact.ScopeObject` |
| `SourceTickFacts` | 本 Tick 借用 Facts | `fact.ScopeTick` |

Candidate Attributes/Facts、Match Facts 和 `fact.ScopeMatch` 在 Prefilter compile profile 中
被拒绝。不存在按值是否存在而改变 scope 的 fallback；缺失值是结构化 evaluate error。

`BeginTick` 校验 Tick Facts 并 prepare range index；`Candidates` 校验 Seed Ticket、Seed
Facts 和 Tick/Seed 重名，再执行同一 TickSession 中的根。

## 8. IndexStore 与 owner 边界

```text
owner goroutine
  Add/Remove (mutation barrier)
      ↓
  BeginTick(tickFacts)
      ↓
  Candidates(seedDocID, seedTicket, seedFacts) × N
      ↓
  session ends
      ↓
  next Add/Remove
```

IndexStore、TickSession 和 DocSet 不提供并发保护。LogicalNode 必须串行驱动所有 mutation
和查询；TickSession 只读借用 Fact maps/slices，不得跨 mutation barrier。Prefilter 不负责
FactFrame 的深拷贝，外层 LogicalNode/fact 包负责生命周期和所有权。

## 9. JSON 编译路径

`NewJSONCompiler(contract, limits)` 固化 Contract 和 limits；`Parse` 严格验证
`prefilter/v2` envelope 后调用 shared `expression.DecodeRootInto`：

```text
raw bytes
  -> jsonstrict bounds/unknown/duplicate/trailing checks
  -> DecodeRootInto(shared Arena, CompileProfile, DomainDescriptors)
  -> Config{Arena, Root{ResultBitmap}}
  -> Compile(Config, Contract)
```

`DomainDescriptors()` 只描述 JSON 字段如何成为 typed `DomainLeaf`，不解析 index slot；
真实索引绑定仍在 `Compile` 完成。Typed Builder 与 JSON 同一 canonical 应产生同一
Program/Requirements/Fingerprint。`prefilter/v1` 会被 `UNKNOWN_SCHEMA_VERSION` 拒绝，不
进入旧 parser 或兼容执行路径。

## 10. 性能与错误不变量

- 查询不扫描全池 Ticket；所有候选来自 Active posting 和 Bitmap 运算。
- 小 scope 使用 `contains`，大 scope 使用 posting lookup；threshold 默认 4096。
- Int64Range 按 distinct keys 二分，不按数值宽度逐整数扫描。
- 编译期错误和运行期错误都保留 `Phase/Path/Code/Err`，错误不会伪装成 None/Universe。
- Plan、Program、sidecar、Requirements 和 Contract snapshot 编译后不可变。

Prefilter 的下一步扩展是新增叶子 schema/compiler 或新的物理 index；不应再新增表达式
AST、通用 value evaluator 或第二份 JSON parser。Roaring、scorer、事务和 Match 提交仍
明确属于各自 runtime。
