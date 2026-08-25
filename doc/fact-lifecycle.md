# Fact 生命周期、分层契约与缓存

本文说明统一后的 Fact 模型，以及 Tick Facts、Object Facts、FactFrame、FactView 和 Prefilter 之间的所有权与生命周期关系。

## 1. 唯一 Fact 模型

Fact 的唯一实现位于 `internal/matchsystem/fact` 包。`matchsystem` 和 `prefilter` 暴露的 `FactType`、`FactSpec`、`Facts` 都是这个包的类型别名，不会产生第二套结构、转换或拷贝。

```go
package fact

type Type uint8

const (
    TypeStrings Type = iota + 1
    TypeInt64
    TypeUint64s
)

type Spec struct {
    Name      string
    Type      Type
    MaxValues int
}

type Values struct {
    StringLists map[string][]string
    Uint64Lists map[string][]uint64
    Int64Values map[string]int64
}
```

在上层也可以使用等价的兼容名称：

```go
// matchsystem.FactSpec is an alias of fact.Spec.
// matchsystem.Facts    is an alias of fact.Values.
// prefilter.FactSpec   is an alias of fact.Spec.
// prefilter.Facts      is an alias of fact.Values.
```

这些是 Go 类型别名，不是运行时包装。

### Type、Spec 和 Values

| 类型 | 数据位置 | `MaxValues` 规则 |
| --- | --- | --- |
| `TypeStrings` | `StringLists[name] []string` | 必须声明正数上限 |
| `TypeUint64s` | `Uint64Lists[name] []uint64` | 必须声明正数上限 |
| `TypeInt64` | `Int64Values[name] int64` | 单值，不声明上限 |

`Spec` 是 LogicalNode 全链路的 Fact 契约。它不仅服务 Prefilter，也约束评分、GroupEvaluator 和其他读取 `FactView` 的阶段。`Values` 的 map 和 slice 在交给 Frame、TickSession 或回调后都必须按只读数据处理。

## 2. Tick 与 Object 两个 scope

Fact 值分为两个运行时层：

```text
Tick Facts（本次 ProduceMatch 共享）
    + Object Facts（按 TicketID 懒加载）
        -> 当前 Ticket 作为 Seed 时传给 Prefilter
        -> 当前 Ticket 作为 Candidate 时供评分/评估读取
```

### Tick Facts

`FactProvider` 在每次 `ProduceMatch` 中最多调用一次，接收匹配轮次固定的 `now`，返回本次尝试共享的 Tick 层：

```go
type FactProvider func(ctx context.Context, now int64) (fact.Values, error)
```

它适合表达本次产出尝试的容量、活动状态或其他全局动态值。返回值进入 Frame 后会被深拷贝，Provider 可以在返回后复用或修改自己的 map 和 slice。

### Object Facts

`ObjectFactProvider` 为当前 Ticket 生成 Object 层：

```go
type ObjectProvider func(
    object *common.Ticket,
    now int64,
    tick fact.Values,
) (fact.Values, error)
```

它收到只读 Ticket 和只读 Tick 层。一个 Ticket 在一次 `ProduceMatch` 中第一次作为 Seed 或 Candidate 使用时生成 Object Facts，后续读取复用同一结果。Object Facts 不能与 Tick Facts 使用同名字段。

旧的 `SeedFactProvider` 只是兼容名称，当前语义同样适用于 seed 和 candidate，不能与 `ObjectFactProvider` 同时配置。

## 3. FactFrame：一次产出尝试的唯一拥有者

`fact.Frame` 是一次 `ProduceMatch` 的 Fact 生命周期容器：

```go
frame, err := fact.NewFrame(tickValues, specs)
if err != nil {
    return err
}

seedValues, err := frame.Object(seed, now, objectProvider)
view := frame.View()
```

Frame 的行为是：

