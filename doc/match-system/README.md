# 匹配系统文档

本分类描述公开 Go 包 `pkg/matchsystem` 及其协作包的当前架构、接入方式和版本化配置契约。若文档与实现
不一致，以当前源码、测试和生产编译器的 fail-closed 结果为准。

## 核心文档

- [架构](architecture.md)：分层、依赖、生命周期、PhysicalNode/LogicalNode 与失败语义。
- [运行时流程](runtime-flow.md)包含从 Client 选路、Ticket 入池到 Match 产出的 Mermaid 流程图，以及 `ProduceMatch(ctx)` 的固定顺序。
- [参数明细](parameters.md)：`match-rule/v1` 顶层字段、算法参数、运行预算和限制。
- [使用指南](usage-guide.md)：创建节点、投递 Ticket、执行轮次和接入 Fact Provider。
- [Go 接入指南](go-consumer.md)：外部模块依赖、`pkg/` 导入边界和本地开发替换。
- [Client Router 路由与选择器](client-router.md)：规则身份、物理节点路由快照和队列选路。
- [根包代码索引](code-reference.md)：公共类型、入口和私有协作组件。
- [包级文档矩阵](packages.md)：根包及六个子包的架构、代码索引和用户指南。

## 配置参考

- [LogicalNode Contract](reference/logical-node-contract.md)
  · [内建属性](reference/logical-node-contract.md#内建属性)
- [标量表达式](reference/expression-scalar.md)
- [表达式 JSON 完整指南](reference/expression-json-usage.md)
- [Prefilter](reference/prefilter.md)
  · [最近值查询](reference/prefilter.md#查找最近的-int64-值)
- [Evaluation](reference/evaluation.md)
- [Match Fact Provider](reference/match-fact-provider.md)
- [Provider Descriptor](reference/provider-descriptor.md)
- [LogicalNode Fact 元数据](reference/logical-node-fact-metadata.md)

历史格式和迁移前 API 仅保存在
[设计决策 / 历史归档](../design-decisions/archive/2026-08-27-pre-v3/README.md)，不得作为
当前接入依据。
