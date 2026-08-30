# 生产架构冗余最终评估（评估基线）

> 本报告保留原始架构审计的规模口径，但运行时结论已按当前重构后的源码更新：
> `ticketStore` 封装 Ticket 生命周期，`seedEvaluator` 封装一次 seed 的完整评估，
> `LogicalNode` 只协调状态、轮次和成功 Match 提交。当前测试与验证结果以仓库实际命令为准。

## 结论摘要

原 [expression、prefilter、evaluation 拆分执行参考](../archive/2026-08-27-pre-v3/doc/architecture/expression-engine-simplification-refactor-plan.md)
仍然具有实施价值，但它的高价值核心重构已经落地：统一 RuleJSON 入口和 Contract、
统一标量表达式编译、去除旧 IR/registry、Prefilter 私有拥有 Bitmap、Evaluation 只保留
两个 Bool 谓词，以及 Match Fact 全部转由 Provider 生成。评分和 Seed 选择也已收敛为
RuleJSON 中的内置类型与参数。

继续把 Bitmap 与 Bool 完全合并为一个跨域 expression/compiler，反而会把 Roaring、索引、
anchor、sidecar 和 Bitmap 执行语义重新推入通用 expression 包，增加依赖和概念数量。
这部分继续实施价值低、反作用高，不建议作为下一轮重构目标。

当前剩余冗余主要是局部死字段、死 API 和重复便捷入口；它们不构成新的架构层。
Ticket membership/lifecycle 与评估流程已分别收敛到 `ticketStore`、`seedEvaluator`，
不再存在独立的 Facts 注入 seam。

## 1. 快照范围与规模口径

统计范围为仓库当前 `cmd/`、`internal/` 下的非测试 `*.go`，不含 `*_test.go`、文档、
归档、生成物和依赖目录。物理行包含空行，非空行统计去掉空白行。沿用本轮审计的
PowerShell 逐文件统计口径，得到：

| 项目 | 文件数 | 物理行 | 非空行 |
| --- | ---: | ---: | ---: |
| 生产 Go 文件 | 43 | 9810 | 9098 |
| 测试文件 | 0 | — | — |
| 功能支持文件 | 5 | — | — |
| 纯功能文件合计（Go + 支持文件） | 48 | — | — |

5 个功能支持文件为 `Makefile`、`build.bat`、`go.mod`、`go.sum` 和
`scripts/check-expression-deps.ps1`。若把仓库元数据 `.gitignore` 也计入文件数，
是 49 个；它不是纯功能文件，因此不计入上面的 48。`doc/` 和 `doc/archive/` 均不计入
生产规模。

### 按包复算

| 包 | Go 文件数 | 物理行 | 非空行 | 观察 |
| --- | ---: | ---: | ---: | --- |
| `cmd/app` | 1 | 168 | 153 | 可运行 demo |
| `internal/client` | 2 | 238 | 213 | 路由客户端 |
| `internal/common` | 2 | 100 | 90 | 传输边界值对象 |
| `internal/identity` | 1 | 88 | 74 | 稳定身份键 |
| `internal/matchsystem` | 11 | 1869 | 1710 | Logical/Physical 编排、策略和 API alias |
| `internal/matchsystem/contract` | 2 | 670 | 635 | 唯一 Contract 模型与校验 |
| `internal/matchsystem/evaluation` | 2 | 737 | 683 | 两个阶段谓词及上下文适配 |
| `internal/matchsystem/expression` | 4 | 2451 | 2300 | 共享 Scalar parser/compiler/program |
| `internal/matchsystem/fact` | 3 | 469 | 424 | Fact 模型、Frame、Provider |
| `internal/matchsystem/jsonstrict` | 1 | 164 | 154 | 结构化 JSON 检查 |
| `internal/matchsystem/prefilter` | 14 | 2856 | 2662 | Bitmap 编译、索引和运行时 |
| **合计** | **43** | **9810** | **9098** | — |

Prefilter 的 14 个文件主要对应不同生命周期或所有权边界，并不等于 14 个架构层。
文件数本身不是当前主要成本；见“文件拆分”一节。

## 2. 依赖边界与编译路径

### 2.1 Contract 已经是单一来源

[contract.Contract](../../internal/matchsystem/contract/contract.go) 是当前唯一的
`logical-node-contract/v3` 模型和 parser。`CompileRuleJSON` 先解析并校验 RuleJSON 中的
`contract` section，随后把同一份不可变 schema 传给 Prefilter 与 Evaluation：

