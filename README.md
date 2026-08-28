# MatchSystem

MatchSystem 是一个进程内匹配核心：`PhysicalNode` 串行拥有多个
`LogicalNode`，每个逻辑节点维护自己的 Ticket、索引、表达式计划和轮次状态。
当前 Contract、Prefilter 和 Evaluation 配置是 JSON-only，并使用同一个已校验的
`logical-node-contract/v3`；轮次、分组等运行参数仍由 `LogicalNodeConfig` 控制。

## 先看这几份文档

- [文档入口](doc/README.md)
- [已落地架构决策](doc/architecture/expression-engine-adr.md)
- [运行时流程](doc/architecture/runtime-flow.md)
- [共享 Contract](doc/logical-node-contract.md)
- [标量表达式](doc/expression-scalar.md)
- [Prefilter](doc/prefilter.md)
- [Evaluation](doc/evaluation.md)
- [Match Fact Provider](doc/match-fact-provider.md)
- [发布与验证](doc/release-validation.md)

`doc/archive/` 只保存迁移前的历史材料，不是当前规范；实现和当前文档以源码为准。

## 运行时分工

```text
Contract JSON
    ├─ Prefilter JSON   -> immutable Bitmap Plan -> IndexStore/TickSession
    └─ Evaluation JSON  -> CanJoin/CanComplete Bool predicates

LogicalNode
    ├─ CandidateScorer     一个直接绑定的 Go callback
    ├─ MatchFactProvider   Match Fact 的唯一写入者
    └─ ProduceMatch(ctx)   按固定顺序编排上面的计划和 callback
```

`internal/matchsystem/expression` 只编译和求值四种标量结果：`bool`、`int64`、
`strings`、`uint64s`。它不拥有 Bitmap、索引、Match 成员或 Fact 写入。Prefilter
自己拥有私有 Bitmap expression 和 Roaring 执行；Evaluation 只拥有两个 Bool 谓词。

## 最小接入形态

`LogicalNodeSpec` 的生产输入包括：

下面是一个可直接放入同一 Go 文件的最小完整示例：

```go
import (
    "context"

    "matchSystem/internal/common"
    "matchSystem/internal/identity"
    "matchSystem/internal/matchsystem"
)

func configureAndRun() error {
ctx := context.Background()
now := int64(100)
spec := matchsystem.LogicalNodeSpec{
    Key: identity.LogicalNodeKey{
        Rule: identity.RuleKey{Namespace: "demo", RuleID: 1},
        PlacementID: "default",
    },
    ContractJSON: []byte(`{"schemaVersion":"logical-node-contract/v3","attributes":[],"facts":[],"indexes":[]}`),
    PrefilterJSON: []byte(`{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"none"}}}`),
    EvaluationJSON: []byte(`{
        "schemaVersion":"evaluation/v3",
        "canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
        "canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}
    }`),
    // NewLogicalNode 始终要求每个节点绑定一个非 nil scorer；本例没有 candidate，
    // 所以 scorer 不会被调用。
    CandidateScorer: func(matchsystem.CandidateScoreContext) (float64, error) {
        return 0, nil
    },
}

physical, err := matchsystem.NewPhysicalNode("physical")
if err != nil { return err }
if err := physical.Load(ctx, spec); err != nil { return err }
owner := identity.OwnerRef{LogicalNode: spec.Key, PhysicalNodeID: physical.ID()}
if _, err := physical.Add(ctx, owner, &common.Ticket{TicketID: 1}); err != nil { return err }
if err := physical.BeginMatchRound(ctx, now); err != nil { return err }
result, err := physical.ProduceMatch(ctx)
if err != nil { return err }
_ = result
return nil
}
```

上例中的 `Key`、三个 JSON 字段和非 nil `CandidateScorer` 都是创建节点的必填条件，
因此可以直接通过 `NewLogicalNode` 和 `PhysicalNode.Load`。如果 Contract 声明了
`scope: "match"` Fact，还必须给 `MatchFactProvider` 字段赋一个实现；本例没有
Match Fact，故不需要 Provider。

`ContractJSON` 必须声明 `attributes`、`facts`、`indexes`；`PrefilterJSON` 必须是
`prefilter/v3` Bitmap envelope；`EvaluationJSON` 必须是 `evaluation/v3`，包含
`canJoin` 和 `canComplete` 两个 `expression-scalar/v3` Bool root。每个标量 root
都显式声明 `resultType` 和 `expr`，例如：

```json
{
  "schemaVersion": "expression-scalar/v3",
  "resultType": "bool",
  "expr": {"op": "bool_literal", "value": true}
}
```

完整字段、数据源、索引限制和错误边界分别见对应文档。`CandidateScorer` 是每个
逻辑节点唯一的评分函数，不通过 JSON 名称解析；Match Fact 只能由 Provider 返回
完整快照，不能由 Evaluation 或 LogicalNode 直接 patch。

## 代码入口

- [LogicalNodeSpec 与 ProduceMatch](internal/matchsystem/logical_node.go)
- [匹配评估](internal/matchsystem/seed_evaluator.go)、[候选评分](internal/matchsystem/candidate_ranking.go)、
  [Ticket 生命周期与原子提交](internal/matchsystem/ticket_store.go)
- [表达式公共契约](internal/matchsystem/expression/schema.go)
- [表达式 JSON 编译](internal/matchsystem/expression/json.go)
- [Prefilter 计划与身份](internal/matchsystem/prefilter/plan.go)
- [Prefilter 编译](internal/matchsystem/prefilter/compiler.go)
- [Evaluation](internal/matchsystem/evaluation/predicates.go)
- [Fact Provider 与校验](internal/matchsystem/fact/provider.go)
- [Fact Frame](internal/matchsystem/fact/frame.go)

## 本地验证

```text
go test ./...
go vet ./...
go build ./...
go mod verify
go run ./cmd/app
```

依赖边界检查脚本是 [check-expression-deps.ps1](scripts/check-expression-deps.ps1)。
