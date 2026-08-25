# Prefilter 使用指南

> 本文面向直接使用 `internal/matchsystem/prefilter` 的调用者，所有示例均对应当前 Go API。

## 1. 最小完整示例

这个示例创建 string MultiValueIndex，让 seed 与候选共享任意 `dimension_a` key：

```go
package example

import (
    "matchSystem/internal/common"
    "matchSystem/internal/matchsystem/prefilter"
)

func run(now int64) ([]uint32, error) {
    config := prefilter.Config{
        Indexes: []prefilter.IndexSpec{
            prefilter.NewMultiValueIndex(
                prefilter.MultiValueIndexConfig{
                    Name:                "dimension_a_index",
                    Field:               "dimension_a",
                    KeyType:             prefilter.KeyTypeString,
                    MaxDocumentValues: 64,
                    MaxQueryValues:    64,
                },
            ),
        },
        Root: prefilter.Lookup(prefilter.StringQuery{
            Index:  "dimension_a_index",
            Values: prefilter.SeedStrings("dimension_a"),
        }),
    }

    plan, err := prefilter.Compile(config)
    if err != nil {
        return nil, err
    }
    store, err := prefilter.New(plan)
    if err != nil {
        return nil, err
    }

    seedDocID := uint32(1)
    seed := &common.Ticket{
        TicketID:  1001,
        CreatedAt: now,
        StringLists: map[string][]string{
            "dimension_a": {"a", "b"},
        },
    }
    candidateDocID := uint32(2)
    candidate := &common.Ticket{
        TicketID: 1002,
        StringLists: map[string][]string{
            "dimension_a": {"b"},
        },
    }

    if err := store.Add(seedDocID, seed); err != nil {
        return nil, err
    }
    if err := store.Add(candidateDocID, candidate); err != nil {
        return nil, err
    }

    session, err := store.BeginTick(prefilter.Facts{})
    if err != nil {
        return nil, err
    }
    result, err := session.Candidates(seedDocID, seed, prefilter.Facts{})
    if err != nil {
        return nil, err
    }

    // Prefilter 本身会返回 seed，因为 seed 也命中索引。
    // 上层应根据自己的建组流程排除 seed。
    result.Remove(seedDocID)
    return result.IDs(), nil
}
```

返回 `[2]`。

## 2. 基本生命周期

```text
build Config
  -> Compile once
  -> New once
  -> Add / Remove at execution boundaries
  -> BeginTick(tickFacts)
  -> Candidates(seedDocID, seedTicket, seedFacts) one or more active seeds
  -> discard TickSession
  -> Add / Remove again
```

必须遵守：

- Add 的 DocID 参数非零，Ticket 参数非 nil。
- 同一个 IndexStore 内 DocID 唯一。
- Candidates 的 seedDocID 必须已经 Add 且尚未 Remove。
- 同一个 owner goroutine 必须串行调用 Add、Remove、BeginTick 和 Candidates。
- TickSession 使用期间不能修改 IndexStore。
- 更新索引字段使用 Remove 后 Add。

## 3. Config

```go
type Config struct {
    Indexes              []IndexSpec
    Facts                []FactSpec
    Root                 Expr
    ContainsProbeThreshold uint64
}
```

| 字段 | 说明 |
| --- | --- |
| Indexes | 物理索引声明 |
| Facts | Tick 动态 Fact 契约 |
| Root | Prefilter 过滤表达式根节点，必填 |
| ContainsProbeThreshold | 小 scope Contains probe 阈值；0 默认 4096 |

`Compile` 成功意味着所有静态检查已通过。

## 4. Ticket 与 DocID

```go
type Ticket struct {
    TicketID    uint64
    CreatedAt   int64
    StringLists map[string][]string
    Uint64Lists map[string][]uint64
    Int64Values map[string]int64
}
```

- Prefilter 直接使用项目唯一的 `common.Ticket`，不再定义 Document。
- DocID 作为独立的 `uint32` 参数传入 Add/Candidates，只用于本地索引。
- 一个 string/uint64 字段可以携带多个 key。
- Int64Values 每个字段只有一个 int64。
- 字段名由上层定义，Prefilter 不解释含义。
- IndexStore 不保留完整 Ticket；调用者仍需自行保存业务对象和 DocID 映射。

## 5. string MultiValueIndex

### 5.1 注册索引