```text
CompileRuleJSON(match-rule/v1)
  ├─ contract.Parse(rule.contract)
  ├─ Prefilter Compile(rule.prefilter)
  │    └─ expression.CompileScalarJSON(expression-scalar/v3)
  ├─ Evaluation Compile(rule.evaluation)
  │    └─ expression.CompileScalarJSON(expression-scalar/v3)
  ├─ built-in scoring Compile(rule.scoring)
  └─ built-in Seed selection Compile(rule.seedSelection)
```

因此当前不存在多版本 Contract，也不存在并列的规则配置文件。Wire 层以
`match-rule/v1` 为唯一规则 envelope；`logical-node-contract/v3`、`prefilter/v3`、
`evaluation/v3` 和 `expression-scalar/v3` 是其中的嵌套 section/sub-expression schema，
分别表达 Contract、Bitmap、阶段谓词和 Scalar 子表达式。

### 2.2 Expression 只有一套通用 Scalar compiler

[expression JSON compiler](../../internal/matchsystem/expression/json.go) 的入口是
`CompileScalarJSON`。它只允许 `bool`、`int64`、`strings`、`uint64s` 四类结果，且会
拒绝 Bitmap root。Program 对外是不可变，不暴露 expression 节点或指令句柄。

Prefilter 的 `bitmapCompiler` 是 Bitmap 领域 compiler，不是第二套通用 Scalar compiler。
`prefilter.NewJSONCompiler(...).Compile(...)` 与 `prefilter.CompileJSON(...)` 只是同一实现的
两种调用入口，属于 API 重复，不是双 compiler。

Evaluation 的 `CompileJSON` 也直接调用共享 Scalar compiler；它增加的是 phase/source
权限、输入校验和 Lookup 映射，不是另建一套表达式内核。

当前 [依赖边界检查脚本](../../scripts/check-expression-deps.ps1) 没有发现旧 Arena、
NodeRef、InstructionID、DomainLeaf、Registry 或旧兼容包符号残留。

## 3. 运行时路径与 Provider-only 结论

当前 [seedEvaluator 编排](../../internal/matchsystem/seed_evaluator.go) 与
[LogicalNode 提交](../../internal/matchsystem/logical_node.go) 的有效路径是：

```text
Tick FactProvider
  -> MatchFactProvider.Initialize
  -> CanComplete（seed-only）
  -> Prefilter
  -> RuleJSON.scoring 的内置评分
  -> CanJoin
  -> MatchFactProvider.OnJoin
  -> clone（Provider 契约由测试阶段验证）
  -> CanComplete
  -> evaluator 返回完整 Match（不修改 store）
  -> ticketStore.Commit 原子消费 Ticket 与 Prefilter membership
```

[MatchFactProvider](../../internal/matchsystem/fact/provider.go) 只有 `Initialize` 和
`OnJoin`，两者都返回完整 `fact.Values` 快照。当前生产代码没有 `mergeMatchFacts`、
patch/fallback、Evaluation 更新入口或 LogicalNode 直接写入 Match Fact 的路径。

`common.MatchFacts` 只在 commit/output 边界保留一份镜像，这是因为 `fact` 已依赖
`common`，直接替换会产生包循环；它不是第二条更新路径。

`seedEvaluator` 的输入只来自本次 `BeginSession` 创建的 Fact Frame、当前 seed 和
只读 `seedStoreReader`；它不接受调用方注入的 Tick Facts，也不改变 Ticket membership。
`LogicalNode.ProduceMatch` 负责 seed cursor/attempt budget，并在 evaluator 返回完整
`Match` 后调用 `ticketStore.Commit`。

评分从 RuleJSON 的 `scoring` section 编译为 LogicalNode 私有运行对象，当前支持
`constant`、`created_at`、`int64_field`；Seed 顺序从 `seedSelection` 编译，支持
`arrival`、`oldest`、`int64_priority`、`random`。宿主只提供 Tick/Object/Match Fact
Provider，不提供另一套评分或 Seed 配置来源。CanJoin 读取 seed/candidate/Tick/加入前
Match Fact，CanComplete 只读取 Tick Fact 和当前 Match Fact，均不能遍历 Match 成员。

## 4. Prefilter private Bitmap 评估

### 4.1 应保留的边界

Prefilter 私有拥有以下互相耦合的运行时能力：

