# MatchSystem

MatchSystem 是一个进程内匹配核心：`PhysicalNode` 串行拥有多个
`LogicalNode`，每个逻辑节点维护自己的 Ticket、索引、表达式计划和轮次状态。
每个规则使用一份 `match-rule/v1` RuleJSON：它把完整 `RuleKey`、Contract、Prefilter、
Evaluation、内置评分、Seed 选择和运行参数绑定在一起。Fact Provider 仍由宿主进程动态
注入，不属于规则文件的可序列化配置。

## 先看这几份文档

- [文档入口](doc/README.md)
- [匹配模拟器快速开始](doc/simulator-quickstart.md)
- [匹配模拟器架构](doc/simulator-architecture.md)
- [已落地架构决策](doc/architecture/expression-engine-adr.md)
- [运行时流程](doc/architecture/runtime-flow.md)
- [共享 Contract](doc/logical-node-contract.md)
- [标量表达式](doc/expression-scalar.md)
- [表达式 JSON 使用文档](doc/expression-json-usage.md)
- [Prefilter](doc/prefilter.md)
- [Evaluation](doc/evaluation.md)
- [Match Fact Provider](doc/match-fact-provider.md)
- [发布与验证](doc/release-validation.md)

`doc/archive/` 只保存迁移前的历史材料，不是当前规范；实现和当前文档以源码为准。

## 运行时分工

```text
RuleJSON (match-rule/v1)
    ├─ ruleKey             -> RuleKey identity
    ├─ contract            -> shared schema for all sections
    ├─ prefilter           -> immutable Bitmap Plan -> IndexStore/TickSession
    ├─ evaluation          -> CanJoin/CanComplete Bool predicates
    ├─ scoring(type+params)-> built-in candidate scoring
    ├─ seedSelection       -> built-in seed ordering
    └─ runtime             -> candidate/group/attempt budgets

LogicalNode
    ├─ compiled RuleJSON   一份不可变规则配置
    ├─ Fact Providers      宿主动态依赖；Match Fact 的唯一写入者
    └─ ProduceMatch(ctx)   按固定顺序编排上面的计划和 Provider
```

`internal/matchsystem/expression` 只编译和求值四种标量结果：`bool`、`int64`、
`strings`、`uint64s`。它不拥有 Bitmap、索引、Match 成员或 Fact 写入。Prefilter
自己拥有私有 Bitmap expression 和 Roaring 执行；Evaluation 只拥有两个 Bool 谓词。

## 最小接入形态

`LogicalNodeSpec` 的生产输入包括 `LogicalNodeKey`、一份完整 RuleJSON 和可选的 Fact
Provider。下面的文件内容可以直接保存为 `rules/demo-1.json`：

```json
{
  "schemaVersion": "match-rule/v1",
  "ruleKey": {"namespace": "demo", "ruleId": 1},
  "contract": {
    "schemaVersion": "logical-node-contract/v3",
    "attributes": [],
    "facts": [],
    "indexes": []
  },
  "prefilter": {
    "schemaVersion": "prefilter/v3",
    "bitmap": {"resultType": "bitmap", "expr": {"op": "none"}}
  },
  "evaluation": {
    "schemaVersion": "evaluation/v3",
    "canJoin": {
      "schemaVersion": "expression-scalar/v3",
      "resultType": "bool",
      "expr": {"op": "bool_literal", "value": true}
    },
    "canComplete": {
      "schemaVersion": "expression-scalar/v3",
      "resultType": "bool",
      "expr": {"op": "bool_literal", "value": true}
    }
  },
  "scoring": {"type": "created_at", "params": {"direction": "descending"}},
  "seedSelection": {"type": "arrival", "params": {}},
  "runtime": {
    "candidateLimitPerSeed": 128,
    "maxPlayers": 8,
    "attemptLimitPerProduceMatch": 500,
    "attemptLimitPerMatchRound": 500
  }
}
```

RuleJSON 中的 `ruleKey` 必须与 `LogicalNodeKey.Rule` 完全一致；`PlacementID` 属于部署
拓扑，不写入规则语义。宿主读取文件后只需把同一份字节交给 `LogicalNodeSpec.RuleJSON`，
再按 Contract 中声明的 Match Fact 配置 `MatchFactProvider`。

`contract` 必须声明 `attributes`、`facts`、`indexes`；`prefilter` 必须是 `prefilter/v3`
Bitmap envelope；`evaluation` 必须包含两个 `expression-scalar/v3` Bool root。`scoring`
支持 `constant`、`created_at`、`int64_field`；`seedSelection` 支持 `arrival`、`oldest`、
`int64_priority`、`random`。每个类型只接受 schema 定义的 `params`，运行时四个预算
字段均为正整数。

```json
{
  "schemaVersion": "expression-scalar/v3",
  "resultType": "bool",
  "expr": {"op": "bool_literal", "value": true}
}
```

完整字段、数据源、索引限制和错误边界分别见对应文档。评分和 Seed 选择均从 RuleJSON
编译为 LogicalNode 私有的不可变运行对象；Fact 只能由宿主 Provider 返回完整快照，不能
由 Evaluation 或 LogicalNode 直接 patch。

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
