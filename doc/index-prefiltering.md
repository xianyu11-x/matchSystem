# Prefilter 树形索引初筛层设计

> 状态：核心索引初筛已实现；JSON 解析和热更新不在本阶段范围内。
> 实现：`internal/matchsystem/prefilter` 子包；上层 `internal/matchsystem.LogicalNode` 直接持有匹配数据与算法状态，并由同包 PhysicalNode 选择执行。
> 范围：仅描述单个 `OwnerNode` 内的索引初筛，不定义具体业务字段和匹配规则。
>
> JSON 配置与热更新方案：[Prefilter JSON 配置与热更新实现方案](json-prefilter-hot-reload.md)。
> 物理/逻辑拓扑映射见 [Router 物理节点与逻辑节点设计](router-physical-logical-node.md)：MatchService 是匹配服务器，PhysicalNode 是与其一一对应的算法实例；本文 `OwnerNode` 对应 PhysicalNode 内数据隔离的 `LogicalNode`，`NodeID` / `OwnerID` 对应稳定 `LogicalNodeKey`。`PhysicalNodeID` 属于 Router 的 `OwnerRef`，不进入候选集合身份。
> 本文沿用当前 `LogicalNode.Tick` API 表示单个 LogicalNode 的一次本地匹配执行。外部 MatchService 调用 `PhysicalNode.Tick` 后，PhysicalNode 选择一个 LogicalNode，并串行触发该节点的一次匹配。匹配核心不关心 MatchService 的调度、限速和服务器 IO；LogicalNode 自身也没有独立 Tick 或执行线程。
> 整个 PhysicalNode、LogicalNode 和 Prefilter 状态必须由同一个 owner goroutine 串行驱动；核心不使用锁，也不允许把 Add、Remove、Get 和 Tick 分派到不同 goroutine。

## 1. 目标与边界

索引初筛层面向单个 `OwnerNode` 约 10 万～100 万个等待中的 Ticket。它的目标是通过声明式索引查询快速产生候选安全超集，并避免全池 Ticket 扫描、重复集合分配和提前物化大量 Ticket。

完整匹配仍然分为三个阶段：

1. `Prefilter`（候选计划）通过索引生成候选 Bitmap（位图）。
2. `GroupEvaluator`（组评估器）读取候选 Ticket，完成双向、动态和组级正确性校验。
3. `GroupBuilder`（建组器）使用 Greedy（贪心）算法构造最终 group。

索引初筛层必须遵守以下边界：

- 初筛期间不遍历 LogicalNode 的全部 Ticket，也不构造等价的全量集合。
- 缺少索引时直接报错，不允许退化为 Ticket 扫描。
- 初筛只返回安全超集，不替代最终组正确性校验。
- 一个 Ticket 只属于 Router 选定的唯一 `OwnerNode`，普通 Prefilter 不跨节点查询。
- 全部可变状态操作属于同一 owner goroutine；外部并发必须在进入匹配核心之前串行化。
- 不包含旧 `RuleIndexStore`、旧线性 `CandidateFilter` 或任何具体业务算法。

## 2. 整体架构

