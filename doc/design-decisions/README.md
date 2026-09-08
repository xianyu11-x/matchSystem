# 设计决策

本分类记录匹配系统演进中的决策依据、设计变更、约束和验证证据。这里解释“为什么”，
当前的“怎么用”仍以[匹配系统](../match-system/README.md)和
[模拟器](../simulator/README.md)文档为准。

## 决策与评估

- [设计变更记录](change-log.md)
- [ADR：模拟器表单与图表交互](adr/simulator-form-configuration.md)
- [ADR：Windows 便携客户端更新](adr/portable-desktop-update.md)
- [ADR：表达式、Prefilter 与 Evaluation 的边界](adr/expression-engine-boundaries.md)
- [ADR：成局等待与调用耗时分离](adr/match-processing-observation.md)
- [属性生成采用精确序号抽样](adr/attribute-generation.md)
- [ADR：场景拥有持续流量调度](adr/continuous-traffic.md)
- [生产架构冗余评估](assessments/production-redundancy.md)
- [系统设计约束](constraints/system-constraints.md)

## 测试与验证记录

- [功能验证矩阵](testing/functional-validation.md)
- [匹配池规模性能基准](testing/performance-benchmark.md)
- [发布与回滚验证](testing/release-validation.md)
- [Windows 发布打包验收](testing/client-release-packaging.md)
- [原生更新器迁移验收](testing/native-updater-acceptance.md)
- [模拟器增强验收记录](testing/simulator-enhancements-acceptance.md)

测试记录描述特定代码和环境下的证据，不自动构成跨环境 SLA。复现时应记录提交、
操作系统、Go/Node/Rust 版本、命令和参数。

## 历史归档

- [2026-08-27 pre-v3 快照](archive/2026-08-27-pre-v3/README.md)

归档中的 Builder、Arena、DomainDescriptors、旧 Program API 和旧 schema 只用于回溯设计
变化。归档内容保持原始语境，不随当前实现持续更新。
