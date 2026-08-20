# CandidatePlan 树形索引初筛层设计

> 状态：设计草案，本文接口尚未在当前代码中实现。  
> 范围：仅描述单个 `OwnerNode` 内的索引初筛，不定义具体业务字段和匹配规则。
>
> JSON 配置与热更新方案：[CandidatePlan JSON 配置与热更新实现方案](json-candidate-plan-hot-reload.md)。

## 1. 目标与边界

索引初筛层面向单个 `OwnerNode` 约 10 万～100 万个等待中的 Ticket。它的目标是通过声明式索引查询快速产生候选安全超集，并避免全池 Ticket 扫描、重复集合分配和提前物化大量 Ticket。

完整匹配仍然分为三个阶段：

1. `CandidatePlan`（候选计划）通过索引生成候选 Bitmap（位图）。
2. `GroupEvaluator`（组评估器）读取候选 Ticket，完成双向、动态和组级正确性校验。
3. `GroupBuilder`（建组器）使用 Greedy（贪心）算法构造最终 group。

索引初筛层必须遵守以下边界：

- 初筛期间不遍历全池 Ticket，不调用等价于 `pool.allSet()` 的构造逻辑。
- 缺少索引时直接报错，不允许退化为 Ticket 扫描。
- 初筛只返回安全超集，不替代最终组正确性校验。
- 一个 Ticket 只属于 Router 选定的唯一 `OwnerNode`，普通 CandidatePlan 不跨节点查询。
- 不包含旧 `RuleEngine`、旧线性 `CandidateFilter` 或任何具体业务算法。

## 2. 整体架构

```text
Router
  -> OwnerNode
  -> Tick Snapshot
  -> CandidatePlan Executor
  -> Candidate Bitmap
  -> remove seed / used
  -> bounded heap Top-L
  -> materialize Ticket
  -> GroupEvaluator
  -> Greedy GroupBuilder
  -> Match
```

各组件职责如下：

| 组件 | 职责 |
| --- | --- |
| `Router` | 为 Ticket 选择唯一 `OwnerNode`，不参与节点内候选正确性判断 |
| `OwnerNode` | 持有本节点 Ticket、Active Bitmap、索引、计划和执行状态 |
| `CandidatePlan` | 决定执行哪些索引查询，以及 Bitmap 如何组合 |
| `IndexQuery` | 声明要查询哪个索引、使用什么操作符、查询值从哪里获得 |
| `CandidateIndex` | 保存 posting（倒排集合），执行 Query 并返回 Bitmap |
| `GroupEvaluator` | 对已物化候选执行 Join、Start、ForceStart 等最终校验 |
| `GroupBuilder` | 对通过校验的候选执行 Greedy 建组 |

可以将三层核心职责概括为：

```text
CandidatePlan 决定执行结构
IndexQuery 决定查询内容
CandidateIndex 决定如何从索引产生结果
```

## 3. Ticket 与候选域

建议 Ticket 使用通用多值字段：

```go
// 设计草案
type Ticket struct {
    DocID      uint32
    TicketID   string
    CreatedAt  int64
    Fields     map[string][]string
    Numeric    map[string]int64
}
```

核心不解释字段名称。单值字符串字段使用长度为 1 的切片表示。

Ticket 入池时必须深拷贝，入池后视为不可变。索引只保存 `DocID`，完整 Ticket 在节点内只保存一份。

本次计划可访问的最大候选域称为 `Universe`。初版中：

```text
Universe = 当前 OwnerNode 的 Active Bitmap
```

CandidatePlan 的正向索引查询从 Universe 中建立或缩小候选集合。任何 Filter、Branch 或 Not 都不能把其他 OwnerNode 的 DocID 引入当前结果。

## 4. CandidatePlan 树节点

CandidatePlan 使用独立 AST（抽象语法树），不复用 GroupEvaluator 的组合节点。