```text
ClientRouter -> PhysicalNode（仅负责 Ticket 归属与远程选路）

外部 MatchService -> PhysicalNode.Tick
  -> PhysicalNode.LogicalNodeSelector 选择一个 OwnerNode
  -> LogicalNode.Tick
  -> TickSession（固定 now + Fact）
  -> Prefilter Executor
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
| `Router` | 为 Ticket 选择 PhysicalNode，并形成唯一 OwnerRef；不参与节点内候选正确性判断 |
| `PhysicalNode.Tick` | 选择一个 LogicalNode 执行一次本地匹配 |
| `OwnerNode` | 持有本节点 Ticket、Active Bitmap、索引、计划和执行状态 |
| `Prefilter` | 决定执行哪些索引查询，以及 Bitmap 如何组合 |
| `IndexQuery` | 声明要查询哪个索引、使用哪种查询类型、查询值从哪里获得 |
| `IndexStore/runtimeIndex` | 保存 posting（倒排集合），执行 Query 并返回 DocSet |
| `GroupEvaluator` | 对已物化候选执行 Join、Start、ForceStart 等最终校验 |
| `GroupBuilder` | 对通过校验的候选执行 Greedy 建组 |

可以将三层核心职责概括为：

```text
Prefilter 决定执行结构
IndexQuery 决定查询内容
IndexStore/runtimeIndex 决定如何从索引产生结果
```

## 3. Ticket 与候选域

当前 Ticket 使用通用多值字段：

```go
type Ticket struct {
    DocID       uint32
    TicketID    string
    CreatedAt   int64
    StringLists map[string][]string
    Uint64Lists map[string][]uint64
    Int64Values map[string]int64
}
```

核心不解释字段名称。单值字符串或 uint64 字段使用长度为 1 的切片表示。

Ticket 入池时必须深拷贝，入池后视为不可变。索引只保存 `DocID`，完整 Ticket 在节点内只保存一份。

本次计划可访问的最大候选域称为 `Universe`。初版中：

```text
Universe = 当前 OwnerNode 的 Active Bitmap
```

Prefilter 的正向索引查询从 Universe 中建立或缩小候选集合。任何 Lookup、If 或 Exclude 都不能把其他 OwnerNode 的 DocID 引入当前结果。

## 4. Prefilter 树节点

Prefilter 使用独立、封闭的 Lookup Expression（过滤表达式），不复用 GroupEvaluator 的组合节点。对外通过 `Lookup`、`And`、`Or`、`Exclude`、`If` 和 `None` 构造器创建，具体节点结构保持为子包私有类型，调用方不能注入任意实现。

```go
// 实际代码中的封闭接口与私有节点
type Expr interface { expr() }

type lookupExpr struct { query IndexQuery }
type andExpr struct { children []Expr }
type orExpr struct { children []Expr }
type excludeExpr struct { child Expr }
type ifExpr struct {
    condition Condition
    thenExpr  Expr
    elseExpr  Expr
}
type noneExpr struct{}
```

### 4.1 Lookup

`Lookup` 是索引查询叶节点。它不保存索引，也不扫描 Ticket，只负责把声明式 Query 交给已经绑定的索引。

```text
Lookup(IndexQuery)
  -> runtimeIndex.lookup(boundIndexQuery)
  -> DocSet
```

### 4.2 And

`And` 表示集合交集：

```text
And(A, B, C) = A AND B AND C
```

执行器优先选择预计基数最小的正向子节点建立 accumulator（累积器），后续原地执行 `And` 或 `Subtract`。任意阶段结果为空即可短路。

空 `And` 没有正向锚点，构建期直接拒绝。

### 4.3 Or

`Or` 表示集合并集：

```text
Or(A, B, C) = A OR B OR C
```

所有可能增加结果的分支都必须执行。候选数量达到 `CandidateLimitPerSeed` 不能作为 Or 的短路条件，因为后续分支可能包含分数更高的合法候选。

只有结果已经等于当前 Universe 时才允许安全短路。空 `Or` 构建期直接拒绝；需要显式空结果时使用 `EmptyNode`。

### 4.4 If

`If` 是控制流选择，不是集合并集：

```text
If(condition)
  -> condition == true  : evaluate Then
  -> condition == false : evaluate Else
```

Condition（条件表达式）只能读取：

- 当前 seed 的字段和创建时间；
- `now`；
- 当前 Tick 的只读 Fact 快照。

它不能读取 candidate、group、当前候选集合或 Pool 中的 Ticket。Then 与 Else 必须都显式配置；无结果使用 `EmptyNode`。

未选中的分支不能绑定动态 Query 值，也不能访问索引。

### 4.5 Exclude：仅支持锚定差集

`Exclude` 不支持独立的全局反转，只能从已经存在的正向候选域中扣除结果：

```text
允许：And(A, Exclude(B)) = A AND NOT B = A - B
允许：And(Or(A, C), Exclude(B))

拒绝：Exclude(B)
拒绝：Or(A, Exclude(B))  // 根部没有可供 Exclude 分支使用的正向候选域
```

如果外层已经建立候选域，该候选域可以传入嵌套 Or 或 If：

```text
And(
    A,
    If(condition, Exclude(B), C),
)
```

这里 If 执行前已经存在 A，因此 Then 中的 `Exclude(B)` 合法。

错误、缺失索引或缺失动态值绝不能被当作 None，否则负向运算可能错误扩大候选结果。

### 4.6 简单集合示例

```text
A = {1, 2, 3, 4}
B = {2, 3, 5}
C = {3, 4, 6}