```go
stringIndex := prefilter.NewMultiValueIndex(
    prefilter.MultiValueIndexConfig{
        Name:                "dimension_a_index",
        Field:               "dimension_a",
        KeyType:             prefilter.KeyTypeString,
        MaxDocumentValues: 64,
        MaxQueryValues:    64,
    },
)
```

KeyType 留空时默认 string。

### 5.2 创建 Query

```go
lookup := prefilter.Lookup(prefilter.StringQuery{
    Index:  "dimension_a_index",
    Values: prefilter.SeedStrings("dimension_a"),
})
```

同一个 StringQuery 中的多个值固定采用 OR 语义。

### 5.3 Values 来源

```go
prefilter.LiteralStrings("a", "b")

prefilter.SeedStrings("dimension_a")

prefilter.FactStrings("extra_values")

prefilter.UnionStrings(
    prefilter.SeedStrings("dimension_a"),
    prefilter.FactStrings("extra_values"),
    prefilter.LiteralStrings("fallback"),
)
```

同一个 Query 最终产生的多个 key 使用 OR：

```text
posting[a] OR posting[b] OR posting[fallback]
```

## 6. uint64 MultiValueIndex

### 6.1 注册

```go
uint64Index := prefilter.NewMultiValueIndex(
    prefilter.MultiValueIndexConfig{
        Name:                "dimension_b_index",
        Field:               "dimension_b",
        KeyType:             prefilter.KeyTypeUint64,
        MaxDocumentValues: 64,
        MaxQueryValues:    64,
    },
)
```

### 6.2 查询

```go
lookup := prefilter.Lookup(prefilter.Uint64Query{
    Index: "dimension_b_index",
    Values: prefilter.UnionUint64s(
        prefilter.SeedUint64s("dimension_b"),
        prefilter.FactUint64s("extra_uint64_values"),
        prefilter.LiteralUint64s(1001, 1002),
    ),
})
```

物理 posting 的 key 是原生 uint64。

以下组合会在 Compile 报错：

- StringQuery 查询 KeyTypeUint64。
- Uint64Query 查询 KeyTypeString。

## 7. 两个 key 上限

```go
type MultiValueIndexConfig struct {
    MaxDocumentValues int
    MaxQueryValues    int
}
```

| 限制 | 对象 | 时机 |
| --- | --- | --- |
| MaxDocumentValues | 单个 Ticket 为这个索引字段产生的唯一 key 数 | IndexStore.Add 前 |
| MaxQueryValues | 一个 Query 为当前 seed 绑定的唯一 key 数 | 能静态推导时 Compile；否则 Candidates |

默认都是 64。

文档值和运行时 Query 都会先去重：

```go
StringLists: map[string][]string{
    "dimension_a": {"a", "a", "b"},
}
```

唯一 document key 数是 2。

编译器对 Union 的声明上限采用保守求和。即使不同来源运行时可能出现重复，只要声明最大值之和超过索引 Query 上限，Compile 就会返回 QUERY_KEY_CONTRACT。

## 8. Int64RangeIndex

### 8.1 注册

```go
numericIndex := prefilter.NewInt64RangeIndex(
    prefilter.Int64RangeIndexConfig{
        Name:  "numeric_index",
        Field: "numeric_value",
    },
)
```

### 8.2 固定闭区间

```go
lookup := prefilter.Lookup(prefilter.Int64RangeQuery{
    Index: "numeric_index",
    Min:   prefilter.LiteralInt64(100),
    Max:   prefilter.LiteralInt64(200),
})
```

命中条件是 `100 <= value <= 200`。

### 8.3 基于 seed 动态范围

```go
lookup := prefilter.Lookup(prefilter.Int64RangeQuery{
    Index: "numeric_index",
    Min: prefilter.SubInt64(
        prefilter.SeedInt64("numeric_value"),
        prefilter.LiteralInt64(50),
    ),
    Max: prefilter.AddInt64(
        prefilter.SeedInt64("numeric_value"),
        prefilter.LiteralInt64(50),
    ),
})
```

### 8.4 通用阶梯函数

`StepInt64` 可以对任意 `Int64Expr` 做阶梯映射，不只支持等待时间：

```go
radius := prefilter.StepInt64(
    prefilter.SeedInt64("numeric_value"),
    prefilter.Int64Step{At: -100, Value: 10},
    prefilter.Int64Step{At: 0, Value: 30},
    prefilter.Int64Step{At: 100, Value: 50},
)
```

绑定时选择最后一个满足 `At <= input` 的 `Value`。如果输入小于首个 `At`，返回首项 `Value`。规则如下：