```go
// 设计草案
type CandidateExpr interface {
    candidateExpr()
}

type FilterNode struct {
    Query IndexQuery
}

type AllNode struct {
    Children []CandidateExpr
}

type AnyNode struct {
    Children []CandidateExpr
}

type NotNode struct {
    Child CandidateExpr
}

type BranchNode struct {
    Predicate SeedFactPredicate
    Then      CandidateExpr
    Else      CandidateExpr
}

type EmptyNode struct{}
```

### 4.1 Filter

`Filter` 是索引查询叶节点。它不保存索引，也不扫描 Ticket，只负责把声明式 Query 交给已经绑定的索引。

```text
Filter(IndexQuery)
  -> CandidateIndex.Lookup(IndexQuery)
  -> Bitmap
```

### 4.2 All

`All` 表示集合交集：

```text
All(A, B, C) = A AND B AND C
```

执行器优先选择预计基数最小的正向子节点建立 accumulator（累积器），后续原地执行 `And` 或 `AndNot`。任意阶段结果为空即可短路。

空 `All` 没有正向锚点，构建期直接拒绝。

### 4.3 Any

`Any` 表示集合并集：

```text
Any(A, B, C) = A OR B OR C
```

所有可能增加结果的分支都必须执行。候选数量达到 `CandidateLimitPerSeed` 不能作为 Any 的短路条件，因为后续分支可能包含分数更高的合法候选。

只有结果已经等于当前 Universe 时才允许安全短路。空 `Any` 构建期直接拒绝；需要显式空结果时使用 `EmptyNode`。

### 4.4 Branch

`Branch` 是控制流选择，不是集合并集：

```text
Branch(predicate)
  -> predicate == true  : execute Then
  -> predicate == false : execute Else
```

Predicate（条件表达式）只能读取：

- 当前 seed 的字段和创建时间；
- `now`；
- 当前 Tick 的只读 Fact 快照。

它不能读取 candidate、group、当前候选集合或 Pool 中的 Ticket。Then 与 Else 必须都显式配置；无结果使用 `EmptyNode`。

未选中的分支不能绑定动态 Query 值，也不能访问索引。

### 4.5 Not：仅支持锚定差集

`Not` 不支持独立的全局反转，只能从已经存在的正向候选域中扣除结果：

```text
允许：All(A, Not(B)) = A AND NOT B = A - B
允许：All(Any(A, C), Not(B))

拒绝：Not(B)
拒绝：Any(A, Not(B))  // 根部没有可供 Not 分支使用的正向候选域
```

如果外层已经建立候选域，该候选域可以传入嵌套 Any 或 Branch：

```text
All(
    A,
    Branch(condition, Not(B), C),
)
```

这里 Branch 执行前已经存在 A，因此 Then 中的 `Not(B)` 合法。

错误、缺失索引或缺失动态值绝不能被当作 Empty，否则负向运算可能错误扩大候选结果。

### 4.6 简单集合示例

```text
A = {1, 2, 3, 4}
B = {2, 3, 5}
C = {3, 4, 6}

All(A, B)        = {2, 3}
Any(B, C)        = {2, 3, 4, 5, 6}
All(A, Not(B))   = {1, 4}
All(A, Any(B,C)) = {2, 3, 4}
```

## 5. 索引与 Query

### 5.1 接口关系

`IndexQuery` 是声明式查询的统一接口，具体查询类型实现该接口：

```text
IndexQuery
  |- MultiKeyQuery
  |- ExactValueQuery
  `- NumericRangeQuery
```

索引写入和快照查询接口建议为：

```go
// 设计草案
type CandidateIndex interface {
    Contract() IndexContract
    Add(docID uint32, ticket *Ticket) error
    Remove(docID uint32)
    Snapshot() IndexSnapshot
}

type IndexSnapshot interface {
    Estimate(query IndexQuery) (CardinalityEstimate, error)
    Lookup(query IndexQuery) (BitmapView, error)
    Contains(query IndexQuery, docID uint32) (bool, error)
}
```

所有共享 posting 和 IndexSnapshot 都是只读对象。执行器只能修改自己持有的 accumulator。

### 5.2 MultiKeyIndex

MultiKeyIndex（多值倒排索引）允许同一 DocID 写入多个 key：

```text
Ticket #42 Fields["dimension_a"] = {a1, a2}

