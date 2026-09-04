# `internal/matchsystem` 包级文档矩阵

这里的文档以当前工作树源码为准，按包提供三件套：

| 包 | 架构说明 | 代码索引 | 使用指南 |
| --- | --- | --- | --- |
| 根包 internal/matchsystem | [architecture](architecture.md) | [code-reference](code-reference.md) | [usage-guide](usage-guide.md) |
| contract | [architecture](contract/architecture.md) | [code-reference](contract/code-reference.md) | [usage-guide](contract/usage-guide.md) |
| evaluation | [architecture](evaluation/architecture.md) | [code-reference](evaluation/code-reference.md) | [usage-guide](evaluation/usage-guide.md) |
| expression | [architecture](expression/architecture.md) | [code-reference](expression/code-reference.md) | [usage-guide](expression/usage-guide.md) |
| fact | [architecture](fact/architecture.md) | [code-reference](fact/code-reference.md) | [usage-guide](fact/usage-guide.md) |
| jsonstrict | [architecture](jsonstrict/architecture.md) | [code-reference](jsonstrict/code-reference.md) | [usage-guide](jsonstrict/usage-guide.md) |
| prefilter | [architecture](prefilter/architecture.md) | [code-reference](prefilter/code-reference.md) | [usage-guide](prefilter/usage-guide.md) |

## 阅读顺序

新接入者建议先读根包架构和使用指南，再按实际配置链阅读 Contract →
Prefilter/Expression → Fact → Evaluation。需要查单个符号时使用对应 code-reference。

这些文档特别记录了当前 v3 边界：

- Contract 只接受 logical-node-contract/v3；
- Expression 只接受 expression-scalar/v3，ScalarProgram 不透明；
- Prefilter 只接受 prefilter/v3，使用私有 Bitmap tree 和嵌套 scalar operand；
- Evaluation 只接受 evaluation/v3，固定 CanJoin/CanComplete 两个 Bool root；
- 所有运行时 owner、Fact 快照和错误边界均以源码为准。

`doc/design-decisions/archive/2026-08-27-pre-v3/` 只用于历史对照；归档 Prefilter 中的 Builder、Arena、
DomainDescriptors 和 Program API 不应照搬到当前代码。