1. `NewFrame` 对 Tick Values 做唯一一次深拷贝，并立即按 Spec 校验类型、字段和容量。
2. `frame.Tick()` 返回 Frame 自己拥有的 Tick 层，Prefilter 只读借用这一份。
3. `frame.Object(ticket, ...)` 第一次调用 Provider；成功后对 Provider 返回值深拷贝一次，再校验并按 TicketID 缓存。
4. 同一个 TicketID 后续调用直接返回缓存值；Provider 的成功结果和失败结果都会缓存，避免一次尝试内重复调用。
5. `frame.View()` 返回只读视图，评分和 GroupEvaluator 使用同一个 Tick/Object 数据源。
6. `ProduceMatch` 返回后 Frame 及本次 Object 缓存释放；View、Values 和其中的 map/slice 不能跨调用保存。

对象 Fact 的数据路径如下：

```text
ObjectFactProvider(Ticket, Tick)
       -> Frame.Clone
       -> Frame.Validate
       -> objects[TicketID]
       +-> Prefilter.Candidates(..., seedFacts)
       +-> CandidateScoreContext.Facts
       `-> GroupEvaluatorContext.Facts
```

## 4. Prefilter 的借用边界

Prefilter 不拥有 Fact 数据：

```go
session, err := store.BeginTick(frame.Tick()) // 借用 Tick
docs, err := session.Candidates(seedDocID, seedTicket, seedFacts) // 借用 Seed/Object
```

具体约束：

- `IndexStore.BeginTick` 只读借用 Tick Facts，并把引用保存到 TickSession；Session 使用完前不能修改这份 Values。
- `TickSession.Candidates` 只读借用本次同步调用的 seed Ticket 和 Seed Facts，不复制也不合并。
- TickSession 不是并发快照；它引用 IndexStore 的 Active、posting 和 Tick Facts。
- 完整 LogicalNode 调用链中，Tick 的唯一深拷贝已经由 Frame 完成，Prefilter 不再重复复制。
- Prefilter 只根据 Fact 值绑定查询，不负责计算 Object Facts，也不负责最终组合法性。

独立使用 Prefilter 时，调用方必须自己保证上述借用生命周期；使用 LogicalNode 时由 owner goroutine 和 Frame 生命周期统一保证。

## 5. 契约、类型和错误

一个 Fact 字段必须满足以下条件：

- 出现在 `Spec` 契约中；
- 只出现在与 Spec 一致的类型 map 中；
- string/uint64 多值字段不超过 `MaxValues`；
- 同一个层内不能在多个类型 map 中重复；
- Tick 与 Object/Seed 层不能出现同名字段。

Frame 使用结构化 `fact.Error` 返回错误：

```go
type Error struct {
    Path string
    Code string
    Err  error
}
```

常见 Code 包括：

| Code | 含义 |
| --- | --- |
| `FACT_TYPE_COLLISION` | 同一层同名字段出现在多个类型 map |
| `UNDECLARED_FACT` | 运行时值没有出现在全链路契约 |
| `FACT_TYPE_MISMATCH` | 运行时 map 类型与 Spec 不一致 |
| `FACT_VALUE_LIMIT` | 多值字段超过 Spec 的 `MaxValues` |
| `FACT_SCOPE_COLLISION` | Tick 与 Object/Seed 使用同名字段 |
| `NIL_OBJECT` | Object Provider 收到 nil Ticket |

Prefilter 会把通用 Fact 错误适配成带 `Phase=evaluate` 的 `prefilter.Error`；计划编译阶段的 Fact 声明错误仍由 Prefilter 以 `compile` 阶段错误报告。配置中 `LogicalNodeConfig.Facts` 与 `Prefilter.Facts` 如果同时填写，必须是同一份契约；只填写一处时 LogicalNode 会把它同步到另一处。

## 6. 为什么不做 MatchRound 级 FactFrame 缓存

FactFrame 当前按一次 `ProduceMatch` 创建，而不是在 `BeginMatchRound` 时创建并持有到整轮结束，原因有两个：

1. Tick Provider 的值可能反映外部容量、开关或服务状态；即使轮次的 `now` 固定，前一次成功匹配也可能改变下一次产出的可用事实。
2. Object Facts 是按实际访问懒加载的。整轮缓存会把大量已经评估过的 Ticket Facts 延长到轮次结束，并让缓存占用随整轮候选数量增长。

因此当前语义是：同一次 `ProduceMatch` 内复用 Tick 和 Object Facts，下一次 `ProduceMatch` 重新调用 Provider、重新建立 Frame。匹配轮次只固定 Seed 顺序和游标，不固定 Fact 快照。

## 7. 单 owner 协程和生命周期

Fact 包没有 mutex、atomic 或并发快照。Frame、View、TickSession 和 Provider 回调都由同一个 owner goroutine 同步使用：

```text
owner goroutine:
  生成 Tick Facts
  -> NewFrame（复制 Tick）
  -> Object（复制并缓存 Object）
  -> BeginTick / Candidates（Prefilter 借用）
  -> Score / GroupEvaluator（读取 View）
  -> ProduceMatch 返回，释放 Frame