- 至少提供一个 step。
- `At` 必须严格递增。
- 通用 `StepInt64` 允许负数 `At`。
- 构造函数会复制 steps，调用方后续修改原 slice 不会改变计划。

`input` 可以使用 `SeedInt64`、`FactInt64` 或其他组合后的 `Int64Expr`。`FactInt64` 不区分 Tick/Seed 来源，运行时从分层 Fact 中解析。

### 8.5 数值钳制

```go
boundedValue := prefilter.ClampInt64(
    prefilter.SeedInt64("numeric_value"),
    prefilter.LiteralInt64(0),
    prefilter.LiteralInt64(1_000),
)
```

`ClampInt64(value, min, max)` 的结果始终位于闭区间 `[min,max]`：低于下限返回下限，高于上限返回上限，否则返回原值。三个参数都可以是动态 `Int64Expr`。

如果两个字面量边界满足 `min > max`，Compile 直接拒绝；如果动态绑定后出现 `min > max`，Candidates 返回 error。系统不会自动交换边界，也不会把错误解释为 None 或全量候选。

### 8.6 使用普通 Fact 表达等待时间放宽

```go
radius := prefilter.StepInt64(
    prefilter.FactInt64("wait_millis"),
    prefilter.Int64Step{At: 0, Value: 10},
    prefilter.Int64Step{At: 30_000, Value: 50},
    prefilter.Int64Step{At: 60_000, Value: 100},
)

lookup := prefilter.Lookup(prefilter.Int64RangeQuery{
    Index: "numeric_index",
    Min: prefilter.SubInt64(
        prefilter.SeedInt64("numeric_value"),
        radius,
    ),
    Max: prefilter.AddInt64(
        prefilter.SeedInt64("numeric_value"),
        radius,
    ),
})
```

`wait_millis` 是普通 `int64` Fact，必须在 LogicalNode 的全链路 Fact 契约中声明，并由上层 `ObjectFactProvider(object, now, tickFacts)` 生成。当前 object 作为 Prefilter seed 时，这层 Object Facts 会传给 `Candidates`。Prefilter 不计算等待时间，也不限制字段名、非负性或溢出策略。

```go
spec.ObjectFactProvider = func(
    object *matchsystem.Ticket,
    now int64,
    tickFacts matchsystem.Facts,
) (matchsystem.Facts, error) {
    elapsed := now - object.CreatedAt // 负值与溢出策略由业务决定
    return matchsystem.Facts{
        Int64Values: map[string]int64{"wait_millis": elapsed},
    }, nil
}
```

AddInt64/SubInt64 是饱和运算，不会在范围边界加减时环绕。

## 9. 树形组合

### 9.1 And

```go
root := prefilter.And(lookupA, lookupB, lookupC)
```

语义是交集。无 inherited scope 时，执行器选择估算最小的正向子树开始。

### 9.2 Or

```go
root := prefilter.Or(lookupA, lookupB)
```

语义是并集。

### 9.3 Exclude

正确：

```go
root := prefilter.And(
    positiveLookup,
    prefilter.Exclude(excludedLookup),
)
```

错误：

```go
root := prefilter.Exclude(excludedLookup)
```

Exclude 不是全局反转。它只计算：

```text
current scope AND NOT excluded
```

Or 中的 Exclude 只有在外层已经提供 scope 时才合法：

```go
root := prefilter.And(
    positiveLookup,
    prefilter.Or(
        prefilter.Exclude(excludedLookup),
        extraLookup,
    ),
)
```

### 9.4 If

```go
root := prefilter.If(
    prefilter.GreaterOrEqual(
        prefilter.FactInt64("wait_millis"),
        prefilter.LiteralInt64(30_000),
    ),
    relaxedExpr,
    strictExpr,
)
```

If 只执行选中路径。未选路径不会：

- 绑定 Query。
- 读取 Fact。
- 访问 Index。

但两个路径仍会在 Compile 时校验，所以引用的 Index 和 Fact 都必须注册。

### 9.5 None

```go
root := prefilter.None()
```

恒为空。`And()` 和 `Or()` 不能用空 children 表达 None。

## 10. Fact

### 10.1 声明

```go
config := prefilter.Config{
    Indexes: indexes,
    Facts: []prefilter.FactSpec{
        {
            Name:          "extra_values",
            Type:      prefilter.FactTypeStrings,
            MaxValues: 8,
        },
        {
            Name:          "extra_uint64_values",
            Type:      prefilter.FactTypeUint64s,
            MaxValues: 8,
        },
        {
            Name: "numeric_radius",
            Type: prefilter.FactTypeInt64,
        },
    },
    Root: root,
}
```