posting[a1] -> add 42
posting[a2] -> add 42
```

同一次 `IN` 查询中的多个 key 使用 OR：

```text
dimension_a IN {a1, a2}
= posting[a1] OR posting[a2]
```

不同 Filter 放在 `All` 下时使用 AND：

```text
All(
    dimension_a IN {a1, a2},
    dimension_b IN {b1, b2},
)

= (posting[a1] OR posting[a2])
  AND
  (posting[b1] OR posting[b2])
```

如果值之间存在绑定关系，不能错误简化成两个独立 IN：

```text
(dimension_a=a1 AND dimension_b=b1)
OR
(dimension_a=a2 AND dimension_b=b2)
```

应表示为：

```text
Any(
    All(Filter(a1), Filter(b1)),
    All(Filter(a2), Filter(b2)),
)
```

热点复合索引只能作为监控证明后的性能优化，不能成为正确性的唯一来源。

### 5.3 Key 数量限制

两个限制必须分开定义：

```go
// 设计草案
type MultiKeyIndexConfig struct {
    Name                string
    Field               string
    MaxDocumentKeyCount int
    MaxQueryKeyCount    int
}
```

| 配置 | 含义 | 检查时机 |
| --- | --- | --- |
| `MaxDocumentKeyCount` | 一个 Ticket 为该索引产生的不同 key 数量上限 | Add 时，规范化和去重之后 |
| `MaxQueryKeyCount` | 一个 Filter 单次查询最终合并的不同 key 数量上限 | 每个 seed 绑定 Query 时 |

如果 Query 只读取一个 seed 字段：

```go
Values: SeedField("dimension_a")
```

那么 `MaxQueryKeyCount` 在这次查询中等价于 seed 的 `dimension_a` 查询 key 数量限制。但当 Query 合并多个声明式值来源时，它限制的是合并、规范化和去重后的总查询 key 数。

超过限制必须返回错误，不能静默截断。

### 5.4 NumericRangeIndex

NumericRangeIndex（数值范围索引）使用：

```text
value -> Roaring Bitmap
ordered distinct-value keys
docID -> value
```

范围查询通过二分查找定位起始 distinct key，只遍历范围内实际存在的 bucket。禁止按每个整数逐值扫描 `[min,max]`。

当 accumulator 已经很小时，可以读取 `docID -> value` 做 Contains probe（成员探测），但仍然不能读取候选 Ticket。

## 6. 基于等待时间的动态范围

如果只是范围参数随等待时间变化，应使用声明式 `WaitStepsInt64`，不需要建立多棵重复 Branch。

```go
// 设计草案
rangeByWait := WaitStepsInt64(
    WaitStepInt64{WaitMs: 0,      Value: 50},
    WaitStepInt64{WaitMs: 30_000, Value: 150},
    WaitStepInt64{WaitMs: 60_000, Value: 500},
)

scoreFilter := Filter(NumericRangeQuery{
    Index: "numeric_index",
    Min: SubInt64(
        SeedNumeric("numeric_value"),
        rangeByWait,
    ),
    Max: AddInt64(
        SeedNumeric("numeric_value"),
        rangeByWait,
    ),
})
```

运行时：

```text
waitMs = now - seed.CreatedAt
radius = rangeByWait(waitMs)
min = seed.Numeric["numeric_value"] - radius
max = seed.Numeric["numeric_value"] + radius
```

`AddInt64/SubInt64` 必须使用安全的饱和运算，避免 `int64` 溢出。

选择原则：

- 只有 Query 参数随时间变化：使用 `WaitStepsInt64`。
- 等待后需要切换索引、操作符或整段规则结构：使用 `Branch`。

索引查询只完成 seed 视角的粗筛。如果最终规则要求 candidate 也接受 seed，或要求全组满足共同范围，必须由 `GroupEvaluatorJoin` 再次验证。

## 7. 构建与编译流程

这里的 Compile（编译）不是 Go 编译，而是验证并绑定配置树，生成可以直接执行的只读计划。

```text
register indexes and facts
  -> build CandidatePlan AST
  -> CompileCandidatePlan
  -> validate
  -> bind index slots
  -> normalize execution graph
  -> create CompiledCandidatePlan
  -> start OwnerNode
