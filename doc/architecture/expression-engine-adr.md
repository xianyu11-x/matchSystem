# ADR：表达式、Prefilter 与 Evaluation 的已落地边界

- 状态：Accepted / Implemented
- 日期：2026-08-27
- 范围：`expression`、`prefilter`、`evaluation`、`LogicalNode`、Fact 生命周期

这是一份决策记录，不是迁移计划。当前实现只有这一组生产契约：业务声明为
`logical-node-contract/v3`，标量表达式为 `expression-scalar/v3`，候选预筛为
`prefilter/v3`，评估谓词为 `evaluation/v3`。

## 决策摘要

1. Contract 只有一个来源。`LogicalNodeSpec` 接收 JSON，创建节点时解析并校验一次，
   随后的两个编译器使用同一份不可变快照。
2. 表达式编译集中在 `internal/matchsystem/expression`。它只提供四种标量结果和
   primitive lookup；Bitmap、索引和 Match 生命周期不进入该包。
3. Prefilter 私有拥有 Bitmap expression、索引查询 sidecar 和 Roaring 执行；它返回不可变
   `Plan`，没有候选扫描兜底。
4. Evaluation 只编译 `canJoin` 与 `canComplete` 两个 Bool 谓词。候选评分是直接
   绑定在 `LogicalNodeSpec` 上的一个 Go `CandidateScorer`，不进入 JSON 或注册表。
5. Match Fact 只有 `MatchFactProvider.Initialize/OnJoin` 可以产生。每次调用返回
   完整快照，LogicalNode 校验、拥有并原子替换；表达式和 Evaluation 不写 Fact。
6. 配置不做双读、隐式降级或运行时版本迁移；不符合当前 schema 的输入直接拒绝。

## 共享 Contract

Contract JSON 的顶层字段是 `schemaVersion`、`attributes`、`facts`、`indexes`，可选
`limits`。Attribute 和 Fact 类型为 `strings`、`int64`、`uint64s`；多值类型必须
声明正数 `maxValues`，`int64` 不接受该字段。名称在整个 Contract 中唯一。

Fact 的 scope 只有 `tick`、`object`、`match`。索引只有：

- `multi_value`：`keyType` 为 `string` 或 `uint64`，有正数的文档值和查询值上限，
  且 key 类型必须匹配 Attribute。
- `int64_range`：只能使用 `int64` Attribute 名，不接受多值索引参数。

JSON 结构、名称、类型、scope、索引 Attribute 和资源上限由 Contract 统一校验。详情见
[Contract](../logical-node-contract.md) 和
[contract 实现](../../internal/matchsystem/contract/contract.go)。

## 共享标量表达式

`expression-scalar/v3` 的 envelope 必须含 `schemaVersion`、`resultType`、`expr`。
`resultType` 显式界定 root 是 `bool`、`int64`、`strings` 或 `uint64s`。四种结果
可以作为父节点的 typed operand，但只有 `bool` root 才能直接作为 Evaluation 谓词。
合法节点和 source 约束由一个 compiler 检查，结果 Program 不暴露 expression 节点或内部句柄。

这意味着 `int64_literal`、`int64_ref`、集合 literal/ref 并不会被错误地当作 Bool；
它们只能在匹配的比较、集合谓词或调用方允许的 operand 位置出现。

实现入口：[expression schema](../../internal/matchsystem/expression/schema.go)、
[JSON compiler](../../internal/matchsystem/expression/json.go)、
[compiler](../../internal/matchsystem/expression/compiler.go)、
[immutable Program](../../internal/matchsystem/expression/program.go)。

## 两个领域编译器

Prefilter envelope 的 root 明确为 `resultType: "bitmap"`，Bitmap 节点和 scalar
operand 分别按各自的闭合 grammar 校验。Evaluation envelope 只含两个 scalar Bool
root，并为每个 root 建立 source capability profile。

Prefilter 可从 `seed_attributes`、`seed_facts`、`tick_facts` 读取；Evaluation 的
`CanJoin` 可读 seed 属性、seed Fact、Tick Fact、candidate 属性、candidate Fact 和
当前 Match Fact；`CanComplete` 只能读 Tick Fact 与 Match Fact。表达式只拿到 primitive
值，不能取得 Ticket、Match 成员、Provider 或索引对象。

Prefilter 的物理实现见 [Prefilter](../prefilter.md) 和
[prefilter compiler](../../internal/matchsystem/prefilter/compiler.go)。Evaluation
的谓词实现见 [Evaluation](../evaluation.md) 和
[evaluation/predicates.go](../../internal/matchsystem/evaluation/predicates.go)。

## Match Fact 与固定流程

每个拥有 Match scope Fact 的节点必须绑定一个 `MatchFactProvider`。`Initialize` 为
seed 返回初始完整快照；`OnJoin` 收到当前快照的 clone 和 candidate 输入，返回加入
candidate 后的下一份完整快照。代码为每次 callback 创建输入的 deep clone，因此 callback
对输入副本的修改不会反向改变 owner 状态；接口约定这些值只在本次调用中按只读数据使用。
Go 接口本身不强制实现不能保存指针。

LogicalNode 在 Provider 返回后执行完整 Contract 校验，再以一次状态转换同时接受
candidate 和新快照。Provider error、panic、取消、缺失字段、类型/scope 错误或超限
都会使当前尝试 fail closed；不会 patch、半提交、重试旧逻辑或回看 Match 成员。
成功加入后后续 predicate 只使用新的 Match Fact；已存在成员的 Ticket、属性和
Object Fact 不会被重新放入表达式输入。

固定调用顺序和错误语义见 [运行时流程](runtime-flow.md) 与
[Match Fact Provider](../match-fact-provider.md)。核心编排在
[logical_node_core.go](../../internal/matchsystem/logical_node_core.go)。

## 身份与发布边界

标量 Program 是编译后的不可变值；Prefilter Plan 的身份是
`prefilter-fingerprint/v5`，包含规范化的 Bitmap、实际索引依赖、Fact/Attribute
依赖、有效限制和物理 probe 参数，不包含运行时 Fact 值、Provider 实现、scorer 或
当前候选集合。当前核心不负责配置存储、热加载、revision 或原子 swap；发布与回滚
由上层持有并绑定完整配置包。见 [发布与验证](../release-validation.md)。

## 结果

该边界保留了领域真正需要的代码：一个标量 compiler、一个私有 Bitmap compiler、
两个 Bool predicate 和明确的 LogicalNode 编排。没有跨领域通用 IR、运行时 leaf
注册表、Match Fact patch 语言或第二套 Contract。