And(A, B)        = {2, 3}
Or(B, C)        = {2, 3, 4, 5, 6}
And(A, Exclude(B))   = {1, 4}
And(A, Or(B,C)) = {2, 3, 4}
```

## 5. 索引与 Query

### 5.1 接口关系

`IndexQuery` 是声明式查询的统一接口，具体查询类型实现该接口：

```text
IndexQuery
  |- StringQuery        // string key
  |- Uint64Query  // uint64 key
  `- Int64RangeQuery
```

`ExactValueQuery` 不单独存在；单值精确查询就是只有一个查询值的 `StringQuery`。

当前子包把可变索引实现保持为私有类型，只向上层暴露以下稳定入口，避免 Roaring Bitmap 的可变 posting 被外部持有：

```go
plan, err := prefilter.Compile(config)
store, err := prefilter.New(plan)

err = store.Add(prefilter.Document{...})
removed := store.Remove(docID)
session := store.BeginTick(now, facts)
candidates, err := session.Candidates(seed)
```

`IndexStore` 持有 Active 和物理索引；`TickSession` 固定本轮 now 和深拷贝后的 Fact，但不复制索引；`Candidates` 返回调用方独占的可变 DocSet。所有共享 posting 都不向上层暴露。

### 5.2 MultiValueIndex

MultiValueIndex（多值倒排索引）允许同一 DocID 写入多个 key，并原生支持 `string` 和 `uint64` 两种 KeyType（键类型）。没有配置 `KeyType` 时默认使用 `KeyTypeString`，保持现有字符串配置兼容；uint64 使用 `KeyTypeUint64`，不会转换为十进制字符串。

```text
Ticket #42 StringLists["dimension_a"] = {a1, a2}

posting[a1] -> add 42
posting[a2] -> add 42
```

uint64 示例：

```go
NewMultiValueIndex(MultiValueIndexConfig{
    Name:                "uint64_dimension_index",
    Field:               "uint64_dimension",
    KeyType:             KeyTypeUint64,
    MaxDocumentValues: 64,
    MaxQueryValues:    64,
})

Lookup(Uint64Query{
    Index:  "uint64_dimension_index",
    Values: UnionUint64s(
        SeedUint64s("uint64_dimension"),
        FactUint64s("extra_uint64_values"),
        LiteralUint64s(1001, 1002),
    ),
})
```

uint64 posting 直接使用 `map[uint64]*roaring.Bitmap`，因此 `1` 与字符串 `"1"` 是不同的契约类型，编译器禁止相互查询。

同一次 `IN` 查询中的多个 key 使用 OR：

```text
dimension_a IN {a1, a2}
= posting[a1] OR posting[a2]
```

不同 Lookup 放在 `And` 下时使用 AND：

```text
And(
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
Or(
    And(Lookup(a1), Lookup(b1)),
    And(Lookup(a2), Lookup(b2)),
)
```

热点复合索引只能作为监控证明后的性能优化，不能成为正确性的唯一来源。

### 5.3 Key 数量限制

两个限制必须分开定义：

```go
type MultiValueIndexConfig struct {
    Name                string
    Field               string
    KeyType             KeyType // KeyTypeString 或 KeyTypeUint64
    MaxDocumentValues int
    MaxQueryValues    int
}
```

| 配置 | 含义 | 检查时机 |
| --- | --- | --- |
| `MaxDocumentValues` | 一个 Ticket 为该索引产生的不同 string 或 uint64 key 数量上限 | Add 时，排序和去重之后 |
| `MaxQueryValues` | 一个 Lookup 单次查询最终合并的不同 string 或 uint64 key 数量上限 | 每个 seed 绑定 Query 时 |

如果 Query 只读取一个 seed 字段：

```go
Values: SeedStrings("dimension_a")
```

那么 `MaxQueryValues` 在这次查询中等价于 seed 的 `dimension_a` 查询 key 数量限制。但当 Query 合并多个声明式值来源时，它限制的是合并、规范化和去重后的总查询 key 数。

超过限制必须返回错误，不能静默截断。

### 5.4 Int64RangeIndex

Int64RangeIndex（数值范围索引）使用：

```text
value -> Roaring Bitmap
ordered distinct-value keys
docID -> value
```

范围查询通过二分查找定位起始 distinct key，只遍历范围内实际存在的 bucket。禁止按每个整数逐值扫描 `[min,max]`。

