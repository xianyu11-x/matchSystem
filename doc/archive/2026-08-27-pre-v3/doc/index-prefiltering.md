# 索引初筛设计

Prefilter 的目标是用声明式索引计划快速构造候选 DocID 集，再把有限候选交给评分和
Evaluation。它不是最终正确性判断，也不扫描完整 Ticket 作为索引缺失时的 fallback。

## 1. 统一表达式入口

索引树是 shared `expression.Arena` 中的 `ResultBitmap` Root：

```go
arena := expression.NewArena()
builder := prefilter.NewBuilder(arena)
seedModes := arena.StringsLookup(expression.SourceSeedAttributes, "mode")
lookup := builder.LookupString("mode_index", seedModes)
root := builder.Root(arena.BitmapAnd(lookup, arena.BitmapNone()))
```

Expression Core 统一规定合法 Kind、child result、cycle、source capability、动态值和
limits；Prefilter 只注册 DomainLeaf 并把它们绑定到 Contract index。Root 必须显式
`ResultBitmap`，不能把 Bool Root 当作 Bitmap。

## 2. Contract 与索引

唯一 `logical-node-contract/v2` 同时声明 attributes、indexes 和三类 Facts：

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
    {"name": "tier", "type": "int64", "scope": "object"}
  ]
}
```

`multi_value` 的 KeyType 必须匹配属性类型；`int64_range` 只能指向 int64 属性。索引
field、文档 key 上限、查询 key 上限及 Attribute/Fact type 均在 compile 时检查，Prefilter
不接收一份私有 schema。

## 3. DomainLeaf 与 Query

当前三种 Prefilter leaf：

| Leaf | 物理索引 | Query values |
| --- | --- | --- |
| `prefilter.lookup.string` | string multi-value | `ResultStrings` |
| `prefilter.lookup.uint64` | uint64 multi-value | `ResultUint64s` |
| `prefilter.lookup.int64_range` | int64 range | 两个 `ResultInt64` |

同一个 multi-value leaf 的多个 key 固定做 OR；不同 leaf 的组合由 Bitmap And/Or 决定。
Range 是闭区间 `[min,max]`。动态值可来自 Seed Attributes、Seed Object Facts 或 Tick
Facts，source scope 由共享 Contract/phase profile 约束。

Expression 的 `DomainLeaf` 只保存闭合 typed fields 和 opaque handle；shared Compiler
把动态 operand 编译为所属 Bitmap Program 中的 typed `InstructionID`，Prefilter 再把叶子
解析为 `compiledIndexQuery` sidecar。sidecar 只保存该 handle 或完全静态 query 的内联值，
不创建 operand 子 Program，也永远不保存 Roaring Bitmap。

## 4. Bitmap 树语义

```text
BitmapAnd(children...)       posting/child 交集
BitmapOr(children...)        posting/child 并集
BitmapExclude(child)         当前正向 scope AND-NOT child
BitmapIf(when, then, else)   只执行 Bool 条件选中的分支
BitmapNone()                 静态空集
DomainLeaf                   从一个物理 index 产生/缩小 scope
```

`Exclude` 不是 Universe 补集：它必须继承已有正向 scope。Compiler 用 leaf
`BitmapProperties` 和树状态验证 anchor；执行器也会在 runtime 防御无 scope。`BitmapIf`
未选路径不绑定 query、不读取 Fact、不访问 index。

## 5. 候选域安全

对任意 Seed，所有候选 DocID 必须来自当前 IndexStore Active 域：

```text
seed DocID active
        │
        ▼
Bitmap Root (leaf postings + AND/OR/AND-NOT)
        │
        ▼
DocSet of active candidate DocIDs
        │
        ▼
remove seed -> scorer bounded Top-L -> Evaluation Join
```

Prefilter 不能引入其他 LogicalNode 的 DocID，不能把缺失索引解释为全池候选，不能读取
Ticket 全表完成同等查询。最终 Join/Complete 必须重新在 Evaluation 中检查业务条件。

## 6. 物理执行算法

### 6.1 Leaf

`compiledIndexQuery.bind` 通过所属 shared Program 的 typed `Evaluate*At` 绑定当前
Seed/Tick context，检查动态 key 数不超过 `MaxQueryValues`，Range 检查 `min <= max`。
静态 query 可在 Compile 阶段计算 canonical
和 StaticNone 属性。

### 6.2 And

无现有 scope 时按 estimate 选择最小正向子树作为 accumulator，然后把其他子树限制在
accumulator 上；任意一步为空立即停止。有现有 scope 时直接缩小 scope。

### 6.3 Or

每个分支都限制在继承 scope（若有），结果做 OR；union 已覆盖 scope 时短路。无 scope
时分支必须各自建立候选域，不能隐式使用 Active 作为补集。

### 6.4 Contains probe 与 Lookup

默认 `ContainsProbeThreshold=4096`：

```text
small scope -> each DocID -> index.contains(query, docID)
large scope -> index.lookup(query) -> Bitmap AND scope
```

Range index 对排序的 distinct value 做二分，复杂度随命中的 key 数量而不是数值范围宽度
增长。string/uint64 multi-value 查询对各 key posting 做 OR。

## 7. 生命周期与并发

```text
IndexStore.New
  -> Add/Remove candidates
  -> BeginTick(tick Facts)
  -> TickSession.Candidates(seed, seed Facts) × N
  -> session ends
  -> next mutation
```

IndexStore/TickSession 不带锁，是 LogicalNode owner goroutine 的单线程物理视图。Tick Facts
在 BeginTick 借用，Seed Facts 在一次 Candidates 借用；外层 FactFrame 负责深拷贝和缓存。不能
在 session 存活时并发 Add/Remove 或修改借用 map/slice。

## 8. Requirements 与 fingerprint

Compile 只记录 Root 实际使用的：

- index name、field、类型、KeyType、文档/查询 key 上限；
- expression 依赖的 Attribute 和 scoped Fact；
- Program canonical、query sidecar canonical、contains probe threshold。

这些内容构成 `prefilter-fingerprint/v4`。And/Or 和集合值 canonical 排序后稳定；
改变实际 Contract、动态值、leaf、组合结构或运行参数都会使 fingerprint 改变。旧代缓存
必须清除并重新构建，不能继续做跨代 no-op。

## 9. 与 Evaluation 的分工

```text
expression.Compiler
  ├─ 合法 Kind / Result / source / limits / Program
  ├─ Prefilter leaf descriptor/compiler -> sidecar
  └─ Evaluation phase profile -> scalar Program

Prefilter runtime  -> Roaring + index + DocSet
Evaluation runtime -> scorer + Fact update + Join/Complete
```

Roaring、scorer、事务和 Match 提交不能统一成 expression runtime；统一的部分是 AST/JSON
协议、类型检查、Program 和 lookup 语义。

## 10. 验收不变量

- typed Builder 与 `prefilter/v2` JSON 的等价计划共享同一 Program canonical、Requirements
  和 fingerprint；
- invalid Root result、未注册 leaf、错误 index type、越权 source、cycle、无 anchor
  和动态 key 超限都会被拒绝；
- 未选 If 分支不读取其 Fact/索引；
- 查询路径零全池 Ticket 扫描回退；
- Prefilter 只产出候选 DocID，Evaluation 负责最终正确性。