- Roaring Bitmap 和 `DocSet` 执行；
- 字符串、uint64、多值和 int64 range 索引；
- AND、OR、EXCLUDE、IF 和 static-none；
- anchor 选择、scope lattice 和 exclude 约束；
- 动态 Scalar operand 的 sidecar/bind；
- 物理 probe、成本/资源限制和 Prefilter fingerprint。

这些能力集中在私有 `bitmapNode`、`bitmapQuery`、`bitmapCompiler` 和
`IndexStore/TickSession` 中，见 [expression](../../internal/matchsystem/prefilter/expression.go)、
[compiler](../../internal/matchsystem/prefilter/compiler.go) 和
[store](../../internal/matchsystem/prefilter/store.go)。把它们放进 expression 会产生
两个坏结果之一：expression 依赖 Roaring/索引，或者 expression 新增 Bitmap domain
leaf、registry、公共 IR。两者都不符合精简目标。

### 4.2 当前局部冗余

Prefilter 仍有可删的局部内容：

- `Plan.scalarPrograms` 被赋值但没有读取；
- `Plan.rootNode()` 被 staticcheck 报告为 `U1000`；
- `whenResult`、`valuesResult`、`minResult`、`maxResult` 等字段只有赋值没有运行时读取；
- `bitmapCost` 中若干统计项不参与 limits 或 fingerprint；
- `IndexStore.Len`、完整 Stats API 和若干 `DocSet` 便利方法当前没有生产消费者。

这些属于字段和 API 表面积清理，不能作为合并 Bitmap/Bool 的理由。

## 5. Evaluation、LogicalNode 与文件拆分

### 5.1 Evaluation 不应再合并进 expression

Evaluation 只有两个生产文件，且没有兼容版本、registry、workflow 或 Match Fact 更新逻辑。
它保留的复杂度来自真实的领域约束：

| 阶段 | 可读数据 |
| --- | --- |
| `CanJoin` | seed 属性/Fact、Tick Fact、当前候选属性/Fact、加入前 Match Fact |
| `CanComplete` | Tick Fact、当前 Match Fact |

Evaluation 负责把这些输入映射为 expression primitive Lookup，并在编译期限制 source
和 result type。将其塞进 expression 只会把阶段权限和 owner 语义搬进通用包，减少不了
核心概念。

### 5.2 LogicalNode 不是残留 facade/registry/IR

`LogicalNode` 是状态 owner/orchestrator，持有状态、round cursor/预算、`ticketStore`
和 `seedEvaluator`；它不直接持有评估依赖或 Ticket/Prefilter membership 字段。
`seedEvaluator` 持有 Fact Frame 依赖、Evaluation Predicates、RuleJSON 编译出的评分和
Seed 顺序实例，并只通过窄的 `seedStoreReader` 读取候选。当前没有可由宿主动态扩展的
评分/表达式 registry、Domain leaf registry、公共 IR 或多级 adapter 链；内置评分和
Seed 类型由 RuleJSON 的闭合参数集合决定。

`LogicalNodeSelector` 仍是跨 LogicalNode 的宿主调度扩展点；单个规则的 Seed 选择则由
`seedSelection` 编译为 LogicalNode 私有实例，不能由另一个 Go 配置来源覆盖。

旧的根包 Contract/Evaluation 转发 facade 已删除；生产调用方统一使用
`CompileRuleJSON`，组件包的 parser/compiler 仅由该入口在内部调用。

### 5.3 文件拆分不是主要冗余

Expression 的 compiler/json/program/schema、Fact 的 fact/frame/provider、Evaluation 的
predicates/errors、Prefilter 的 expression、compiler、query、store、index 各自对应稳定的生命周期和所有权。
仅为降低文件数量而跨边界合并，会使编译、运行时和领域约束重新混在一起。

如果发布门槛强制要求 Prefilter 从 14 个文件压到不超过 10 个，可以采用以下可选形式
合并方案：

1. `doc.go` 合入带 package comment 的 `compiler.go`；
2. `fact_adapter.go` 合入 `errors.go`；
3. `index.go`、`int64_range_index.go`、`multi_value_index.go` 合入 `indexes.go`。

这样从 14 减到 10，仍保留 `json.go`、`query.go`、`lookup.go`、`store.go`、`expression.go`、
`compiler.go` 和 `plan.go` 的边界。该方案只改变文件布局，不降低 runtime 复杂度、依赖数量或
维护风险，因此不是核心收益，不应作为架构重构验收条件。

