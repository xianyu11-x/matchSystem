# MatchSystem 文档入口

本目录描述当前源码已经实现的配置契约和运行边界。`doc/archive/` 是迁移前的
历史归档，不是规范来源；当文档与源码冲突时，以当前源码和测试中暴露的 API 为准。

## 架构与流程

- [已落地架构决策](architecture/expression-engine-adr.md)：契约、所有权和简化边界。
- [运行时流程](architecture/runtime-flow.md)：从 `ProduceMatch(ctx)` 到提交 Match 的固定顺序。
- [生产架构冗余最终评估](architecture/final-production-redundancy-assessment.md)：基于删除测试后的 HEAD `75ca1a2` 的边界、规模与清理建议。

## 配置与表达式

- [logical-node-contract/v3](logical-node-contract.md)：Attributes、Facts、索引和限制。
- [expression-scalar/v3](expression-scalar.md)：共享标量表达式的概览。
- [expression JSON 使用文档](expression-json-usage.md)：完整的 JSON 编写规则、字段、节点、类型约束、限制和错误。
- [Prefilter](prefilter.md)：私有 Bitmap expression、索引查询和 TickSession。
- [Evaluation](evaluation.md)：`canJoin`、`canComplete` 两个 Bool 谓词。

## Fact 与发布

- [Match Fact Provider](match-fact-provider.md)：完整快照、校验、clone 和原子提交。
- [发布与验证](release-validation.md)：编译计划身份、上层发布/回滚和本轮验证记录。

## 包级说明与使用指南

- [internal/matchsystem 包级文档](matchsystem/README.md)：根包及六个子包各自的架构说明、代码索引和使用指南。
- [根包：架构](matchsystem/architecture.md) · [代码索引](matchsystem/code-reference.md) · [使用指南](matchsystem/usage-guide.md)
- [contract](matchsystem/contract/architecture.md) · [expression](matchsystem/expression/architecture.md) · [fact](matchsystem/fact/architecture.md)
- [jsonstrict](matchsystem/jsonstrict/architecture.md) · [prefilter](matchsystem/prefilter/architecture.md) · [evaluation](matchsystem/evaluation/architecture.md)

## 依赖方向

```text
fact ───────────────┐
contract ────────────┼─> expression ──> prefilter
                     └───────────────> evaluation
contract + prefilter + evaluation + fact ──> matchsystem
```

表达式包只处理标量 JSON 和 typed lookup；Prefilter 负责 Bitmap/索引执行，Evaluation
负责两个谓词。`LogicalNode` 只协调状态、轮次和提交：`seedEvaluator` 封装 Tick/Object
Fact、Prefilter、Top-L、Scorer、CanJoin/CanComplete 及 Match Fact 流程，`ticketStore`
封装 Ticket/DocID、Prefilter membership 和原子 Commit。没有跨领域的第二套配置模型或
运行时注册表。

## 最小验证命令

```text
go test ./...
go vet ./...
go build ./...
go mod verify
go run ./cmd/app
```

依赖边界检查见 [scripts/check-expression-deps.ps1](../scripts/check-expression-deps.ps1)。