当 accumulator 已经很小时，可以读取 `docID -> value` 做 Contains probe（成员探测），但仍然不能读取候选 Ticket。

## 6. 基于等待时间的动态范围

如果只是范围参数随等待时间变化，应使用声明式 `WaitSteps`，不需要建立多棵重复 If。

```go
rangeByWait := WaitSteps(
    WaitStep{WaitMillis: 0,      Value: 50},
    WaitStep{WaitMillis: 30_000, Value: 150},
    WaitStep{WaitMillis: 60_000, Value: 500},
)

scoreLookup := Lookup(Int64RangeQuery{
    Index: "numeric_index",
    Min: SubInt64(
        SeedInt64("numeric_value"),
        rangeByWait,
    ),
    Max: AddInt64(
        SeedInt64("numeric_value"),
        rangeByWait,
    ),
})
```

运行时：

```text
waitMillis = now - seed.CreatedAt
radius = rangeByWait(waitMillis)
min = seed.Int64Values["numeric_value"] - radius
max = seed.Int64Values["numeric_value"] + radius
```

`AddInt64/SubInt64` 必须使用安全的饱和运算，避免 `int64` 溢出。

选择原则：

- 只有 Query 参数随时间变化：使用 `WaitSteps`。
- 等待后需要切换索引、查询类型或整段规则结构：使用 `If`。

索引查询只完成 seed 视角的粗筛。如果最终规则要求 candidate 也接受 seed，或要求全组满足共同范围，必须由 `GroupEvaluatorJoin` 再次验证。

## 7. 构建与编译流程

这里的 Compile（编译）不是 Go 编译，而是验证并绑定配置树，生成可以直接执行的只读计划。

```text
register indexes and facts
  -> build Prefilter 过滤表达式
  -> prefilter.Compile
  -> validate
  -> bind index slots
  -> normalize execution graph
  -> create CompiledPrefilter
  -> start OwnerNode
```

### 7.1 编译产物

当前编译产物为不可变对象，内部字段不导出：

```go
plan, err := prefilter.Compile(config)
fingerprint := plan.Fingerprint()
requirements := plan.Requirements()
store, err := prefilter.New(plan)
```

- `Fingerprint`：规范化过滤表达式、字段、查询类型、动态值引用和索引需求的稳定 Hash，不包含运行时动态值。
- `Requirements`：整棵树需要的索引、字段类型、Fact 和查询值上限。
- 私有执行根节点：已经绑定 IndexSlot 的可执行操作图，运行时不再通过字符串反复查找索引。

能够成功得到 `Plan` 就表示无环、类型和路径检查已经全部通过，不需要额外保存“验证结果”对象。

### 7.2 最直接的检查流程

Compiler 对 过滤表达式执行 DFS（深度优先遍历）：

1. 使用 visiting/visited 状态检测循环引用。
2. 拒绝空 And、空 Or、缺失 Then/Else 和未知节点类型。
3. 为每个 Lookup 解析索引，检查 Query 类型与索引类型是否匹配。
4. 检查 SeedStrings、SeedInt64、FactRef 的声明类型。
5. 携带 `scopeAvailable` 状态验证每条路径的 Exclude 正向锚点。
6. 规范化 And/Or，生成可重排执行节点。
7. 收集 Requirements 和 Fact 依赖。
8. 计算 Fingerprint。

为了严格禁止扫描，If Condition 和 Query Value 必须是封闭的声明式类型，不能接受任意 Go 函数或闭包。

### 7.3 锚点检查

编译器携带：

```text
scopeAvailable bool
```

规则如下：

- 正向 Lookup 可以建立或缩小 scope。
- And 先选择一个可以建立 scope 的正向子节点，再验证其他子节点。
- Or 的所有分支继承父级 scope；没有父级 scope 时，每个非空分支必须自行建立 scope。
- If 的 Then/Else 继承相同父级 scope。
- Exclude 要求进入节点前 `scopeAvailable == true`。

典型错误：

```text
plan.root.or[1].exclude:
NOT requires a positive candidate scope
```

```text
plan.root.children[0].lookup:
index "numeric_index" does not support string IN query
```

```text
plan.root.if.else:
missing required index "dimension_b_index"
```

### 7.4 动态值限制的检查时机

启动时检查声明上限是否满足 Requirements；运行时检查当前 seed 实际生成的值数量。