## 6. 保留、删除、暂缓建议

### 6.1 保留

| 项目 | 证据/理由 | 若删除的风险 |
| --- | --- | --- |
| 单一 `contract.Contract` parser | [contract.go](../../internal/matchsystem/contract/contract.go) 是唯一 Contract 来源，两个领域共享同一 schema | 再次产生双模型、双校验和版本分叉 |
| expression Scalar compiler/opaque Program | Prefilter 和 Evaluation 都调用 `CompileScalarJSON`；类型、source、limits 集中管理 | 类型规则分叉，重新出现通用 IR |
| Prefilter 私有 Bitmap expression、compiler、runtime | Bitmap、Roaring、索引、anchor、sidecar 需要同一领域内优化 | expression 依赖膨胀或引入 registry/IR |
| Evaluation 两个 Bool 谓词 | phase capability 和输入 ownership 是真实语义 | CanJoin/CanComplete 权限混入通用包 |
| RuleJSON 内置评分绑定 LogicalNode | `constant`、`created_at`、`int64_field` 由统一编译入口校验并生成私有实例 | 避免宿主 callback 与规则文件产生双来源 |
| MatchFactProvider 完整快照 + clone/原子替换 | [provider.go](../../internal/matchsystem/fact/provider.go) 与 [runtime-flow.md](runtime-flow.md) 一致；契约在测试阶段用 Validator 检查 | 产生 patch/merge/半提交旁路 |
| `seedEvaluator` 固定评估顺序 | Fact、Prefilter、Top-L、评分、Evaluation 和 Provider 顺序明确；不修改 store | workflow/state machine 复杂化 |
| `ticketStore` 生命周期边界 | Ticket/DocID、Prefilter membership、arrival 和原子 Commit 集中管理 | 分散删除逻辑导致部分提交 |
| LogicalNodeSelector 与 RuleJSON Seed 选择 | 前者属于节点调度，后者是 `arrival`、`oldest`、`int64_priority`、`random` 的闭合配置 | 将调度实现硬编码，降低实际可替换性 |

### 6.2 删除候选（按优先级）

下表是建议清理项，不代表本次已执行。P1/P2/P3 分别表示低风险立即清理、需要消费
者确认的 API 清理、需要基准或发布策略确认的清理。

| 优先级 | 删除/收缩项 | 证据 | 收益 | 风险与门槛 |
| --- | --- | --- | --- | --- |
| P1 | 删除 `Plan.rootNode()`、`Plan.scalarPrograms` | `rootNode` 是当前 staticcheck 唯一 U1000；`scalarPrograms` 只写不读 | 缩小 Plan 状态，去掉死代码 | 先确认 canonical、runtime、文档无反射/外部同包消费者 |
| P1 | 删除 Bitmap 死结果字段及未使用 cost counters | `whenResult`、`valuesResult`、`minResult`、`maxResult` 等无读取；多数计数不参与限制 | 减少编译状态和误导性指标 | 保留 limits/fingerprint 真实依赖，先做字段引用扫描 |
| P1 | 删除 `fact.Frame.View`、根 `FactView` alias 和未使用 Fact wrapper | 当前无生产调用者，且容易被误解为额外 Fact 读取路径 | 强化 Provider-only 表面，减少 API | 先确认部署/同仓库工具没有调用；当前包为 `internal` |
| P1 | 删除 `expression.StrictProfile`、`ProgramCost.Within`、`prefilter.DefaultJSONLimits` | 都是无生产消费者的 alias/forward convenience | 降低公共表面积 | 文档和内部示例需同步，外部模块不能直接导入 internal |
| P2 | Prefilter 只保留一个 JSON 编译入口 | `NewJSONCompiler(...).Compile` 与 `CompileJSON` 逻辑相同 | 减少 API 选择和文档分叉 | 若需要同一 schema 批量编译，可保留 wrapper；先盘点消费者 |
| P2 | 删除 `IndexStore.Len`、Stats/CandidatesWithStats、未使用 DocSet 便利方法 | 当前生产代码没有诊断消费者 | 缩小运行时观测 API | 可能有外部 benchmark/运维工具；需显式迁移或保留为 diagnostic API |
| P2 | 保持 RuleJSON 作为唯一生产编译入口 | `CompileRuleJSON` 已统一 Contract、Prefilter、Evaluation、评分、Seed 和 runtime；组件 compiler 只由聚合入口调用 | 减少 facade 表面和配置分叉 | 组件包 API 仅供内部实现使用，新增调用方应先确认是否真正需要 |
| P3 | 评估 Seed 顺序实现的分配/排序路径 | RuleJSON 内置顺序需要在大队列上保持稳定吞吐 | 控制大队列 CPU/内存成本 | 必须先 benchmark，再决定是否调整实现 |
| P3 | 合并少量形式文件（可选） | `doc.go`、`fact_adapter.go`、索引文件可以按上节合并 | 满足硬文件预算 | 对 runtime/依赖无收益，可能降低定位性；不得作为核心验收 |

