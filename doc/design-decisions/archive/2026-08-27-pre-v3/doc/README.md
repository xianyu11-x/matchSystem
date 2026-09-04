# MatchSystem 文档入口

文档以当前源码为准，重点说明统一的 Expression/Contract、Prefilter 和 Evaluation
边界。所有 Go 示例都应以 `internal/matchsystem` 下的当前导出 API 为准；JSON 示例中的
表达式必须带显式 `resultType`。

## 核心模型

- [Expression Core](expression-core.md)：`ResultType`、`Kind`、`Arena`/`NodeRef`/`Root`、
  `StrictProfile`/`BuiltinKinds`、统一 `Compiler`/`Program`、Domain leaf descriptor、
  `DecodeRootInto`/`ParseRoot` 和 runtime 边界。
- [Expression/Prefilter/Evaluation 合并实施记录](expression-prefilter-evaluation-split-plan.md)：
  目标分层、不可合并的 runtime 和迁移验收项。
- [表达式统一改造完成报告](expression-unification-completion.md)：当前代码契约、领域边界
  和本次文档审计结果。
- [logical-node-contract/v2](logical-node-contract-v2.md)：属性、索引、tick/object/match
  Fact 及严格 JSON；这是 Prefilter 与 Evaluation 的唯一 Contract。
- [Evaluation 层](evaluation-layer.md)：共享 Arena Root、phase profile、真实 Contract 单次
  编译、scorer、Join、Match Fact update、Complete。
- [Fact 生命周期](fact-lifecycle.md)：FactFrame、三种 scope、Provider 和缓存所有权。

## Prefilter

- [Prefilter 使用指南](prefilter/usage-guide.md)：typed `expression.Arena` +
  `prefilter.Builder`、显式 Bitmap Root、JSON 和 IndexStore 生命周期。
- [Prefilter 架构](prefilter/architecture.md)：expression 编译、leaf sidecar、anchor、
  Roaring executor 与单 owner goroutine。
- [Prefilter 代码索引](prefilter/code-reference.md)：当前生产文件和公共入口对应关系。
- [索引初筛设计](index-prefiltering.md)：候选域、Query、Bitmap 算法和安全边界。
- [Prefilter JSON 与热更新边界](json-prefilter-hot-reload.md)：当前 JSON 编译接口及未来
  generation 发布边界；不把未实现的 manager 当作现有 API。

## 匹配运行时

- [匹配系统框架](match-system-framework.md)：PhysicalNode、LogicalNode、Tick 和产出流程。
- [Router/PhysicalNode/LogicalNode](router-physical-logical-node.md)
- [LogicalNode 选择策略](logical-node-selector.md)
- [Seed 顺序策略](seed-order-policy.md)
- [Ticket 生命周期与 DocID](ticket-lifecycle.md)

## 版本约定

`logical-node-contract/v2` 是唯一契约；`prefilter/v2` 与 `evaluation/v2` 仅表示两个 JSON
envelope，和 Contract 版本不是同一概念。表达式、Prefilter、Evaluation 的入口分别由当前
shared Core、Builder 和 phase Compiler 提供。`prefilter/v1` 与 `evaluation/v1` 会被明确拒绝，
不会作为兼容输入。Prefilter fingerprint 为 `prefilter-fingerprint/v4`；本次 breaking change 使所有旧 fingerprint 失效，升级时清理旧缓存
并重新编译。

## 依赖方向

```text
fact ----------------------┐
expression ----------------┼─> prefilter ──┐
contract ------------------┘              ├─> matchsystem/LogicalNode
expression + contract + fact ─> evaluation ┘
```

Expression 不依赖 Roaring、索引或 Match 生命周期；Contract 不依赖 Prefilter/Evaluation。
Prefilter/Evaluation 只负责自己的叶子、phase profile 和 runtime 编排。

## 验证

```bash
go test ./...
go vet ./...
```

架构和运行时文档里的“设计边界”不等于仓库已经提供的外部网络、配置发布或热更新服务。
