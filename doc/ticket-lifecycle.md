# Ticket 生命周期、所有权与 DocID

本文说明当前匹配核心中 Ticket 的唯一数据模型、入池和出池语义，以及 TicketID、DocID 和 Prefilter 之间的边界。

## 1. 核心结论

项目中只有一套 Ticket 数据模型：`internal/common.Ticket`。`internal/matchsystem.Ticket` 只是类型别名，Prefilter 直接接收 `*common.Ticket`，不会再构造 Document 或其他 Ticket 投影。

```go
type TicketID = uint64

type Ticket struct {
    TicketID    TicketID
    CreatedAt   int64
    StringLists map[string][]string
    Uint64Lists map[string][]uint64
    Int64Values map[string]int64
}
```

字段含义由业务和匹配规则解释，核心只约束数据形状：

| 字段 | 作用 |
| --- | --- |
| `TicketID` | 跨调用边界稳定的业务身份；必须是非零 `uint64`，同一 LogicalNode 内不能重复 |
| `CreatedAt` | 创建时间戳；可供等待时间、Seed 顺序等上层逻辑使用 |
| `StringLists` | 按名称存放 string 多值字段 |
| `Uint64Lists` | 按名称存放 uint64 多值字段 |
| `Int64Values` | 按名称存放单值 int64 字段 |

`TicketID` 是身份，不是索引位置。它可以是业务生成的 uint64 GUID，但不能替代 Prefilter 所需的 `uint32 DocID`。

## 2. Ticket、storedTicket 与 DocID

`Ticket` 故意不包含 `DocID`。DocID 是一个 LogicalNode 内部的、非零的 `uint32` 索引编号，只用于 Active Bitmap、倒排索引和候选集合：

```text
TicketID(uint64)  <->  LogicalNode 的 ticketIDToDocID  <->  DocID(uint32)
                                                               |
                                                               +-> Prefilter 索引
```

LogicalNode 内部的 `storedTicket` 把业务对象和索引元数据组合起来：

```go
type storedTicket struct {
    *Ticket
    docID        uint32
    arrivalIndex int
}
```

其中 `docID` 和 `arrivalIndex` 不会暴露给 Ticket 调用方或 SeedOrderPolicy。Prefilter 只保存 DocID 及索引所需的键，不负责 TicketID 到 DocID 的映射。

DocID 的分配规则如下：

1. 新 LogicalNode 从 `1` 开始分配，`0` 永远无效。
2. Remove 或匹配成功后，DocID 从 Active、全部物理索引和节点映射中移除，并进入空闲栈。
3. 后续 Add 优先复用空闲栈中的 DocID；没有空闲 ID 时才递增分配。
4. `uint32` 空间耗尽时返回错误，不会把 `0` 当作有效 DocID。
5. DocID 只在本 LogicalNode 内有意义，不能跨 LogicalNode、PhysicalNode 或进程持久化使用。

轮次快照保存的是 DocID 顺序。匹配成功时，`ProduceMatch` 内部仍会同步移除匹配成员并回收 DocID；为避免回收的 DocID 在旧 Seed 快照仍被消费时重新指向另一张 Ticket，外部新增应延迟到轮次边界，或明确保证本轮回收的 DocID 不会被复用。实现仍会防御性地跳过已经删除但尚未复用的 stale DocID。

## 3. 入池：Add 只深拷贝一次

调用方交给 `LogicalNode.Add` 或 `PhysicalNode.Add` 的指针不归匹配池所有。Add 会执行以下步骤：

```text
调用方 Ticket
    -> 校验 nil、TicketID 非零、TicketID 不重复
    -> common.CloneTicket（唯一一次深拷贝）
    -> 分配本地 DocID
    -> Prefilter.IndexStore.Add(DocID, 同一个池内 Ticket 指针)
    -> 保存 storedTicket、TicketID 映射、到达顺序和 oldest heap
```

`common.CloneTicket` 会复制三张 map，并复制 string/uint64 slice；Add 返回后，调用方可以复用或修改原始 Ticket，不会影响池内对象。Prefilter 在 Add 时直接读取这份池内 Ticket，不再产生第二份 Document 或 Ticket 拷贝。

如果 DocID 分配、索引字段校验或索引写入失败，Ticket 不会进入 Active；已分配的 DocID 会归还空闲栈。成功入池后，池内 Ticket 按不可变数据处理，调用方不能通过回调修改它。

## 4. 读取、删除和匹配成功

### Get：深拷贝快照

`Get` 返回池内 Ticket 的独立深拷贝，不会暴露 LogicalNode 内部指针：

```go
ticket, ok := node.Get(ticketID)
if ok {
    // 可以安全修改或跨调用保存这个副本
    log.Printf("ticket=%d", ticket.TicketID)
}
```