```

### 7.1 编译产物

最小编译产物为：

```go
// 设计草案
type CompiledCandidatePlan struct {
    Fingerprint PlanFingerprint
    Contract    IndexContract
    Root        ExecutableNode
}
```

- `Fingerprint`：规范化 AST、字段、操作符、动态值引用和索引契约的稳定 Hash，不包含运行时动态值。
- `Contract`：整棵树需要的索引、字段类型、操作符、Fact 和 QueryKey 能力。
- `Root`：已经绑定 IndexSlot 的可执行操作图，运行时不再通过字符串反复查找索引。

能够成功得到 `CompiledCandidatePlan` 就表示无环、类型和路径检查已经全部通过，不需要额外保存“验证结果”对象。

### 7.2 最直接的检查流程

Compiler 对 AST 执行 DFS（深度优先遍历）：

1. 使用 visiting/visited 状态检测循环引用。
2. 拒绝空 All、空 Any、缺失 Then/Else 和未知节点类型。
3. 为每个 Filter 解析索引，检查 Query 类型和操作符。
4. 检查 SeedField、SeedNumeric、FactRef 的声明类型。
5. 携带 `scopeAvailable` 状态验证每条路径的 Not 正向锚点。
6. 规范化 All/Any，生成可重排执行节点。
7. 收集 IndexContract 和 Fact 依赖。
8. 计算 PlanFingerprint。

为了严格禁止扫描，Branch Predicate 和 Query Value 必须是封闭的声明式类型，不能接受任意 Go 函数或闭包。

### 7.3 锚点检查

编译器携带：

```text
scopeAvailable bool
```

规则如下：

- 正向 Filter 可以建立或缩小 scope。
- All 先选择一个可以建立 scope 的正向子节点，再验证其他子节点。
- Any 的所有分支继承父级 scope；没有父级 scope 时，每个非空分支必须自行建立 scope。
- Branch 的 Then/Else 继承相同父级 scope。
- Not 要求进入节点前 `scopeAvailable == true`。

典型错误：

```text
plan.root.any[1].not:
NOT requires a positive candidate scope
```

```text
plan.root.children[0].filter:
index "numeric_index" does not support string IN query
```

```text
plan.root.branch.else:
missing required index "dimension_b_index"
```

### 7.4 动态值限制的检查时机

启动时检查声明上限是否满足 IndexContract；运行时检查当前 seed 实际生成的值数量。

```text
声明最多产生 128 个 key，索引只允许 64：拒绝节点启动
当前 seed 实际产生 70 个 key，索引允许 64：终止当前 seed 并返回运行错误
```

运行时不能截断成前 64 个，否则会漏掉合法候选。

## 8. Tick 运行流程

### 8.1 创建不可变快照

Tick 开始时为当前节点创建：

```go
// 设计草案
type TickSnapshot struct {
    OwnerID NodeID
    Revision uint64
    Active   BitmapView
    Indexes  []IndexSnapshot
    Facts    FactSnapshot
}
```

当前 Tick 内 Active、posting、Fact 和 Revision 都不可变。Add/Remove 只能在 Tick 边界执行。

### 8.2 为 seed 绑定查询

执行器读取 seed、now 和 Fact，把声明式模板解析成具体 Query：

```text
SeedNumeric("numeric_value") -> 1200
WaitStepsInt64(wait=40s)      -> 150
NumericRangeQuery             -> [1050, 1350]
```

只绑定 Branch 选中的路径，未选路径不解析 Query。

### 8.3 估算与集合运算

以一个 All 为例：

```text
Filter A estimate = 30,000
Filter B estimate = 8,000
Not C estimate    = 500
```

执行顺序：

```text
accumulator = Clone(Filter B posting) // 最小正向集合
accumulator.And(Filter A posting)
accumulator.AndNot(Filter C posting)
```

Not 的结果不能作为初始锚点。

### 8.4 小集合与大集合

初始阈值为：

```text
accumulator <= 4096：遍历 accumulator，使用 IndexSnapshot.Contains 探测
accumulator > 4096：使用 Roaring Bitmap container 级集合运算
```

阈值可配置，并由 benchmark 最终校准。

计划采用 [`RoaringBitmap/roaring`](https://github.com/RoaringBitmap/roaring) 作为 Go 实现；跨实现的数据格式遵循 [`RoaringFormatSpec`](https://github.com/RoaringBitmap/RoaringFormatSpec)。该依赖属于后续代码实现范围，当前文档不会修改 `go.mod`。

### 8.5 扣除与 Top-L

计划执行完成后：

```text
candidate Bitmap
  -> remove seed.DocID
  -> AndNot(Tick used Bitmap)