### 10.2 引用

```go
prefilter.FactStrings("extra_values")
prefilter.FactUint64s("extra_uint64_values")
prefilter.FactInt64("numeric_radius")
```

### 10.3 传值

```go
session, err := store.BeginTick(prefilter.Facts{
    StringLists: map[string][]string{
        "extra_values": {"a", "b"},
    },
    Uint64Lists: map[string][]uint64{
        "extra_uint64_values": {1001, 1002},
    },
})

seedFacts := prefilter.Facts{
    Int64Values: map[string]int64{
        "numeric_radius": 25,
    },
}
result, err := session.Candidates(seedDocID, seedTicket, seedFacts)
```

Tick Facts 由 TickSession 只读借用，调用方必须保证在 Session 使用完毕前不修改；Seed Ticket 和 Seed Facts 在 `Candidates(seedDocID, seedTicket, seedFacts)` 的同步调用期间同样只读借用，不复制也不合并。两层不能出现任何同名字段；单层中同一个名字也不能跨 string、uint64、int64 类型重复。LogicalNode 调用链会先由 FactFrame 建立唯一一份自有拷贝，再交给 Prefilter 借用。

声明 Fact 但运行时未提供值是允许创建 TickSession 的；只有选中执行路径真正读取它时才返回 QUERY_BIND。

## 11. 完整复合配置

```go
indexes := []prefilter.IndexSpec{
    prefilter.NewMultiValueIndex(
        prefilter.MultiValueIndexConfig{
            Name:                "dimension_a_index",
            Field:               "dimension_a",
            KeyType:             prefilter.KeyTypeString,
            MaxDocumentValues: 64,
            MaxQueryValues:    64,
        },
    ),
    prefilter.NewMultiValueIndex(
        prefilter.MultiValueIndexConfig{
            Name:                "dimension_b_index",
            Field:               "dimension_b",
            KeyType:             prefilter.KeyTypeUint64,
            MaxDocumentValues: 64,
            MaxQueryValues:    64,
        },
    ),
    prefilter.NewMultiValueIndex(
        prefilter.MultiValueIndexConfig{
            Name:                "excluded_index",
            Field:               "excluded",
            KeyType:             prefilter.KeyTypeString,
            MaxDocumentValues: 64,
            MaxQueryValues:    64,
        },
    ),
    prefilter.NewInt64RangeIndex(
        prefilter.Int64RangeIndexConfig{
            Name:  "numeric_index",
            Field: "numeric_value",
        },
    ),
}

radius := prefilter.StepInt64(
    prefilter.FactInt64("wait_millis"),
    prefilter.Int64Step{At: 0, Value: 10},
    prefilter.Int64Step{At: 30_000, Value: 50},
)

root := prefilter.And(
    prefilter.Lookup(prefilter.StringQuery{
        Index:  "dimension_a_index",
        Values: prefilter.SeedStrings("dimension_a"),
    }),
    prefilter.Lookup(prefilter.Uint64Query{
        Index:  "dimension_b_index",
        Values: prefilter.SeedUint64s("dimension_b"),
    }),
    prefilter.Lookup(prefilter.Int64RangeQuery{
        Index: "numeric_index",
        Min: prefilter.SubInt64(
            prefilter.SeedInt64("numeric_value"),
            radius,
        ),
        Max: prefilter.AddInt64(
            prefilter.SeedInt64("numeric_value"),
            radius,
        ),
    }),
    prefilter.Exclude(
        prefilter.Lookup(prefilter.StringQuery{
            Index:  "excluded_index",
            Values: prefilter.SeedStrings("excluded_values"),
        }),
    ),
)

config := prefilter.Config{
    Indexes:              indexes,
    Facts: []prefilter.FactSpec{
        {Name: "wait_millis", Type: prefilter.FactTypeInt64},
    },
    Root: root,
    ContainsProbeThreshold: 4096,
}
```

集合语义：

```text
dimension_a 命中
AND dimension_b 命中
AND numeric_value 在动态闭区间
AND NOT excluded 命中
```

## 12. Compile 产物

```go
plan, err := prefilter.Compile(config)
if err != nil {
    return err
}

fingerprint := plan.Fingerprint()
requirements := plan.Requirements()
```

### Fingerprint