每次 `Get` 都会生成新的 Ticket 及其 map/slice 副本。调用方修改返回值不会影响池内数据，多次 `Get` 也不会返回同一个指针。匹配流程内部使用不导出的 `lookupTicket` 借用池内指针；匹配成功后再将该指针转移给 `Match`，因此 Get 的防御性拷贝不会增加匹配路径的拷贝次数。

Get 返回的是调用方拥有的副本，可以安全修改、保存或交给异步流程。它不会随下一条 owner 命令失效，也不会影响池内 Ticket。只有匹配核心内部的 `lookupTicket` 返回借用指针，该指针不能跨 owner 命令使用。

### Remove：删除并丢弃

`Remove(ticketID)` 按 TicketID 找到 DocID，清理 Prefilter、Active、到达顺序、两个映射和 DocID 空闲栈，池内 Ticket 不返回给调用方。调用方如果需要保留业务数据，应在 Remove 前自行持有一份自己的数据。

### 匹配成功：同一指针转移所有权

合法组确定后，LogicalNode 会先从池和全部索引移除成员，再返回：

```go
type Match struct {
    Tickets []*common.Ticket
}
```

`Match.Tickets` 中的指针就是池内原指针，没有再次拷贝。返回成功后，调用方取得这些 Ticket 的完整所有权，可以保存、序列化或交给后续流程。匹配结果中的 Ticket 已经不再属于 LogicalNode，不能再用它们访问池内状态。

因此，三种路径的所有权语义是：

| 操作 | 结果 |
| --- | --- |
| `Add` | 调用方数据复制一份，池拥有副本 |
| `Get` | 返回独立深拷贝，调用方拥有副本 |
| `Remove` | 池内对象删除并丢弃 |
| 匹配成功 | 池内指针从池转移给 `Match` |

## 5. Prefilter 如何使用 Ticket

Prefilter 的稳定入口是：

```go
err := store.Add(docID, ticket)
session, err := store.BeginTick(tickFacts)
docs, err := session.Candidates(seedDocID, seedTicket, seedFacts)
```

这里的 `ticket` 是 `*common.Ticket`，`docID` 是独立参数。Prefilter：

- Add 时只读取 `StringLists`、`Uint64Lists` 等索引字段并写入 posting；
- 不读取或解释 TicketID 的业务语义；
- 不保存第二份完整 Ticket；
- Candidates 返回 DocID 集合，由 LogicalNode 再通过 `ticketsByDocID` 读取 `storedTicket`；
- 不在缺少索引时扫描整个 Ticket 池作为回退。

因此完整 Ticket 在 LogicalNode 中只保留一份，TicketID 仍由上层规则和结果使用，DocID 只承担本地索引定位。

## 6. 单 owner 协程与轮次边界

PhysicalNode、LogicalNode、TicketStore、Active、索引和 Seed 状态由同一个 owner goroutine 顺序驱动，核心没有 mutex、并发快照或跨协程所有权交接。以下命令不能分派给不同 goroutine：

```text
Add / Remove / Get / BeginMatchRound / ProduceMatch / BeginDrain / Stop
```

外部入口可以接收并发请求，但必须在进入匹配核心前串行化。一次匹配轮次通常是：

```text
BeginMatchRound（构建顺序并重置游标）
  -> 多次 ProduceMatch
  -> 下一轮边界前集中处理 Add / Remove
```

本轮的 Seed 顺序和游标不会因失败重置；一个 Seed 被尝试后，即使 Prefilter、Fact 或最终规则失败，本轮也不会再次选择。详见 [Seed 顺序策略与匹配轮次](seed-order-policy.md)。

## 7. 使用不变量

实现和调用方都应保持以下不变量：

1. `TicketID != 0`，且在一个 LogicalNode 内唯一。
2. Active 中每个 DocID 恰好对应一个 `storedTicket`，每个池内 Ticket 恰好对应一个 DocID。
3. DocID 不作为跨节点业务身份，也不写入通用 Ticket。
4. Add 后池内 Ticket 不可变；Prefilter 不复制它。
5. Get 返回的深拷贝可以由调用方独立修改或持有；只有内部 `lookupTicket` 借用指针不能跨 owner 命令使用。
6. Remove 和匹配成功必须同步清理 Active、全部索引和 TicketID/DocID 映射。
7. Match 结果中的 Ticket 指针只属于结果调用方，不再属于 LogicalNode。
8. DocID 回收只能在 owner 协程内进行，并遵守轮次边界。

相关实现：[common.Ticket](../internal/common/ticket.go)、[LogicalNode Ticket 流程](../internal/matchsystem/logical_node_core.go)、[Prefilter Store](../internal/matchsystem/prefilter/store.go)。