```

当结果不超过 `CandidateLimitPerSeed` 时直接物化；超过上限时按 DocID 流式读取评分所需 Ticket，并用 bounded heap（有界堆）保留最高分 L 个候选。

```text
K = 初筛命中数量
L = CandidateLimitPerSeed，默认 128
复杂度 = O(K log L)
```

初筛完成前不得构造完整 `[]*Ticket`。

## 9. 完整配置示例

以下所有 API 都是设计草案。

```go
indexes := []CandidateIndexFactory{
    NewMultiKeyIndexFactory(MultiKeyIndexConfig{
        Name:                "dimension_a_index",
        Field:               "dimension_a",
        MaxDocumentKeyCount: 64,
        MaxQueryKeyCount:    64,
    }),
    NewMultiKeyIndexFactory(MultiKeyIndexConfig{
        Name:                "dimension_b_index",
        Field:               "dimension_b",
        MaxDocumentKeyCount: 64,
        MaxQueryKeyCount:    64,
    }),
    NewMultiKeyIndexFactory(MultiKeyIndexConfig{
        Name:                "category_index",
        Field:               "category",
        MaxDocumentKeyCount: 64,
        MaxQueryKeyCount:    64,
    }),
    NewNumericRangeIndexFactory(NumericRangeIndexConfig{
        Name:  "numeric_index",
        Field: "numeric_value",
    }),
}

rangeByWait := WaitStepsInt64(
    WaitStepInt64{WaitMs: 0,      Value: 50},
    WaitStepInt64{WaitMs: 30_000, Value: 150},
    WaitStepInt64{WaitMs: 60_000, Value: 500},
)

plan := All(
    Filter(MultiKeyQuery{
        Index:  "dimension_a_index",
        Op:     OpIn,
        Values: SeedField("dimension_a"),
    }),
    Branch(
        GreaterOrEqual(SeedWaitMs(), Int64Value(20_000)),
        Any(
            Filter(MultiKeyQuery{
                Index:  "dimension_b_index",
                Op:     OpIn,
                Values: SeedField("dimension_b"),
            }),
            Filter(NumericRangeQuery{
                Index: "numeric_index",
                Min: SubInt64(
                    SeedNumeric("numeric_value"),
                    rangeByWait,
                ),
                Max: AddInt64(
                    SeedNumeric("numeric_value"),
                    rangeByWait,
                ),
            }),
        ),
        Filter(NumericRangeQuery{
            Index: "numeric_index",
            Min: SubInt64(
                SeedNumeric("numeric_value"),
                Int64Value(50),
            ),
            Max: AddInt64(
                SeedNumeric("numeric_value"),
                Int64Value(50),
            ),
        }),
    ),
    Not(Filter(MultiKeyQuery{
        Index:  "category_index",
        Op:     OpIn,
        Values: SeedField("excluded_categories"),
    })),
)

rules := NewRuleSet(plan, startEvaluator)
rules.WithCandidateScore(scoreCandidate)

node := NodeConfig{
    ID: "node-1",
    Pool: PoolConfig{
        MaxPlayers:          4,
        BitmapProbeThreshold: 4096,
        GroupBuilder: GroupBuilderConfig{
            CandidateLimitPerSeed: 128,
        },
    },
    Indexes: indexes,
    Rules:   rules,
}