- 类型是 Fingerprint。
- 内容是 SHA-256 十六进制。
- 覆盖 过滤表达式、Query、动态值表达式、实际 Requirements 和 probe threshold。
- And/Or 子节点换序不改变 fingerprint。
- 默认 KeyTypeString 与显式 KeyTypeString 等价。

### Requirements

只包含 Root 实际使用的 Index 和 Fact。返回值是 slice 副本，修改 slice 不会改变 Plan。

## 13. IndexStore API

### 13.1 Add

```go
err := store.Add(docID, ticket)
```

可能失败：

- DocID 为 0。
- Ticket 为 nil。
- DocID 已 Active。
- 任一 MultiValue index 的文档 key 超限。

所有 index.validate 都通过后才真正写索引。

### 13.2 Remove

```go
removed := store.Remove(docID)
```

- Active 时清理所有索引并返回 true。
- 不存在时返回 false。

### 13.3 Len

```go
count := store.Len()
```

返回 Active cardinality，类型是 uint64。

### 13.4 TickSession

```go
session, err := store.BeginTick(tickFacts)
```

TickSession 只读借用 Tick Facts，并在创建时准备 Int64RangeIndex 的有序 distinct keys。它不保存 now；Seed Facts 在每次 Candidates 调用时单独传入。它引用 IndexStore 的 Active 和 posting，不是并发快照。

## 14. Candidates 与结果

```go
result, err := session.Candidates(seedDocID, seedTicket, seedFacts)
```

结果：

- 是调用方独占的可变 Bitmap。
- 只来自当前 IndexStore posting。
- 默认可能包含 seed。
- 按 Prefilter 语义产生，不包含最终业务校验。

常用操作：

```go
result.Remove(seedDocID)
result.Subtract(used)

count := result.Count()
ids := result.IDs()

result.ForEach(func(docID uint32) bool {
    // 访问上层 Ticket store。
    return true
})
```

`IDs` 和 `ForEach` 都按 DocID 升序。

## 15. 执行统计

```go
result, stats, err := session.CandidatesWithStats(seedDocID, seedTicket, seedFacts)
```

```go
type Stats struct {
    LookupCalls   uint64
    ContainsCalls uint64
    AndCalls      uint64
    OrCalls       uint64
    SubtractCalls   uint64
}
```

主要用于测试和观测执行策略：

- 小 scope 应出现 ContainsCalls。
- posting 查询增加 LookupCalls。
- Or 增加 OrCalls。
- Exclude 增加 SubtractCalls。

## 16. 错误处理

```go
var planErr *prefilter.Error
if errors.As(err, &planErr) {
    log.Printf(
        "phase=%s path=%s code=%s detail=%v",
        planErr.Phase,
        planErr.Path,
        planErr.Code,
        planErr.Err,
    )
}
```

常见 Compile code：

| Code | 原因 |
| --- | --- |
| MISSING_ROOT | Root 为空 |
| NIL_INDEX | IndexSpec 为 nil |
| INVALID_INDEX | 索引名或字段为空 |
| DUPLICATE_INDEX | 索引名重复 |
| INVALID_KEY_LIMIT | MultiValue 上限非正 |
| INVALID_KEY_TYPE | 非 string/uint64 |
| INVALID_FACT | Fact 声明非法 |
| DUPLICATE_FACT | Fact 名重复 |
| EMPTY_AND / EMPTY_OR | 空组合 |
| CYCLE | 过滤表达式循环 |
| EXCLUDE_REQUIRES_SCOPE | Exclude 没有正向 scope |
| MISSING_INDEX | Query 引用未注册索引 |
| QUERY_INDEX_MISMATCH | Query kind 与 Index kind 不一致 |
| QUERY_KEY_TYPE_MISMATCH | string/uint64 类型不一致 |
| MISSING_FACT | 动态值引用未声明 Fact |
| FACT_TYPE_MISMATCH | Fact 类型不一致 |
| QUERY_KEY_CONTRACT | 静态最大 query keys 超过合同 |

常见 Candidates code：

| Code | 原因 |
| --- | --- |
| INVALID_TICK_SESSION | nil/未初始化 TickSession |
| INACTIVE_SEED | seed DocID 不在 Active |
| QUERY_BIND | seed 字段或运行时 Fact 缺失 |
| QUERY_KEY_LIMIT | 动态 query key 超限 |
| INVALID_RANGE | Min 大于 Max |
| CONDITION | If 条件绑定失败 |
| INDEX_LOOKUP / INDEX_CONTAINS | 物理索引查询异常 |

错误不会被转换成 None，也不会触发全池扫描。

## 17. 单 owner goroutine

