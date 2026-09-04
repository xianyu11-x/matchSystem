# Evaluation 评估层

`internal/matchsystem/evaluation` 负责一个 LogicalNode 的候选评分、加入 Match 的
判断、Match Fact 更新和 Match 是否成立的判断。表达式的节点集合、五种结果类型、JSON
解码、类型检查和编译全部由 `internal/matchsystem/expression` 统一提供；Evaluation
只声明 phase 能力、领域叶子和运行时编排。

## 1. 三块评估能力

每个 LogicalNode 选择一个命名的 `CandidateScorer`。它是普通 Go callback，不是表达式，
只返回一个有限 `float64` 分数和错误。候选集合由 Prefilter 产生后，LogicalNode 用该
函数做 bounded Top-L；不同 LogicalNode 可以注册并选择不同 scorer。

另外两块是共享表达式的 Bool 根：

| 能力 | 配置入口 | 根结果 | 作用 |
| --- | --- | --- | --- |
| 加入 Match | `Config.Join` | `ResultBool` | 判断当前候选能否加入当前 Match |
| Match 成立 | `Config.Complete` | `ResultBool` | 判断当前 Match 是否已经满足规则 |

Match Fact 的 `Initialize` 和 `OnJoin` 是固定流程中使用的 value roots：它们的根结果
必须分别匹配 Contract 中对应 match-scope Fact 的 `Strings`、`Int64` 或 `Uint64s` 类型。
它们不是第三个评估判断，也不能改变流程或调用 Provider。所有 Join/Complete 判断最终
都必须由 Bool root 返回成立/不成立。

## 2. Typed 配置与编译

`LogicalNodeSpec.Contract` 是唯一 Contract 入口。Prefilter 和 Evaluation 都接收这一份
已验证的 `contract.Contract`；`evaluation.CompileOptions` 不再携带第二个 Contract。

```go
arena := expression.NewArena()
count := arena.Int64Lookup(expression.SourceMatchFacts, "count")
limit := arena.Int64Literal(4)

config := evaluation.Config{
    Arena:           arena,
    CandidateScorer: "createdAt",
    MatchFacts: evaluation.MatchFactsConfig{
        Initialize: map[string]expression.Root{
            "count": arena.Root(arena.Int64Literal(1), expression.ResultInt64),
        },
        OnJoin: map[string]expression.Root{
            "count": arena.Root(arena.Int64Add(count, arena.Int64Literal(1)), expression.ResultInt64),
        },
    },
    Join:     arena.Root(arena.LessInt64(count, limit), expression.ResultBool),
    Complete: arena.Root(arena.GreaterOrEqualInt64(count, limit), expression.ResultBool),
}

options := evaluation.CompileOptions{
    Scorers: evaluation.ScorerRegistry{
        "createdAt": func(ctx evaluation.CandidateScoreContext) (float64, error) {
            return -float64(ctx.Candidate.CreatedAt), nil
        },
    },
}
plan, err := evaluation.Compile(config, schema, options)
```

`Config.Arena` 必须承载 Join、Complete、Initialize 和 OnJoin 的所有 roots。每个 root 都
使用 `expression.Root{Node, Result}` 明确结果类型；缺少或不匹配的结果类型会在编译期
拒绝。`ResultInt64`、`ResultStrings` 和 `ResultUint64s` 是 Match Fact 更新的内部值表达式，
而不是 Evaluation 的最终判断结果。

## 3. 各阶段可读取的数据

Expression 的叶子是 typed lookup/domain leaf；它们读取值，Bool 谓词将值组合为最终的
`true/false`。可读 source 由 phase profile 在编译期封闭：

| 阶段 | 可读内容 | 结果 |
| --- | --- | --- |
| Initialize | Seed 属性、Seed Object Fact、当前 Tick Fact | Match Fact 的 value root |
| Join | Seed 属性/Fact、当前 Tick Fact、当前 Candidate 属性/Fact、当前 Match Fact | Bool |
| OnJoin | 与 Join 相同的当前快照 | Match Fact 的 value root |
| Complete | Match Fact、当前 Tick Fact | Bool |

Candidate scorer 的 `CandidateScoreContext` 包含 Seed/Candidate Ticket 属性、Seed Facts、
Candidate Facts 和 Tick Facts；它不读取 Match Fact，也不通过表达式参与控制流。

“当前 Candidate”是唯一暴露的候选成员视图。新候选通过 Join 后，OnJoin 可以基于加入前
的 Match Fact 快照计算更新；LogicalNode 只在全部更新成功后原子发布新的 Match Fact。后续
候选不会获得已有 Match 成员的 Ticket、属性或 Object Fact，也没有遍历 Match 成员的 API，
因此表达式不会为了读取历史成员而反复扫描 Match。

## 4. 固定运行时序

```text
选定 Seed
  -> 生成 Tick/Object Fact 快照
  -> Initialize：生成全部 match-scope Match Fact
  -> Complete：用 Match Fact + Tick Fact 判断 Seed-only Match
  -> Prefilter 产生候选安全超集
  -> CandidateScorer 对候选排序（每个 LogicalNode 一个命名函数）
  -> 对每个候选：Join Bool
       false -> 跳过候选
       true  -> OnJoin 计算 Match Fact update，并原子合并
             -> Complete Bool
                  true  -> 提交 Match 并移除成员
                  false -> 继续下一个候选
```