system, err := NewMatchSystem(SystemConfig{
    Router: router,
    Nodes:  []NodeConfig{node},
})
if err != nil {
    return err // 包含索引注册、CandidatePlan 编译和契约错误
}
```

该示例同时展示：

- 多值字符串索引；
- All、Any、Branch 和锚定 Not；
- 基于等待时间的动态数值范围；
- 索引、RuleSet 与 NodeConfig 的组合。

## 10. 错误处理

### 10.1 构建期错误

- 索引或 Fact 未注册；
- 字段、Query 值或操作符类型不匹配；
- 索引不支持指定操作符；
- AST 存在循环、空组合或缺失 Branch 分支；
- 某条路径包含无正向锚点 Not；
- Branch 或 Query 包含任意函数、候选依赖或扫描能力；
- 声明的动态 key 上限超过 IndexContract。

构建期错误拒绝节点启动，并返回带 AST 路径的结构化错误。

### 10.2 运行期错误

- 必需 SeedField、SeedNumeric 或 Fact 缺失；
- 动态值类型错误或实际 QueryKey 数量超限；
- Snapshot Revision 不一致；
- IndexSnapshot 不可用或读取失败；
- Bitmap 数据损坏；
- 执行预算超限。

统一原则：

```text
错误 != Empty
错误 != Universe
错误 != 全池扫描
```

当前 seed 执行失败时保留 Ticket，不生成不可信 Match，并记录结构化指标。

## 11. 测试与验收

### 11.1 语义测试

- 多层 All、Any、Branch、Empty 和锚定 Not；
- 无锚点 Not、空组合、缺失 Then/Else 的构建拒绝；
- 未选中的 Branch 不绑定 Query、不调用索引；
- Any 不因候选数量达到上限而遗漏其他分支；
- 多值独立维度和绑定组合；
- WaitSteps 动态范围和数值边界饱和运算；
- GroupEvaluator 对双向和全组约束的最终校验。

### 11.2 索引正确性

- 测试代码使用逐 Ticket 扫描实现作为 oracle（标准答案）；
- 随机生成 Ticket、索引数据和合法 AST；
- 按 DocID 比较 Bitmap 计划结果和 oracle；
- 验证 Add、Remove、Match 后 Active 和所有 posting 一致；
- 验证同一 Tick Snapshot 内结果稳定。

oracle 只能存在于测试中，生产执行器不能调用它作为回退。

### 11.3 执行路径验证

通过 instrumentation（执行插桩）验证：

- 不遍历 `ticketsByDocID` 构造全集；
- 初筛完成前不物化候选 Ticket；
- 未选 Branch 不访问 IndexSnapshot；
- accumulator 只进行必要 Clone；
- 小集合走 Contains probe；
- 大集合走 Roaring Bitmap 运算；
- 缺少索引不会触发扫描回退。

### 11.4 Benchmark

覆盖以下规模：

```text
Pool：100,000 / 500,000 / 1,000,000 Ticket
选择率：0.1% / 1% / 10% / 50% / 90%
连续条件：2 / 4 / 8 个
```

记录：

- map TicketSet 与 Roaring Bitmap 的耗时；
- 分配次数、GC 和内存；
- IndexSnapshot 调用次数；
- Bitmap Clone、And、Or、AndNot、Contains 次数；
- Top-L heap 访问数量。

## 12. 最终约束摘要

- Router 决定唯一 OwnerNode，CandidatePlan 只处理节点内候选。
- CandidatePlan 是唯一索引初筛入口。
- CandidatePlan 决定执行结构，IndexQuery 描述查询内容，CandidateIndex 产生 Bitmap。
- 初筛严格索引化，不扫描候选 Ticket，不提供无索引回退。
- Not 只执行有正向候选域的锚定差集。
- 多值字段使用 posting Union，不复制完整 Ticket。
- 动态范围通过声明式 Query 值表达式计算。
- 初筛结果是安全超集，最终正确性由 GroupEvaluator 保证。