```text
声明最多产生 128 个 key，索引只允许 64：拒绝节点启动
当前 seed 实际产生 70 个 key，索引允许 64：终止当前 seed 并返回运行错误
```

运行时不能截断成前 64 个，否则会漏掉合法候选。

## 8. Tick 运行流程

### 8.1 创建评估批次

PhysicalNode 选定 LogicalNode 后，由该节点创建：

```go
session := store.BeginTick(now, prefilter.Facts{
    StringLists: stringFacts,
    Int64Values: int64Facts,
})

candidates, err := session.Candidates(seedDocument)
```

`TickSession` 只固定 now 和 Fact。Active 与 posting 继续由 IndexStore 持有；同一个 owner goroutine 保证批次使用期间不执行 Add/Remove。它不是并发快照，也不携带 OwnerID、Revision 或 Active 副本。

### 8.2 为 seed 绑定查询

执行器读取 seed、now 和 Fact，把声明式模板解析成具体 Query：

```text
SeedInt64("numeric_value") -> 1200
WaitSteps(wait=40s)      -> 150
Int64RangeQuery             -> [1050, 1350]
```

只绑定 If 选中的路径，未选路径不解析 Query。

### 8.3 估算与集合运算

以一个 And 为例：

```text
Lookup A estimate = 30,000
Lookup B estimate = 8,000
Exclude C estimate    = 500
```

执行顺序：

```text
accumulator = Clone(Lookup B posting) // 最小正向集合
accumulator.And(Lookup A posting)
accumulator.AndNot(Lookup C posting)
```

Exclude 的结果不能作为初始锚点。

### 8.4 小集合与大集合

初始阈值为：

```text
accumulator <= 4096：遍历 accumulator，使用 Index.Contains 探测
accumulator > 4096：使用 Roaring Bitmap container 级集合运算
```

阈值可配置，并由 benchmark 最终校准。

