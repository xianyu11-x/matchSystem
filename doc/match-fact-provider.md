# Match Fact Provider

`MatchFactProvider` 是 Match scope Fact 的唯一生成和更新边界。它不是 patch 接口：
`Initialize` 与 `OnJoin` 每次都返回一份满足 Contract 的完整 Match Fact 快照。
Evaluation 只能读取快照，LogicalNode 不能自行增删字段或重算已有 Match 成员。

## 接口

```go
import (
    "context"

    "matchSystem/internal/matchsystem/fact"
)

type MatchFactProvider interface {
    Initialize(context.Context, fact.InitializeInput) (fact.Values, error)
    OnJoin(context.Context, fact.JoinInput) (fact.Values, error)
}
```

在 `matchsystem` 顶层使用时，`matchsystem.MatchFactProvider`、
`matchsystem.InitializeInput`、`matchsystem.JoinInput` 和 `matchsystem.Facts` 是指向
上述 `fact` 包类型的别名；接口本身的实际返回类型是 `fact.Values`。

`InitializeInput` 提供：`Now`、seed Ticket、seed Object Fact、Tick Fact。
`JoinInput` 另外提供 candidate Ticket、candidate Object Fact，以及加入前的完整
`MatchFactsBefore`。代码保证传入的是本次回调专用的 deep clone，因此 Provider 对这些
副本的修改不会反向改变 owner 状态。Go 接口本身不能阻止实现保留指针或修改副本；
接口约定 Provider 应将输入当作只读、call-scoped 值，不应跨调用保存它们。

## 生命周期

```text
seed Object Fact
  -> Provider.Initialize
  -> validate complete Match Fact
  -> CanComplete
  -> [CanJoin == true]
       -> Provider.OnJoin
       -> validate complete next Match Fact
       -> atomically accept candidate + next snapshot
       -> CanComplete
```

Contract 声明至少一个 `scope: "match"` Fact 时，`LogicalNodeSpec` 必须提供一个非
nil Provider；没有 Match scope Fact 时不调用 Provider。一次 seed 尝试只有在
`CanJoin` 返回 true 后才调用 `OnJoin`。已有成员不会再次参与 Provider 输入或表达式
遍历；新的聚合值由 Provider 根据它收到的快照计算。

## 完整快照规则

Provider 返回值必须同时满足：

- 每个键都在 Contract 声明中，且类型、scope、`MaxValues` 正确；
- 所有 `scope: "match"` 声明都出现；多值 Fact 可以用空 slice 表示空集合，但不能
  省略键；
- 不在另一个类型 map 重复出现，不包含其它 scope 的 Fact，不超过值上限。

LogicalNode 在 `Initialize`/`OnJoin` 返回后调用 `Validator.CloneValidatedMatch`：先
完整校验，再 deep clone 到 owner 内部。Provider 的 map/slice 后续变化不能改变当前
Match；提交给 `common.Match.Facts` 时还会复制一次。

## 原子性与失败语义

在 callback 成功且完整校验通过前，当前 group 和旧 Match Fact 保持不变。失败时：

- Provider 返回 error：包装为 `PROVIDER_ERROR`；
- Provider panic：包装为 `PROVIDER_PANIC`；
- context 已取消或 deadline：包装为 `PROVIDER_CANCELED`；
- 快照缺失、类型/scope 错误、重复键或超限：返回对应结构化 Fact 错误。

上述情况都不 patch、不接受 candidate、不使用旧快照补全、不静默重试，也不回退到
Evaluation 或其它写入路径。当前 `ProduceMatch` 调用 fail closed；seed 的轮次消费
规则仍由 LogicalNode 的 cursor 保证。

`CanComplete == false` 只表示当前完整快照尚未达到完成条件；已经成功接受的临时
candidate 和快照可供同一 seed 的后续候选继续判断。只有 `CanComplete == true` 才会
将整组 Ticket 与最终快照一起 commit。

实现见 [fact/provider.go](../internal/matchsystem/fact/provider.go)、
[fact/frame.go](../internal/matchsystem/fact/frame.go)、
[Match Fact 编排](../internal/matchsystem/logical_node_core.go)，完整顺序见
[runtime-flow](architecture/runtime-flow.md)。
