# 匹配系统文档

本分类描述 `internal/matchsystem` 的当前架构、接入方式和版本化配置契约。若文档与实现
不一致，以当前源码、测试和生产编译器的 fail-closed 结果为准。

## 核心文档

- [架构](architecture.md)：分层、依赖、生命周期、PhysicalNode/LogicalNode 与失败语义。
- [运行时流程](runtime-flow.md)：一次 `ProduceMatch(ctx)` 从 Seed 到原子提交的固定顺序。
- [参数明细](parameters.md)：`match-rule/v1` 顶层字段、算法参数、运行预算和限制。
- [使用指南](usage-guide.md)：创建节点、投递 Ticket、执行轮次和接入 Fact Provider。
- [根包代码索引](code-reference.md)：公共类型、入口和私有协作组件。
- [包级文档矩阵](packages.md)：根包及六个子包的架构、代码索引和用户指南。

## 配置参考

- [LogicalNode Contract](reference/logical-node-contract.md)
- [标量表达式](reference/expression-scalar.md)
- [表达式 JSON 完整指南](reference/expression-json-usage.md)
- [Prefilter](reference/prefilter.md)
- [Evaluation](reference/evaluation.md)
- [Match Fact Provider](reference/match-fact-provider.md)
- [Provider Descriptor](reference/provider-descriptor.md)
- [LogicalNode Fact 元数据](reference/logical-node-fact-metadata.md)

历史格式和迁移前 API 仅保存在
[设计决策 / 历史归档](../design-decisions/archive/2026-08-27-pre-v3/README.md)，不得作为
当前接入依据。