```

Provider 和评估回调不得：

- 修改传入的 Ticket、Tick Facts 或 View 中的 map/slice；
- 将 Values、View 或 Ticket 借用指针交给其他 goroutine；
- 在回调中重入同一个 PhysicalNode/LogicalNode；
- 在 TickSession 仍使用时修改 Tick Facts 或 IndexStore。

外部网络层可以并发接收请求，但必须在进入匹配核心前完成串行化。轮次中的 Add/Remove 也由同一 owner 协程执行，并在多次 `ProduceMatch` 之间保持不变。

## 8. 完整示例

下面示例展示统一 Fact 包、契约、一次 Frame 和 Prefilter 借用关系：

```go
specs := []fact.Spec{
    {Name: "capacity", Type: fact.TypeInt64},
    {Name: "tags", Type: fact.TypeStrings, MaxValues: 8},
}

tick := fact.Values{
    Int64Values: map[string]int64{"capacity": 3},
}

frame, err := fact.NewFrame(tick, specs)
if err != nil {
    return err
}

objectProvider := fact.ObjectProvider(func(
    ticket *common.Ticket,
    now int64,
    tick fact.Values,
) (fact.Values, error) {
    return fact.Values{
        StringLists: map[string][]string{
            "tags": ticket.StringLists["tags"],
        },
    }, nil
})

seedFacts, err := frame.Object(seedTicket, now, objectProvider)
if err != nil {
    return err
}

session, err := store.BeginTick(frame.Tick())
if err != nil {
    return err
}
candidateDocs, err := session.Candidates(seedDocID, seedTicket, seedFacts)
```

生产代码通常使用 `matchsystem.Facts`、`matchsystem.FactSpec` 和 `matchsystem.ObjectFactProvider` 这些别名；它们与示例中的 `fact.Values`、`fact.Spec` 和 `fact.ObjectProvider` 完全相同。

## 9. 使用不变量

1. Fact 类型、Spec 和 Values 只有 `internal/matchsystem/fact` 一套真实定义。
2. Tick Facts 在一次 ProduceMatch 中只深拷贝一次；Object Facts 每个 TicketID 至多深拷贝一次。
3. 同一 TicketID 在同一次 ProduceMatch 中重复读取，必须命中 Frame 缓存。
4. Tick、Object/Seed 和 Prefilter 共享的是只读值，不共享可变所有权。
5. 每个运行时字段都必须符合全链路 Fact 契约，且两个 scope 不能同名。
6. FactView、Values 和 TickSession 不能脱离其同步回调或 Frame 生命周期使用。
7. Fact 错误必须保留结构化 Path/Code；不能把错误当成空候选或静默缺省值。
8. 新一轮不会复用上一轮的 FactFrame；每次 ProduceMatch 都重新建立本次尝试的 Fact 生命周期。

相关实现：[fact 模型](../internal/matchsystem/fact/fact.go)、[Frame 与校验](../internal/matchsystem/fact/frame.go)、[LogicalNode 调用链](../internal/matchsystem/logical_node_core.go)、[Prefilter Fact 适配](../internal/matchsystem/prefilter/fact_adapter.go)。
