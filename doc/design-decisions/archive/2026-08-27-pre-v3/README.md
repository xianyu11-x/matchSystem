# MatchSystem 通用匹配模板

> 历史快照：本目录冻结于 2026-08-27 之前的设计语境，不是当前 API 或配置规范。
> 当前入口见[匹配系统文档](../../../match-system/README.md)。仓库不提供旧格式的运行时
> 双读、fallback 或自动迁移。

迁移时需要离线重建完整 `match-rule/v1`：旧 Contract 和表达式升级到当前 v3 section，
Prefilter/Evaluation 重新编译，Provider 配置移到宿主/Scenario 的 Descriptor，最后记录新
RuleJSON fingerprint。不得只替换某个局部 section 或沿用旧 Program/Plan。

MatchSystem 是进程内匹配核心。它把“表达式的合法性和编译”集中在
`internal/matchsystem/expression`，再由 Prefilter 和 Evaluation 各自提供领域叶子、
阶段权限以及运行时编排。两者使用同一份 `logical-node-contract/v2`，不保留另一套
Prefilter Contract。

## 文档导航

- [文档总入口](doc/README.md)
- [Expression Core](doc/expression-core.md)：五种结果类型、封闭节点集合、Arena、JSON、Compiler 和 Program。
- [Expression/Prefilter/Evaluation 合并实施记录](doc/expression-prefilter-evaluation-split-plan.md)
- [表达式统一改造完成报告](doc/expression-unification-completion.md)
- [共享 Contract](doc/logical-node-contract-v2.md)：属性、索引和三类 Fact 的唯一声明。
- [Evaluation 层](doc/evaluation-layer.md)：phase profile、评分、Join、Match Fact 更新和 Complete。
- [Prefilter 使用指南](doc/prefilter/usage-guide.md)
- [Prefilter 架构](doc/prefilter/architecture.md)与[代码索引](doc/prefilter/code-reference.md)
- [索引初筛设计](doc/index-prefiltering.md)
- [Prefilter JSON 与热更新边界](doc/json-prefilter-hot-reload.md)

## 统一编译模型

```text
expression.Arena
  ├─ value/bool nodes
  ├─ bitmap structure nodes
  └─ domain leaves supplied by a phase
          │
          ▼ explicit expression.Root{Node, Result}
expression.Compiler(CompileProfile)
          │
          ▼ immutable expression.Program
  ├─ Evaluation: primitive Lookup + typed leaf evaluators
  └─ Prefilter: Bitmap instructions + index-query sidecars + Roaring executor
```

`ResultType` 必须显式为 `bitmap`、`bool`、`int64`、`strings` 或 `uint64s`。Compiler
只接受 `BuiltinKinds(result)` 允许的闭合集合，并检查每个节点的输入/输出类型、source
capability、Contract 名称、深度、节点数和循环。Prefilter/Evaluation 不再各自维护一套
AST 或通用编译器；它们只注册自己的 `DomainLeaf` schema/compiler 并编排执行。

## Typed Prefilter 最小用法

下面的示例使用一座共享 Arena。`Builder` 只创建 Prefilter 的索引叶子，布尔/Bitmap
结构和动态值仍由 `expression.Arena` 创建：

```go
schema := contract.Contract{
    Attributes: []contract.AttributeSpec{
        {Name: "mode", Type: fact.TypeStrings, MaxValues: 8},
    },
    Facts: []contract.FactSpec{
        {Name: "capacity", Type: fact.TypeInt64, Scope: fact.ScopeTick},
        {Name: "count", Type: fact.TypeInt64, Scope: fact.ScopeMatch},
    },
    Indexes: []contract.IndexSpec{
        {
            Type: contract.IndexTypeMultiValue, Name: "mode_index", Field: "mode",
            KeyType: contract.KeyTypeString, MaxDocumentValues: 8, MaxQueryValues: 8,
        },
    },
}

arena := expression.NewArena()
builder := prefilter.NewBuilder(arena)
seedModes := arena.StringsLookup(expression.SourceSeedAttributes, "mode")
leaf := builder.LookupString("mode_index", seedModes)
root := builder.Root(leaf) // Root.Result == expression.ResultBitmap

plan, err := prefilter.Compile(prefilter.Config{
    Arena: arena,
    Root:  root,
}, schema)
if err != nil {
    return err
}

store, err := prefilter.New(plan)
if err != nil {
    return err
}
_ = store // Add/Remove/BeginTick/Candidates 由 LogicalNode 的 owner 串行调用
```

