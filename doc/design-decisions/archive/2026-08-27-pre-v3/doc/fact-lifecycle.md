# Fact 生命周期、作用域与所有权

本文说明 Tick、Object、Match 三类 Fact 的契约、缓存和所有权。字段声明见
[logical-node-contract/v2](logical-node-contract-v2.md)，Match Fact 更新见
[Evaluation/v2](evaluation-layer.md)。

## 统一模型

唯一实现位于 `internal/matchsystem/fact`；上层包暴露的是别名。

```go
type Spec struct {
    Name string
    Type Type
    Scope Scope
    MaxValues int
}
type Values struct {
    StringLists map[string][]string
    Uint64Lists map[string][]uint64
    Int64Values map[string]int64
}
```

`strings`、`uint64s` 必须声明正数 `MaxValues`；`int64` 必须为零。
Scope 是 `tick`、`object`、`match` 之一。v2 contract 要求 Fact 名称在三个
scope 间全局唯一，也不能与属性同名。缺失值不会被解释为空列表或零值。

## 三个 scope

```text
FactProvider(now) -> Tick Facts（一次 ProduceMatch 共享）
  |
  +-> ObjectFactProvider(ticket, now, tick)
  |     -> Object Facts（按 TicketID 惰性生成和缓存）
  |
  -> Evaluation.Initialize(seed, tick)
        -> Match Facts（待构建 Match 的聚合状态）
             -> Evaluation.OnJoin(old match, candidate, ...)
```

Tick Fact 表达本次尝试的全局动态值。Object Fact 表达对象动态值；同一 Ticket 首次作为
seed 或 candidate 使用时生成，成功值和 provider 错误都按 TicketID 缓存。

Match Fact 不由 provider 或 Fact Frame 生成。Evaluation 在选定 seed 后初始化全部字段；
候选通过 Join 后，`onJoin` 的全部右值读取同一旧快照，全部计算和校验成功后才与
候选一起原子提交。Complete 只能读取 Match 与 Tick Fact，不能遍历成员。

## Frame 与 Prefilter

`fact.Frame` 只拥有 Tick/Object 生命周期：

1. `NewFrame` 深拷贝并校验 Tick Values。
2. `Object` 首次调用 provider，深拷贝、校验并缓存结果；之后复用。
3. `Tick` 和 `View` 是同步调用期间的只读借用。
4. `ProduceMatch` 返回后 Frame、View 和对象缓存一起释放。

Frame 不持有 Match Fact。LogicalNode 把 Frame 的 Tick/Object 值和 Evaluation 管理的
Match 快照分别交给对应阶段。

```go
session, err := store.BeginTick(frame.Tick())
docs, err := session.Candidates(seedDocID, seedTicket, seedFacts)
```

Prefilter 只借用 Tick 和 seed Object Fact；不读取 candidate Object Fact 或 Match Fact，
也不计算组合合法性。`TickSession` 不是并发快照。独立使用 Prefilter 时由调用方
保证借用寿命；LogicalNode 中由 owner goroutine 和 Frame 保证。

Frame 按一次 `ProduceMatch` 而非整个 MatchRound 创建：前一次成功匹配可能改变外部
容量；对象缓存跨整轮还会延长大量 Ticket Fact 的存活。

## 校验与错误

`fact.Error` 保留 `Path`、`Code` 和底层 `Err`。常见 code：

| Code | 含义 |
| --- | --- |
| `FACT_TYPE_COLLISION` | 同名值出现在多个类型 map |
| `UNDECLARED_FACT` | 值未在 contract 声明 |
| `FACT_TYPE_MISMATCH` | 值类型与声明不符 |
| `FACT_SCOPE_MISMATCH` | 值用于错误 scope |
| `FACT_VALUE_LIMIT` | 多值字段超过上限 |
| `FACT_SCOPE_COLLISION` | 不同运行时 scope 出现同名字段 |
| `NIL_OBJECT` | Object provider 收到 nil Ticket |

Prefilter 将 Fact 错误适配为 `Phase=evaluate` 的 `prefilter.Error`；
Evaluation 使用自己的 phase/path/code 包装。

## 所有权

- Provider 返回后可修改自己的 map/slice；Frame 已深拷贝。
- scorer 回调收到隔离的可变视图；表达式 resolver 只读借用当前快照。
- 成功结果的 `common.Match.Facts` 对 map 和 slice 深拷贝。
- `common.Match.Tickets` 沿用池内 Ticket 指针的所有权转移，不因 MatchFacts
  深拷贝而复制。
- Frame、View、Values 和 Ticket 借用指针不能跨同步回调保存或交给其他 goroutine。

匹配核心使用单 owner goroutine；provider/scorer 不得重入同一个 PhysicalNode 或
LogicalNode。

## 示例

```go
specs := []fact.Spec{
    {Name: "capacity", Type: fact.TypeInt64, Scope: fact.ScopeTick},
    {Name: "tags", Type: fact.TypeStrings, Scope: fact.ScopeObject, MaxValues: 8},
}
frame, err := fact.NewFrame(fact.Values{
    Int64Values: map[string]int64{"capacity": 3},
}, specs)
if err != nil {
    return err
}
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

当前对应实现：[Fact 模型](../../../../../internal/matchsystem/fact/fact.go)、
[Frame 与校验](../../../../../internal/matchsystem/fact/frame.go)、
[LogicalNode 调用链](../../../../../internal/matchsystem/logical_node.go)。
