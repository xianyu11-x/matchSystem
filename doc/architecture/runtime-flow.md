# `ProduceMatch(ctx)` 运行时流程

`PhysicalNode` 是其所拥有节点的单一 owner，外层必须串行调用 Load、Ticket 操作、
轮次操作和 ProduceMatch。`BeginMatchRound(now)` 固定本轮时间和 seed 顺序；之后
新加入的 Ticket 等待下一轮。

## 固定顺序

一次 `LogicalNode.ProduceMatch(ctx)` 的核心路径严格如下：

```text
reserve seed
  -> Tick FactProvider(ctx, round.now)
  -> FactFrame clone 并拥有 Tick Fact
  -> seed ObjectFactProvider
  -> MatchFactProvider.Initialize
  -> clone 并拥有完整 Match Fact
  -> CanComplete(Tick Fact, Match Fact)
       ├─ true  -> commit seed + Match Fact -> done
       └─ false -> Prefilter.Candidates(seed, seed Fact, Tick Fact)
                    -> candidate ObjectFactProvider
                    -> CandidateScorer（建立 bounded Top-L）
                    -> 对排序后的 candidate 逐个：CanJoin
                         ├─ false -> 下一个 candidate
                          └─ true  -> MatchFactProvider.OnJoin
                                       -> clone 并拥有完整 Match Fact
                                      -> 原子接受 candidate + 新 Match Fact
                                      -> CanComplete(Tick Fact, 新 Match Fact)
                                           ├─ true  -> commit group + Match Fact
                                           └─ false -> 下一个 candidate
```

因此，`CanComplete` 的两次位置不是可配置的：第一次在 seed 初始化后，第二次在
每次成功的 `OnJoin` 原子接受后。没有成功加入时不会调用 `OnJoin`。

## 每个阶段的输入

- Tick `FactProvider` 每次 `ProduceMatch` 最多调用一次；它的结果建立本次尝试共享
  的 Tick Fact 层。`FactFrame` 只 clone 并拥有该层，不在生产路径重复做 Fact Contract
  校验。
- `ObjectFactProvider` 在一个 Frame 内按 Ticket ID 缓存，每个 Ticket 最多计算一次。
  Prefilter 和后续 Evaluation 使用同一份已拥有的 Object Fact。
- `Initialize` 收到 `Now`、seed Ticket、seed Object Fact 和 Tick Fact，返回完整
  Match Fact。没有 Match scope Fact 的 Contract 不调用 Provider。
- `CandidateScorer` 是绑定到该 LogicalNode 的一个 Go callback。它每次收到 seed、
  candidate、`Now`、Tick/seed/candidate Fact 的 clone；它不能读取已有 Match 成员或
  Match Fact。评分错误或非有限值会使本次匹配尝试 fail closed；Scorer panic 直接传播。
- `CanJoin` 可以读 seed 属性、seed Fact、Tick Fact、candidate 属性、candidate Fact
  和加入前的完整 Match Fact，但不能读 Match 内已有成员数据。
- `OnJoin` 可以读相同的 seed/Tick/candidate 输入和加入前 Match Fact 的 clone，返回
  加入 candidate 后的完整 Match Fact。
- `CanComplete` 只可以读 Tick Fact 和当前完整 Match Fact；它不能读 seed、candidate
  或 Match 成员。

表达式的 source 与谓词 profile 见 [Evaluation](../evaluation.md)，Fact 快照
规则见 [Match Fact Provider](../match-fact-provider.md)。

## Prefilter 与评分

Prefilter 只负责通过索引产生候选 DocSet，不决定 Match 是否成立，也不扫描 Ticket
作为兜底。LogicalNode 从 DocSet 去掉 seed 后，读取 candidate Object Fact 并执行
scorer，保留有限的 Top-L；只有这些排序后的候选才进入 `CanJoin`。因此 Prefilter
是必要的候选缩小步骤，Evaluation 是最终的加入/完成判定。

## 所有权与提交

LogicalNode 只在以下两个条件都满足后改变当前临时 group：

1. `OnJoin` 已返回；
2. 返回值已 clone 到 owner 内部。Provider 的完整 Match Fact Contract 由对应测试负责。

随后以同一次逻辑状态转换接受 candidate 和新快照。最终 commit 从池中移除 group
内所有 Ticket，并将 Match Fact 再复制到 `common.Match`；调用方可以安全持有返回值。
失败路径不会把旧快照替换成半成品，也不会通过重新遍历已有成员来补算表达式。

## 错误、取消和轮次预算

- context 在入口、阶段边界、候选迭代和 Provider 调用前后检查；取消或 deadline
  不会产生 Match。
- Provider 的 error 或取消会保留结构化错误并停止当前调用，不 patch、不重试旧快照；
  Provider 输出的 Fact Contract 正确性由测试保证，生产路径不重复校验；Provider panic
  直接传播。
- `CanJoin`、`CanComplete` 或候选数据错误不会静默放行；评分错误不会用未完成的
  排名继续匹配。
- seed 在开始尝试前从本轮 cursor 保留；Provider 或评估失败也不会在同一轮重新选择
  该 seed。每次调用和整轮分别受配置的尝试上限约束。

相关实现：[ProduceMatch](../../internal/matchsystem/logical_node.go)、[匹配评估](../../internal/matchsystem/seed_evaluator.go)、
[Ticket 生命周期](../../internal/matchsystem/ticket_store.go)、
[PhysicalNode](../../internal/matchsystem/physical_node.go)、[评分 callback](../../internal/matchsystem/evaluation_runtime.go)。