组合节点仍是 shared Arena 的方法，例如 `arena.BitmapAnd(leafA, leafB)`、
`arena.BitmapOr(...)`、`arena.BitmapExclude(...)`、`arena.BitmapIf(condition, then, else)`
和 `arena.BitmapNone()`。`Exclude` 只有在已有正向 Bitmap scope 时才合法；这属于
Prefilter 编译/执行器的 anchor 规则，不是 expression 的 Roaring 实现。

## Typed Evaluation 最小用法

Evaluation 与 Prefilter 可以分别拥有 Arena；一套 Evaluation phase 内的 Join、Complete、
Initialize、OnJoin 必须共享同一座 Arena。下面只使用当前 API：

```go
arena := expression.NewArena()
count := arena.Int64Lookup(expression.SourceMatchFacts, "count")
four := arena.Int64Literal(4)

evaluationConfig := evaluation.Config{
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
    Join:     arena.Root(arena.LessInt64(count, four), expression.ResultBool),
    Complete: arena.Root(arena.GreaterOrEqualInt64(count, four), expression.ResultBool),
}

registry := evaluation.ScorerRegistry{
    "createdAt": func(ctx evaluation.CandidateScoreContext) (float64, error) {
        return -float64(ctx.Candidate.CreatedAt), nil
    },
}
evaluationPlan, err := evaluation.Compile(evaluationConfig, schema, evaluation.CompileOptions{
    Scorers: registry,
})
if err != nil {
    return err
}
```

`Compile` 会把真实 Contract 转为每个 phase 的 `CompileProfile`，再调用同一个
`expression.Compiler`。运行时 `Join`/`Complete` 只接收 primitive `expression.Lookup`；
评分 callback、Match Fact 原子更新、Roaring Bitmap、索引存储和事务边界不会下沉到
expression。

## JSON 入口

Contract 先通过唯一入口解析：

```go
schema, err := matchsystem.ParseLogicalNodeContract(contractJSON)
if err != nil {
    return err
}
```

Prefilter 的 envelope 是 `prefilter/v2`，但 `plan` 必须是 shared expression 的显式
Root：

```json
{
  "schemaVersion": "prefilter/v2",
  "plan": {
    "resultType": "bitmap",
    "expr": {
      "op": "domain_call",
      "tag": "prefilter",
      "kind": "prefilter.lookup.string",
      "resultType": "bitmap",
      "fields": {"index": "mode_index", "values": ["ranked"]}
    }
  },
  "runtime": {"containsProbeThreshold": 4096}
}
```

```go
jsonCompiler, err := prefilter.NewJSONCompiler(schema)
if err != nil {
    return err
}
plan, err := jsonCompiler.Compile(planJSON)
```

Evaluation 的 `evaluation/v2` envelope 中，`join`、`complete` 和每个 Match Fact value
也使用同样的 `{"resultType": ..., "expr": ...}`。`ParseEvaluationJSONWithDefaults`
只解析严格 JSON/表达式形状；必须随后以同一份真实 Contract 调用
`matchsystem.CompileEvaluation`，或者直接调用 `CompileEvaluationJSON` 完成单次语义编译。

## 运行时边界

- expression：闭合节点、类型检查、source/capability 检查、canonical、依赖、Program
  和 primitive typed evaluation。
- Prefilter：`prefilter.Builder` 的索引叶子、Contract 到索引 slot 的绑定、query
  sidecar、anchor/estimate、Roaring Bitmap 与 `IndexStore`/`TickSession`。
- Evaluation：phase profile、scorer registry、Fact scope 到 source 的映射、Match Fact
  原子更新以及 Join/Complete 调度。
- LogicalNode：owner goroutine、Ticket/DocID 生命周期、FactFrame、Top-L、建组和产出。

Roaring Bitmap、物理 scorer、TicketStore 和事务/提交语义不能统一进 expression runtime。
统一的是“节点是否合法、如何编译成 Program、如何通过 typed Lookup 读取值”；领域执行
资源仍由各自的包拥有。

## 版本与验证

`logical-node-contract/v2` 是唯一 Contract；`prefilter/v2` 和 `evaluation/v2` 只是各自
JSON envelope，不是两套契约，且与 Contract 版本不是同一概念。旧的 `prefilter/v1` 和
`evaluation/v1` 只会得到 `UNKNOWN_SCHEMA_VERSION`，不会进入解析或编译。目标 Prefilter fingerprint 为
`prefilter-fingerprint/v4`；本次 breaking change 使所有旧 fingerprint 全部失效，升级时必须丢弃旧缓存并从 Contract、Program、sidecar 和
运行参数重新编译。文档中的 fingerprint、canonical 和 wire 版本不承诺旧代兼容。

核心验证命令：

```bash
go test ./...
go vet ./...
```

匹配轮次、Fact/Ticket 所有权、Router 和 LogicalNode 的 owner 约束见 [文档总入口](doc/README.md)。
