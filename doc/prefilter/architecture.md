# Prefilter 当前实现架构

> 本文只描述 `internal/matchsystem/prefilter` 当前已经实现的结构和执行行为。

配套文档：

- [逐文件逐函数说明](code-reference.md)
- [使用指南](usage-guide.md)
- [上层索引初筛设计背景](../index-prefiltering.md)

## 1. 包的职责边界

Prefilter 是严格索引化的候选初筛子包：

```text
common.Ticket + DocID + Config
       |
       v
    Compile
       |
       v
Plan
       |
       v
    IndexStore
       |
       v
TickSession
       |
       v
Candidates(seedDocID, seedTicket, seedFacts) -> DocSet
```

它负责：

- 声明式 Lookup Expression（过滤表达式）。
- 索引和 Fact 契约校验。
- Fingerprint（计划指纹）。
- string/uint64 MultiValueIndex。
- int64 Int64RangeIndex。
- Roaring Bitmap 集合运算。
- Tick 范围的 seed、now 和 Fact 动态绑定。

它不负责：

- 保存完整业务 Ticket。
- seed 调度。
- 候选评分和 Top-L。
- 最终 Join/Start/ForceStart 正确性。
- 建组和匹配提交。
- JSON generation 的发布、版本管理或热更新。
- 缺失索引时扫描 Ticket 回退。

## 2. 内部分层

```text
┌─────────────────────────────────────────────────────┐
│ JSON 生成层                                          │
│ json_contract.go + json.go                           │
│ 独立契约 JSON + LogicalNodeContract + 计划 typed Config│
└──────────────────────┬──────────────────────────────┘
                       │ Config
┌─────────────────────────────────────────────────────┐
│ 声明层                                               │
│ expr.go + query.go + expressions.go                  │
│ uint64_expr.go + index.go + fact_adapter.go          │
└──────────────────────┬──────────────────────────────┘
                       │ Config
┌──────────────────────▼──────────────────────────────┐
│ 编译层                                               │
│ compiler.go + errors.go                              │
│ 过滤表达式校验、scope/anchor、slot、requirements、fingerprint │
└──────────────────────┬──────────────────────────────┘
                       │ Plan
┌──────────────────────▼──────────────────────────────┐
│ 存储层                                               │
│ store.go + multi_value_index.go + int64_range_index.go                │
│ Active Bitmap + posting + forward map               │
└──────────────────────┬──────────────────────────────┘
                       │ TickSession
┌──────────────────────▼──────────────────────────────┐
│ 执行层                                               │
│ store.go + docset.go                               │
│ bind、estimate、lookup/contains、AND/OR/AND-NOT     │
└─────────────────────────────────────────────────────┘
```

## 3. 数据模型

### 3.1 Ticket 与 DocID

```go
type Ticket struct {
    TicketID    uint64
    CreatedAt   int64
    StringLists map[string][]string
    Uint64Lists map[string][]uint64
    Int64Values map[string]int64
}
```

- `common.Ticket` 是项目内唯一 Ticket 定义，Prefilter 不再声明 Document 投影。
- DocID 是调用 IndexStore 时独立传入的、Store 内唯一的非零 uint32。
- StringLists 提供 string 多值索引数据。
- Uint64Lists 提供 uint64 多值索引数据。
- Int64Values 为每个字段提供单个 int64。
- TicketID 和 CreatedAt 不参与当前物理索引，Prefilter 不内建业务身份或等待时间语义。

IndexStore 不保存完整 Ticket。`Add(docID, ticket)` 后只在各物理索引中保存 posting、必要的反向记录和 Active DocID。

### 3.2 Fact

```go
type FactSpec struct {
    Name          string
    Type          FactType
    MaxValues int
}

type Facts struct {
    StringLists map[string][]string
    Uint64Lists map[string][]uint64
    Int64Values map[string]int64
}
```

`FactSpec` / `Facts` 以及 Frame、View、Provider 和通用校验都位于中立 `matchsystem/fact` 包，Prefilter 只提供兼容类型别名与 evaluate 错误适配。FactSpec 是全匹配链契约；值可以来自 Tick 级 Provider，也可以来自当前对象的 ObjectFactProvider。对象充当 Prefilter seed 时，其 Object Facts 就是当前 Candidates 调用的 Seed Facts。

`FactTypeStrings` 和 `FactTypeUint64s` 必须声明正数 MaxValues，编译器用它检查 QueryKey 契约。BeginTick 只读借用 Tick Facts；Candidates 只读引用当前 Seed Facts。LogicalNode 调用链由外层 FactFrame 持有唯一拷贝。

## 4. 过滤表达式模型

过滤表达式是封闭接口，外部只能通过构造函数创建：

```text
Expr
  ├─ Lookup(Query)
  ├─ And(children...)
  ├─ Or(children...)
  ├─ Exclude(child)
  ├─ If(condition, then, else)
  └─ None()
```