Prefilter 没有 mutex、channel、atomic 状态切换或并发快照。下面的完整命令序列必须由同一个 owner goroutine 顺序执行：

```text
owner goroutine:
  apply Add / Remove
  session, err := store.BeginTick(tickFacts)
  session.Candidates(seedDocID A, seedTicket A, seedFacts A)
  session.Candidates(seedDocID B, seedTicket B, seedFacts B)
  discard session
  apply next Add / Remove
```

禁止让 goroutine A 执行 Add/Remove，同时让 goroutine B 执行 BeginTick/Candidates；也禁止把不同 seed 的 Candidates 分派到多个 goroutine。外部网络层如果并发接收请求，必须在进入 Prefilter 之前完成串行化。

## 18. 更新和删除

IndexStore 不提供 Update：

```go
if store.Remove(oldDocID) {
    err := store.Add(newDocID, newTicket)
    if err != nil {
        // 调用方需要决定如何恢复旧文档。
    }
}
```

注意这个操作本身不是事务。上层若要求更新失败时保留旧数据，需要先验证新 Ticket 索引字段或实现自己的恢复逻辑。

## 19. 从固定契约生成 JSON Prefilter

Index/Fact 契约与 Prefilter 计划是两份独立 JSON。先加载契约文件：

```json
{
  "schemaVersion": "logical-node-contract/v1",
  "indexes": [
    {"type":"multi_value","name":"mode","field":"mode","keyType":"string","maxDocumentValues":2,"maxQueryValues":4},
    {"type":"int64_range","name":"rating","field":"rating"}
  ],
  "facts": [
    {"name":"mode_keys","type":"strings","maxValues":2},
    {"name":"wait_millis","type":"int64"}
  ]
}
```

```go
contract, err := prefilter.ParseLogicalNodeContract(contractJSON, prefilter.JSONLimits{})
if err != nil {
    return err
}
jsonCompiler, err := prefilter.NewJSONCompiler(contract)
```

随后加载完全独立的计划 JSON。它只能引用契约中已经固定的名字：

```json
{
  "schemaVersion": "prefilter/v1",
  "plan": {
    "type": "lookup",
    "query": {
      "type": "multi_value",
      "index": "mode",
      "values": {"type": "fact_strings", "fact": "mode_keys"}
    }
  },
  "runtime": {"containsProbeThreshold": 4096}
}
```

```go
config, err := jsonCompiler.Parse(planJSON)
plan, err := jsonCompiler.Compile(planJSON)
```

接入 LogicalNode 时还要把同一份全链路 Fact 契约放到节点配置：

```go
nodeConfig := matchsystem.LogicalNodeConfig{
    Facts:     contract.Facts,
    Prefilter: config,
}
```

`ParseLogicalNodeContract` 校验索引类型、KeyType、字段、文档/查询容量、Fact 类型和多值上限。`JSONCompiler.Parse` 校验封闭 tagged union 及所有索引/Fact 引用，并返回普通 typed `Config`。`Compile` 在此基础上复用 `Compile(Config)`；JSON 与 typed 构造路径具有相同 Requirements、Fingerprint 和候选语义。Fact 契约属于 LogicalNode 全匹配链，CandidateScore 和 GroupEvaluator 也能读取 Object Facts；Prefilter 只读取它实际引用的字段。

契约和计划都执行重复 key、未知字段、`null`、尾随值及资源上限检查。契约 JSON 中出现 `plan`，或计划 JSON 中出现 `indexes` / `facts`，都会返回 `UNKNOWN_FIELD`。

## 20. 性能调节

### ContainsProbeThreshold

- 小 scope：逐 DocID Contains，避免创建大 posting union。
- 大 scope：Lookup + Bitmap AND。
- 默认 4096。

### Index 选择

- 离散多值：MultiValueIndex。
- 单 int64 闭区间：Int64RangeIndex。
- 多个独立约束：And。
- 备选关系：Or。
- 排除：正向 And 中的 Exclude。

### 不要提前物化

Prefilter 返回 DocSet。上层应在完成所有 Lookup 后再按 DocID 读取 Ticket，并在需要时执行 Top-L。

## 21. 当前没有的能力

- 自定义 Expr/Query/IndexSpec 外部实现；接口刻意封闭。
- 热更新 generation。
- 自动排除 seed。
- used 集合管理。
- 候选评分与 Top-L。
- GroupEvaluator 和建组。
- IndexStore Update 事务。
- 并发调用或跨 goroutine 执行。
- 扫描回退。