### 6.3 暂缓

| 事项 | 暂缓理由 |
| --- | --- |
| Bitmap 与 Bool 完全合并到 expression | 低收益、高反作用，会引入 Bitmap domain leaf、Roaring/索引依赖或公共 IR |
| Evaluation 合并到 expression 或 LogicalNode | Evaluation 的 phase/source/input ownership 是必要领域适配，不是重复 compiler |
| 抽取所有 JSON helper/limits 到新的公共包 | 错误路径、版本校验和资源限制虽相似但并不完全相同，抽取会形成新的抽象层 |
| 将 `common.MatchFacts` 直接替换为 `fact.Values` | 当前包依赖方向会形成循环；它只是 output 镜像，不是更新模型问题 |
| 删除 Provider Fact 的生产 Validator 调用 | Provider 属于同仓库可信实现；保留 Validator 作为测试/调试工具，生产路径只保留必要 clone | 把契约错误推迟到测试阶段，需确保 Provider 契约测试覆盖 |
| 立即删除 optimized seed order fast path | 缺少 benchmark，性能风险大于当前代码节省 |
| 为达到文件数目标大规模重排目录 | 文件数不等于依赖复杂度，跨生命周期合并会降低可维护性 |

## 7. 静态检查与当前验证证据

### 7.1 Staticcheck

当前 `staticcheck` 结果只有：

- `internal/matchsystem/prefilter/expression.go:163`：`(*Plan).rootNode`，`U1000`，属于本
  报告建议 P1 删除项；
- `internal/client/route_table.go:46`、`:67`：两个 `ST1005`，是 client 包错误消息
  大写风格问题，与本次表达式/Provider/Prefilter 架构无关。

除上述 1 个 matchsystem 内部 U1000 和 2 个无关 client ST1005 外，没有发现旧 compiler、
registry、IR 或 Provider 旁路相关静态检查问题。

### 7.2 删除测试后的可执行验证

当前工作树已恢复并补充与本次变更相称的 Fact、Prefilter、Evaluation、Frame 和端到端
Provider 测试。当前验证证据包括：

- `go test -count=1 ./...` 通过，包含可信 Provider 快照和表达式缺失值覆盖；
- `go vet ./...` 通过；
- `git diff --check` 通过。

归档文档中的历史单元/集成、loader matrix、golden 和 Fuzz 记录仍保留在
[release-validation.md](../release-validation.md)，不能替代当前工作树的测试资产。

## 8. 最终判断与下一步门槛

### 最终判断

当前生产快照不是“仍有多套大架构并行”的状态，而是：

```text
Contract 单一来源
  -> expression 单一 Scalar compiler
  -> Prefilter 私有 Bitmap compiler/runtime
  -> Evaluation 两个 Bool phase adapter
  -> LogicalNode 固定流程
  -> MatchFactProvider 唯一事实生成入口
```

因此，原方案中精简边界的核心重构具有实施价值且已经落地；继续完全合并 Bitmap/Bool
不具有足够实施价值。下一步最多执行 P1/P2 局部清理，P3 等待性能和发布策略证据。

### 下一步门槛

本报告的后续生产清理仍应以测试和静态检查为门槛。当前 Provider Fact 信任边界的验证
资产至少包括：

1. Contract、Scalar、Prefilter、Evaluation 的正/负 loader 和 limits 验证；
2. 固定运行时顺序、Provider 完整快照、失败 fail-closed 和原子提交验证；
3. Prefilter Bitmap/索引结果、Evaluation 输入权限和 Provider-only 负向验证；
4. 关键 golden/Fuzz，必要时补 benchmark 以评估 seed-order fast path；
5. 清理前后重新执行 `go vet`、`go build`、依赖边界检查、demo 和发布记录。

在上述资产恢复或重建前，不应删除生产字段、改变 compiler 入口、调整 Provider seam，
也不应以“编译通过”标记架构清理完成。