一次 OnJoin update 中所有表达式读取同一份旧 Match Fact snapshot；任何表达式错误、类型
错误或 MaxValues 超限都会丢弃整批 update，旧 snapshot 保持不变。成功加入后，下一次
Join/OnJoin 只看到聚合后的 Match Fact、当前候选和固定的 Seed/Tick 输入。

Complete 只能使用 Match Fact 与 Tick Fact，不能使用 Match 内成员数据。Complete 返回
`false` 时不会执行完成阶段的后续提交动作；表达式错误也不会触发成员删除或其他副作用。

## 5. Compile profile 与 DomainLeaf

Evaluation 通过 `CompileOptions.Domains` 提供 `DomainRegistry`。每个
`DomainLeafRegistration` 包含共享 `expression.DomainDescriptor`、按 ResultType 区分的
typed compiler/evaluator，以及显式的 `AllowedPhases`；这是当前唯一的领域注册入口。

四个阶段的根约束如下：

| Phase | 根结果 | 说明 |
| --- | --- | --- |
| `PhaseInitialize` | Strings/Int64/Uint64s | 必须覆盖全部 match-scope Facts |
| `PhaseJoin` | Bool | 当前候选加入判断 |
| `PhaseUpdate` | Strings/Int64/Uint64s | OnJoin 的部分更新 |
| `PhaseComplete` | Bool | Match 成立判断 |

领域叶子的 descriptor、字段类型和 phase allow-list 由共享 Compiler 检查；未知 kind、
错误 result、非法 source、跨 scope Fact、缺失 compiler/evaluator 都 fail closed。动态
operand 会被 expression Compiler 编译为所属 Program 中的 typed Instruction handle；
Evaluation 领域包只提供 descriptor、typed leaf compiler/evaluator 和绑定表，不创建子
Program，也不遍历通用 Arena/AST。

领域叶子的运行时 `Lookup` 同样携带当前 phase capability；即使 Context 意外包含数据，叶子
也不能读取该 phase 未授权的 source，违规访问统一返回 `SOURCE_NOT_ALLOWED`。

## 6. JSON 入口

Evaluation envelope 当前唯一版本为 `evaluation/v2`。它与唯一业务契约
`logical-node-contract/v2` 不是同一版本概念；前者只描述 Evaluation 配置的 wire shape，
后者声明 Attributes、Indexes 和三类 Fact。

每个表达式都必须是共享的显式 root envelope：

```json
{
  "schemaVersion": "evaluation/v2",
  "candidateScorer": "createdAt",
  "matchFacts": {
    "initialize": {
      "count": {"resultType":"int64","expr":{"op":"int64_literal","value":1}}
    },
    "onJoin": {
      "count": {"resultType":"int64","expr":{"op":"int64_add",
        "left":{"op":"int64_ref","source":"match_facts","name":"count"},
        "right":{"op":"int64_literal","value":1}}}
    }
  },
  "join": {"resultType":"bool","expr":{"op":"int64_lt",
    "left":{"op":"int64_ref","source":"match_facts","name":"count"},
    "right":{"op":"int64_literal","value":4}}},
  "complete": {"resultType":"bool","expr":{"op":"int64_gte",
    "left":{"op":"int64_ref","source":"match_facts","name":"count"},
    "right":{"op":"int64_literal","value":4}}}
}
```

解析和语义编译分层，但编译器只有一份：

```go
options := matchsystem.EvaluationCompileOptions{Scorers: scorers}
config, err := matchsystem.ParseEvaluationJSONWithDefaults(raw, options)
if err != nil { return err }
plan, err := matchsystem.CompileEvaluation(config, schema, options)

// 直接入口：plan, err := matchsystem.CompileEvaluationJSON(raw, schema, options)
```

`ParseEvaluationJSONWithDefaults` 只调用共享 `expression.DecodeRootInto` 完成严格 JSON、
root shape、ResultType、child shape 和 Domain descriptor 解码；真实 Contract、phase source
和 Fact scope 在 `Compile` 中检查一次。JSON 限制通过 `CompileOptions.JSONLimits` 传入，
`DecodeOptions` 不携带另一个 limits 字段。

## 7. API 与安全边界

公开 Plan 方法包括 `Score`、`Join`、`Complete`、`Initialize`、`OnJoin` 和 `Update`，并
提供先验证后执行的 `ValidatedContext` 热路径。Context 中的 Ticket、Fact map 和 slice
在 callback/表达式运行期间按借用规则使用；scorer 的输入则由 Plan 隔离为独立快照。

Evaluation 不暴露通用 Program map、任意 phase/workflow、表达式 scorer、Provider 调用或
表达式写事务。它只把 OnJoin 计算出的 typed values 交给 LogicalNode owner，由 owner 做
Contract 校验和原子 Match Fact 合并。Prefilter 的 Bitmap、Roaring、index sidecar 和
TicketStore 也不进入 Evaluation。

缺失值、Fact scope/type、Domain leaf、limits 和表达式运行错误都保留 `Phase/Path/Code`；
错误不会被解释为空 Match、跳过 Join 或触发后续副作用。

旧的 `evaluation/v1` envelope 不属于当前输入，加载时返回 `UNKNOWN_SCHEMA_VERSION`。