当前实现采用 [`RoaringBitmap/roaring`](https://github.com/RoaringBitmap/roaring)；跨实现的数据格式可参考 [`RoaringFormatSpec`](https://github.com/RoaringBitmap/RoaringFormatSpec)。

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

以下 API 已由 `prefilter` 子包提供；为突出计划结构，计划和索引构造器省略了 `prefilter.` 前缀。

```go
indexes := []IndexSpec{
    NewMultiValueIndex(MultiValueIndexConfig{
        Name:                "dimension_a_index",
        Field:               "dimension_a",
        MaxDocumentValues: 64,
        MaxQueryValues:    64,
    }),
    NewMultiValueIndex(MultiValueIndexConfig{
        Name:                "dimension_b_index",
        Field:               "dimension_b",
        MaxDocumentValues: 64,
        MaxQueryValues:    64,
    }),
    NewMultiValueIndex(MultiValueIndexConfig{
        Name:                "category_index",
        Field:               "category",
        MaxDocumentValues: 64,
        MaxQueryValues:    64,
    }),
    NewInt64RangeIndex(Int64RangeIndexConfig{
        Name:  "numeric_index",
        Field: "numeric_value",
    }),
}

rangeByWait := WaitSteps(
    WaitStep{WaitMillis: 0,      Value: 50},
    WaitStep{WaitMillis: 30_000, Value: 150},
    WaitStep{WaitMillis: 60_000, Value: 500},
)

root := And(
    Lookup(StringQuery{
        Index:  "dimension_a_index",
        Values: SeedStrings("dimension_a"),
    }),
    If(
        GreaterOrEqual(SeedWaitMillis(), LiteralInt64(20_000)),
        Or(
            Lookup(StringQuery{
                Index:  "dimension_b_index",
                Values: SeedStrings("dimension_b"),
            }),
            Lookup(Int64RangeQuery{
                Index: "numeric_index",
                Min: SubInt64(
                    SeedInt64("numeric_value"),
                    rangeByWait,
                ),
                Max: AddInt64(
                    SeedInt64("numeric_value"),
                    rangeByWait,
                ),
            }),
        ),
        Lookup(Int64RangeQuery{
            Index: "numeric_index",
            Min: SubInt64(
                SeedInt64("numeric_value"),
                LiteralInt64(50),
            ),
            Max: AddInt64(
                SeedInt64("numeric_value"),
                LiteralInt64(50),
            ),
        }),
    ),
    Exclude(Lookup(StringQuery{
        Index:  "category_index",
        Values: SeedStrings("excluded_categories"),
    })),
)

prefilterConfig := Config{
    Indexes:              indexes,
    Root: root,
    ContainsProbeThreshold: 4096,
}

// Prefilter 不放进 RuleSet；RuleSet 只保存最终 GroupEvaluator。
rules := matchsystem.NewRuleSet(startEvaluator)
rules.WithCandidateScore(scoreCandidate)

node, err := matchsystem.NewLogicalNode(matchsystem.LogicalNodeSpec{
    Key: logicalNodeKey,
    Config: matchsystem.LogicalNodeConfig{
        MaxPlayers: 4,
        GroupBuilder: matchsystem.GroupBuilderConfig{
            CandidateLimitPerSeed: 128,
        },
        Prefilter: prefilterConfig,
    },
    Rules: rules,
})
if err != nil {
    return err // 包含索引注册、Prefilter 编译和契约错误
}
```

该示例同时展示：

- 多值字符串索引；
- And、Or、If 和锚定 Exclude；
- 基于等待时间的动态数值范围；
- Prefilter 配置与上层 LogicalNode、RuleSet 的组合。

## 10. 错误处理

### 10.1 构建期错误

- 索引或 Fact 未注册；
- 字段或 Query 值类型不匹配；
- Query 类型与索引类型不匹配；
- 过滤表达式存在循环、空组合或缺失 If 分支；
- 某条路径包含无正向锚点 Exclude；
- If 或 Query 包含任意函数、候选依赖或扫描能力；
- 声明的动态 key 上限超过 Requirements。

构建期错误拒绝节点启动，并返回带 过滤表达式路径的结构化错误。

### 10.2 运行期错误

- 必需 SeedStrings、SeedInt64 或 Fact 缺失；
- 动态值类型错误或实际 QueryKey 数量超限；
- TickSession 未初始化；
- 物理 Index 查询失败；
- Bitmap 数据损坏；
- 执行预算超限。

统一原则：

```text
错误 != None
错误 != Universe
错误 != 全池扫描
```

当前 seed 执行失败时保留 Ticket，不生成不可信 Match，并记录结构化指标。

## 11. 测试与验收

### 11.1 语义测试

- 多层 And、Or、If、None 和锚定 Exclude；
- 无锚点 Exclude、空组合、缺失 Then/Else 的构建拒绝；
- 未选中的 If 不绑定 Query、不调用索引；
- Or 不因候选数量达到上限而遗漏其他分支；
- 多值独立维度和绑定组合；
- WaitSteps 动态范围和数值边界饱和运算；
- GroupEvaluator 对双向和全组约束的最终校验。

### 11.2 索引正确性

- 测试代码使用逐 Ticket 扫描实现作为 oracle（标准答案）；
- 随机生成 Ticket、索引数据和合法 过滤表达式；
- 按 DocID 比较 Bitmap 计划结果和 oracle；
- 验证 Add、Remove、Match 后 Active 和所有 posting 一致；
- 验证同一 TickSession 内 now 和 Fact 稳定。

oracle 只能存在于测试中，生产执行器不能调用它作为回退。

### 11.3 执行路径验证

通过 instrumentation（执行插桩）验证：

- 不遍历 `ticketsByDocID` 构造全集；
- 初筛完成前不物化候选 Ticket；
- 未选 If 不访问 Index；
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
- Index Estimate/Lookup/Contains 调用次数；
- Bitmap Clone、And、Or、AndNot、Contains 次数；
- Top-L heap 访问数量。

## 12. 最终约束摘要

- Router 决定唯一 OwnerNode，Prefilter 只处理节点内候选。
- Prefilter 是唯一索引初筛入口。
- 整套匹配状态由同一个 owner goroutine 串行驱动，不使用锁，不支持并发 Add/Remove/Tick。
- Prefilter 决定执行结构，IndexQuery 描述查询内容，IndexStore/runtimeIndex 产生 DocSet。
- 初筛严格索引化，不扫描候选 Ticket，不提供无索引回退。
- Exclude 只执行有正向候选域的锚定差集。
- 多值字段使用 posting Union，不复制完整 Ticket。
- 动态范围通过声明式 Query 值表达式计算。
- 初筛结果是安全超集，最终正确性由 GroupEvaluator 保证。