集合语义：

| 节点 | 语义 |
| --- | --- |
| Lookup | 从一个索引产生或缩小 Bitmap |
| And | 交集 |
| Or | 并集 |
| Exclude | 当前正向 scope 减去 child |
| If | 只执行 condition 选中的一条路径 |
| None | 空 Bitmap |

Exclude 不支持全局反转。它必须在 And 等节点已经建立的正向候选域上执行。

## 5. Query 与动态值

### 5.1 string MultiValue

```text
StringQuery
  -> LiteralStrings
  -> SeedStrings
  -> FactStrings
  -> UnionStrings
```

同一个 Query 的多个 key 对 posting 做 OR。

### 5.2 uint64 MultiValue

```text
Uint64Query
  -> LiteralUint64s
  -> SeedUint64s
  -> FactUint64s
  -> UnionUint64s
```

uint64 posting 使用原生 uint64 key。string Query 与 uint64 Index 不能交叉使用。

### 5.3 Int64Range

```text
Int64RangeQuery{Min, Max}
  -> LiteralInt64
  -> SeedInt64
  -> FactInt64
  -> StepInt64
  -> ClampInt64
  -> AddInt64 / SubInt64
```

范围是闭区间。`StepInt64` 对任意 int64 输入做阶梯映射，输入可以来自原始 Seed 属性或普通 Fact；`ClampInt64` 将结果钳制到动态闭区间，Add/Sub 使用饱和运算。当前 If Condition 只有 `GreaterOrEqual`。

## 6. 物理索引

### 6.1 Active

`IndexStore.active` 是当前 Owner 域内全部活动 DocID 的 Roaring Bitmap。

Active 用于拒绝重复 DocID、Remove/Len 和 active seed 校验。它不作为查询 Universe，也不复制进 TickSession。所有 posting 必须由 IndexStore.Add/Remove 与 Active 同步维护，因此 Candidates 不再执行冗余的结果 AND Active。

### 6.2 string MultiValueIndex

```text
postings  map[string]*roaring.Bitmap
keysByDoc map[uint32][]string
```

- Add 对文档 key 排序去重。
- 一个文档可进入多个 posting。
- keysByDoc 使 Remove 不需要扫描全部 key。
- Lookup 对 query keys 的 posting 做 OR。

### 6.3 uint64 MultiValueIndex

```text
postings  map[uint64]*roaring.Bitmap
keysByDoc map[uint32][]uint64
```

算法与 string 版本一致，但键类型原生为 uint64。

### 6.4 Int64RangeIndex

```text
postingsByValue map[int64]*roaring.Bitmap
valueByDoc      map[uint32]int64
sortedValues    []int64
valuesDirty     bool
```

- postingsByValue 保存 exact value posting。
- valueByDoc 支持 Remove 和单 DocID Contains。
- sortedValues 只保存 distinct values，并在 BeginTick 的 prepare 阶段按需重建排序。
- rangeKeys 用两次二分查找定位闭区间。

范围查询不会从 min 逐整数扫描到 max。

## 7. Compile 流程

```text
Config
  -> validate Root
  -> normalize defaults
  -> register indexes and slots
  -> register Fact definitions
  -> compileExpr DFS
       -> cycle validation
       -> empty node validation
       -> positive scope / NOT anchor validation
       -> compileQuery
            -> index kind and KeyType validation
            -> value expression validation
            -> query key requirements validation
  -> collect required Requirements
  -> canonical 过滤表达式+ requirements + probe threshold
  -> SHA-256 Fingerprint
  -> Plan
```

### 7.1 Index slot

编译时把 Query 的索引名称解析成 slice slot。运行时不再反复通过字符串 map 查索引。

### 7.2 正向 scope 与 anchor

一个节点可独立从索引建立候选域时称为可 anchor：

- Lookup：可以。
- Exclude：不可以。
- None：不可以，但可在无 scope 下执行。
- And/Or/If：根据子节点递归判断。

根计划必须能安全执行。Exclude 只能继承已有 scope。

### 7.3 Requirements

Requirements 只收集计划实际引用的索引和 Fact，并按名称排序：

```go
type Requirements struct {
    Indexes []RequiredIndex
    Facts   []FactSpec
}
```

MultiValue requirement 包含 KeyType、字段和两个 key 上限。

### 7.4 Fingerprint

Fingerprint 覆盖：

- 规范化 过滤表达式。
- Query 和动态值表达式。
- 实际 Requirements。
- ContainsProbeThreshold。

And/Or 和 Union 的可交换子项在 canonical 阶段排序，因此仅调换顺序不会改变指纹。

## 8. IndexStore 生命周期

