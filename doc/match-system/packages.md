# `matchsystem` 包级文档矩阵

这里的文档以当前工作树源码为准，按包提供三件套。`pkg/matchsystem` 及下表六个子包
均可从模块 `github.com/xianyu11-x/matchSystem` 导入其导出 API；`pkg/client`、`pkg/common`
和 `pkg/identity` 的职责与导入方式见 [Go 接入指南](go-consumer.md)。

| 包 | 架构说明 | 代码索引 | 使用指南 |
| --- | --- | --- | --- |
| `pkg/client` | [路由身份](client-router.md#路由身份) | [主要入口](client-router.md#主要入口) | [快照发布](client-router.md#从物理节点快照发布路由) · [路由选择策略](client-router.md#路由选择策略) |
| `pkg/identity` | [共享规则与部署身份](client-router.md#路由身份) | [数值身份和校验](../../pkg/identity/key.go) | [Go 接入](go-consumer.md#包与宿主边界) |
| `pkg/matchsystem` | [architecture](architecture.md) | [code-reference](code-reference.md) · [PhysicalNode API](code-reference.md#3-physicalnode-api) | [usage-guide](usage-guide.md) |
| `pkg/matchsystem/contract` | [architecture](contract/architecture.md) | [code-reference](contract/code-reference.md) | [usage-guide](contract/usage-guide.md) |
| `pkg/matchsystem/evaluation` | [architecture](evaluation/architecture.md) | [code-reference](evaluation/code-reference.md) | [usage-guide](evaluation/usage-guide.md) |
| `pkg/matchsystem/expression` | [architecture](expression/architecture.md) | [code-reference](expression/code-reference.md) | [usage-guide](expression/usage-guide.md) |
| `pkg/matchsystem/fact` | [architecture](fact/architecture.md) | [code-reference](fact/code-reference.md) | [usage-guide](fact/usage-guide.md) |
| `pkg/matchsystem/jsonstrict` | [architecture](jsonstrict/architecture.md) | [code-reference](jsonstrict/code-reference.md) | [usage-guide](jsonstrict/usage-guide.md) |
| `pkg/matchsystem/prefilter` | [architecture](prefilter/architecture.md) | [code-reference](prefilter/code-reference.md) | [usage-guide](prefilter/usage-guide.md) |

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