```text
New
  -> create mutable indexes
  -> Active = empty

Add(DocID, *common.Ticket)
  -> validate every index
  -> add every index
  -> Active.Add

Remove(DocID)
  -> remove every index
  -> Active.Remove

BeginTick(tickFacts)
  -> validate Tick Fact type namespace
  -> prepare indexes
  -> borrow Tick Facts
  -> TickSession{store, borrowed Tick Facts}
```

Add 先校验非零 DocID、非 nil Ticket 和所有 index.validate，再写任何索引，避免字段 key 超限造成半写入。

## 9. TickSession 执行

```text
CandidatesWithStats(seedDocID, seedTicket, seedFacts)
  -> validate TickSession
  -> require seedTicket != nil and seedDocID in Active
  -> validate Seed Fact types and Tick/Seed name collisions
  -> bindContext{seed, borrowed Tick Facts, borrowed Seed Facts}
  -> eval(root, scope=nil)
  -> owned DocSet
```

### 9.1 Lookup

```text
bind Query
  |
  +-- scope != nil and cardinality <= threshold
  |      -> for each DocID: index.contains
  |
  +-- otherwise
         -> index.lookup
         -> optional AND scope
```

默认 threshold 是 4096。

### 9.2 And

没有继承 scope 时：

1. 对可 anchor 子树做基数估算。
2. 选择估算最小的子树。
3. 执行它，得到 accumulator。
4. 其他子树在 accumulator scope 上执行。
5. accumulator 为空立即停止。

### 9.3 Or

执行各分支并 OR。已有 scope 时，每个分支结果都被限制在 scope；当 union 基数等于 scope 基数时可以安全停止。

### 9.4 Exclude

```text
out = clone(scope)
out.AndNot(excluded)
```

编译期和运行期都会防御无 scope Exclude。

### 9.5 If

先绑定并计算 Condition，只执行 Then 或 Else。未选路径不会绑定 Query、读取 Fact 或访问索引。

## 10. 错误模型

```go
type Error struct {
    Phase string
    Path  string
    Code  string
    Err   error
}
```

- Compile 错误 Phase 为 `compile`。
- Candidates 错误 Phase 为 `evaluate`。
- Path 指向准确配置或 过滤表达式节点。
- 错误不会自动转换为 None。
- 错误不会触发 Universe 或扫描回退。

## 11. 单 owner goroutine 与 TickSession

IndexStore 和 TickSession 都不是 goroutine-safe，也不包含锁。一个 LogicalNode 的同一个 owner goroutine 必须串行执行全部 Add、Remove、BeginTick 和 Candidates，不能把这些方法分派到不同 goroutine。

TickSession 只读借用 Tick Fact maps/slices，调用方必须保证 Session 存活期间不修改。Seed Facts 在单次 Candidates 调用期间只读引用，两个作用域不合并。Active、MultiValue postings、Int64Range postingsByValue、valueByDoc 和 sortedValues 都继续由 IndexStore 持有；session 不是 concurrent snapshot（并发快照）。LogicalNode 外层的 FactFrame 会先建立唯一一份自有拷贝，因此完整匹配链路仍具备明确所有权。

必须遵守：

```text
owner goroutine:
  IndexStore.Add(docID, ticket) / Remove(docID)
  -> BeginTick(tickFacts)
  -> one or more TickSession.Candidates(seedDocID, seedTicket, seedFacts)
  -> session no longer used
  -> IndexStore may Add / Remove again
```

GroupEvaluator、CandidateScore、FactProvider 和 ObjectFactProvider 等上层回调也在该 owner goroutine 内同步执行，禁止重入或等待另一个访问相同节点的 goroutine。LogicalNode 在 Prefilter 外持有 Tick FactFrame；同一对象的 Facts 按 TicketID 在每次 ProduceMatch 中最多生成一次，并通过 FactView 继续传给评分和评估层。

## 12. 性能模型

- posting 和 accumulator 使用压缩 Roaring Bitmap。
- And 从最小估算 anchor 开始。
- 小 scope 使用 Contains probe。
- 大 scope 使用 Bitmap container 运算。
- Int64Range 按范围内 distinct keys，而不是整数宽度执行。
- Candidates 返回 DocSet，不物化完整 Ticket。

现有 benchmark 覆盖 10 万、50 万和 100 万文档的 string MultiValue 查询。

## 13. 当前限制

- 整个包只支持 single-owner goroutine；没有锁、并发快照或跨 goroutine 兼容层。
- Candidates 只验证 seedTicket 非 nil 和 seedDocID Active，不校验传入 Ticket 字段与 Add 时的值一致。
- IndexStore 没有 Update；应 Remove 后 Add。
- DocSet.Add/Remove 对 nil receiver 不安全。
- Condition 当前只有 int64 GreaterOrEqual。
- MultiValue 查询中的多个值固定采用 OR 语义。
- JSON 只生成单个计划；尚无 generation 发布和热更新管理器。
- 不提供扫描 oracle；逐 Ticket 扫描只存在于测试代码。
