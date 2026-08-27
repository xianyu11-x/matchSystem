# expression、prefilter、evaluation 拆分执行参考

> 用途：供重构 Agent 审阅、实施和验收。
>
> 基线：2026-08-26；当前版本为 logical-node-contract/v2、prefilter/v2、evaluation/v2、prefilter-fingerprint/v4。
>
> P0 冻结 ADR：[表达式简化与 Prefilter/Evaluation v3 契约](expression-engine-v3-adr.md)。
> 目标版本、最终 JSON shape、Provider 语义、错误码和事件顺序以 ADR 为准：
> `prefilter/v3`、`evaluation/v3`、`expression-scalar/v1`、
> `prefilter-fingerprint/v5`；`logical-node-contract/v2` shape/语义不变并继续接受。

## 1. 最终裁决

保留现有 expression、prefilter、evaluation 领域包，不新增 valueprogram、workflow、provider、matchfact 子包。

Value Program 只是概念名；expression 是唯一内核，不新增 ValueProgram、BoolProgram 或平行程序类型。

expression 只产生 Bool、Int64、Strings、Uint64s 四类值，负责 parser、类型检查、compiler、evaluator、canonical、依赖和 limits。

prefilter 独占 Bitmap、候选集合、索引查询、anchor、sidecar 和物理执行；最终 Bitmap 只属于 prefilter。

evaluation 只保留 CanJoin、CanComplete 两个 Bool 谓词，不负责 MatchFact 更新、阶段状态、scorer registry 或 workflow。

LogicalNode 拥有固定流程；scorer 在 Prefilter 之后、CanJoin 之前。

MatchFact 更新全部且只能通过外部 MatchFactProvider 完成。这是不可协商的目标约束，不是可选迁移路径：所有 Initialize 和每次候选加入后的 OnJoin 都必须有对应 Provider 实现，不得保留表达式更新、LogicalNode 直接写入、patch/merge fallback 或“暂不接 Provider”的生产路径。

Provider 只处理 Initialize、OnJoin 并返回完整 fact.Values；结果校验成功后整体替换，失败保留旧值。Provider 不依赖 expression。

公开生产配置入口裁决为 JSON-only，目标 loader 只接受最新 ADR 格式。

旧配置不兼容，不做双读、双写、运行时升级、旧 parser/builder fallback 或隐式自动迁移。

所有生产配置、示例、typed Go 生产路径和部署输入一次性对齐最新格式。

## 2. 当前代码基线

### 2.1 版本、兼容和证据

当前四个版本仅是迁移输入基线，不自动成为目标版本。

目标 Prefilter/Evaluation 版本已由 [P0 ADR](expression-engine-v3-adr.md) 冻结为
`prefilter/v3` 和 `evaluation/v3`；不得再引入其它未记录的目标版本。

Contract v2 只有在 ADR 确认 shape 和语义不变时才能继续接受，否则定义新版本并拒绝 Contract v2。

ParseOptions alias、optional Arena、legacy NodeID 兼容入口已经清理，是当前基线，不得回退。

代码证据优先以文件、类型和符号名定位，避免重构后复制虚假行号：

- expression/arena.go：ResultType、Kind、Arena 和 Bitmap 结构。
- expression/compiler.go：Compiler、Compile、DomainLeaf 编译和程序产物。
- expression/program.go：Value、叶求值器、Evaluate 和类型求值。
- prefilter/compiler.go：Plan、BitmapExpr、编译和 fingerprint。
- prefilter/query.go：compiledIndexQuery、operand bind 和 sidecar。
- prefilter/store.go：IndexStore、TickSession、Bitmap 执行。
- evaluation/config.go：MatchFactsConfig、Phase、AllowedPhases、DomainRegistry。
- evaluation/compile.go：Compile、Join、Complete、Initialize、OnJoin 和更新计算。
- evaluation/validation.go：ValidatedContext 和阶段验证。
- fact/fact.go：Values、Clone 和完整快照基础。
- logical_node_core.go：seed、topCandidates、Join 和事实合并路径。

### 2.2 当前真实流程

当前实现必须写成以下事实，不能把目标 Provider 当成现状：

~~~text
Evaluation.Initialize
  -> seed-only Evaluation.Complete
  -> Prefilter
  -> scorer
  -> Evaluation.Join
  -> Evaluation.OnJoin 计算完整更新集合
  -> logical_node_core.mergeMatchFacts
  -> Evaluation.Complete
  -> 循环末尾当前重复 Complete
~~~

scorer 当前位于 Prefilter 之后、CanJoin 之前；seed-only Complete、每次 OnJoin 后 Complete、循环末尾重复 Complete 都是迁移 trace oracle，未完成对照前不得悄然删除。

当前 Evaluation.OnJoin 计算更新集合，logical_node_core.mergeMatchFacts 合并并校验；这两点是待迁移事实，不是目标边界。
它们不构成目标豁免；迁移完成后任何 MatchFact 更新都必须从 Evaluation 和 LogicalNode 的旧更新路径移除，统一由 MatchFactProvider 承担。

### 2.3 目标流程映射

目标由 LogicalNode 固定编排，Provider 映射单独记录：

~~~text
MatchFactProvider.Initialize（seed 属性/Fact、Tick Fact）
  -> 校验并原子替换完整 fact.Values
  -> seed-only CanComplete
  -> Prefilter
  -> scorer
  -> CanJoin
  -> MatchFactProvider.OnJoin（seed、Tick、当前候选属性/Fact、加入前 MatchFact）
  -> 校验完整快照，并原子提交候选与新 fact.Values
  -> CanComplete
  -> 迁移 oracle 验证前保留当前循环末尾重复 Complete
~~~

目标不再由 Evaluation 计算更新集合，也不再由 mergeMatchFacts 拼接 Provider 结果。OnJoin 成功前候选仍是待加入对象；Provider 失败时候选不加入且旧 MatchFact 保持不变。

scorer 直接注入 LogicalNodeSpec，或由上层先解析为回调再注入；Evaluation 不设 scorer registry。

### 2.4 包和文件盘点

以下是主 Agent 按 PowerShell Get-Content 物理行统计的近似膨胀基线，仅作观察，不是精确契约、上限或趋势承诺：

| 包 | 生产文件数 | 物理行数近似 | 主要膨胀来源 |
| --- | ---: | ---: | --- |
| expression | 8 | ~6000 | Bitmap、DomainLeaf、泛化 IR、多个编译结果视图 |
| prefilter | 13 | ~2265 | Plan、query、store、索引和 JSON 适配层 |
| evaluation | 6 | ~1695 | 旧阶段更新表达式、DomainRegistry、验证上下文 |
| fact | 2 | ~337 | Values、Frame、tick/object 边界和校验 |

主 Agent 实施前应重新 Get-Content 复核；不要使用旧 checkpoint 的伪精确行数。

## 3. 领域边界

### 3.1 依赖方向

~~~text
expression
   ^                 ^
   |                 |
prefilter       evaluation
   \                 /
       LogicalNode
            |
     MatchFactProvider
            |
          fact
~~~

expression 不依赖 prefilter、evaluation 或 LogicalNode。

prefilter 可调用 expression 的纯值 Program，但不读取 Evaluation 类型、Phase 或更新接口。

evaluation 可调用 expression 的 Bool Program，但不读取 Bitmap、IndexStore、QuerySidecar 或 Prefilter Plan。

Provider 不接收 expression AST、Program、Value、patch 或 transaction，也不接收 Match、已有成员列表、成员迭代器或可用于回查成员的句柄；LogicalNode 可以组合领域，但不得拼装第三种跨包 IR。

### 3.2 所有权

| 能力 | 唯一所有者 | 允许消费者 |
| --- | --- | --- |
| 标量 AST、类型、解析、编译、求值、canonical | expression | prefilter、evaluation |
| Bitmap AST、候选集合、anchor、sidecar、索引 | prefilter | LogicalNode |
| CanJoin、CanComplete | evaluation | LogicalNode |
| 固定调用顺序和 scorer 调用 | LogicalNode | 外部 scorer 回调 |
| 完整 MatchFact 快照生成 | Provider | LogicalNode |
| MatchFact 更新与提交 | Provider 计算、LogicalNode 原子提交 | evaluation 只读 |
| 快照模型 | fact.Values | Provider、LogicalNode、只读谓词 |

核心身份只保留 expression canonical 与 prefilter fingerprint；部署 release bundle 若存在，只是可选的外部记录，不属于核心包契约。

不改变匹配规则、候选排序或索引系统；不把 Bitmap 降级为逐文档 scalar 扫描；不增加 Evaluation 第三个顶层表达式入口。

## 4. 最小目标模型

### 4.1 expression

Value Program 只是描述，expression.Program 是现有载体；目标是收缩公共视图而非新建程序类型。

Program 可产生四类值；Evaluation 根节点必须是 Bool，其他三类只能作为两个 Bool 谓词的内部子表达式。

expression 不负责 Bitmap、Roaring、索引、scope、anchor、sidecar、Provider 写入、patch 或 scorer。

CanJoin/CanComplete 如需参与表达式，只通过只读 Lookup 将 MatchFact 转为 expression.Value；表达式不能写入 MatchFact，Provider 本身不依赖 expression。

### 4.2 prefilter

Prefilter loader 解析 Bitmap/query AST，运行时选择 anchor，并负责 And、Or、Exclude、If、None、索引叶子、sidecar、scope、估算和 Roaring 执行。

动态 operand 规则固定为：

1. loader 解析动态值表达式。
2. 每个动态值表达式交 expression 编译为 opaque Program。
3. Prefilter 只持有 Program 和期望 ResultType。
4. Prefilter 在自己的上下文中求值并绑定查询。

动态 operand 是内部实现，不公开 InstructionID、NodeRef、DomainLeaf、CompileProfile 或其他通用 IR 协议。

允许 Prefilter 内部 canonical 去重，但该 canonical 不是公共契约。

P1/P2 可暂存 expression 的 Bitmap 窄视图，以避免与 Provider/Evaluation 同批大搬迁；桥接必须在 P3 删除。

### 4.3 evaluation

目标公开入口是 JSON-only；Evaluation 删除公开 typed Config、Arena、Root builder。

CompileJSON 解析最新 JSON，接收 contract.Contract，并返回薄 Predicates；Predicates 只暴露两个求值方法，不暴露原始 Program，不是 workflow、state machine 或状态容器。

~~~go
type Predicates struct {
    canJoin     *expression.Program
    canComplete *expression.Program
}

func CompileJSON(data []byte, c contract.Contract) (Predicates, error)
func (p Predicates) CanJoin(input CanJoinInput) (bool, error)
func (p Predicates) CanComplete(input CanCompleteInput) (bool, error)
~~~

CanJoin 只读 seed attr/fact、当前轮次 Tick Fact、candidate attr/fact 和候选加入前的 MatchFact；它不能读取或遍历 Match 成员。

CanComplete 只读 tick 和 match facts。

不得把任意 LogicalNode、Provider 或 Prefilter 状态注入 Evaluation。

Predicates 不执行 Initialize、OnJoin、Update、Advance 或 Complete。

scorer 由 LogicalNodeSpec 直接注入，或由上层先解析为回调；Evaluation 不提供 scorer registry。

### 4.4 MatchFactProvider

MatchFactProvider 是所有 MatchFact 更新的唯一入口，属于强制目标边界。对声明了 Match Fact 的规则，每个 LogicalNode 必须绑定一个 Provider；不得按规则、阶段或部署开关选择“仍由 Evaluation 更新”。Provider 实现放在 fact 包或应用编排边界（二选一由依赖方向决定），但不新增 provider 子包或 Provider registry。

~~~go
type MatchFactProvider interface {
    Initialize(ctx context.Context, input InitializeInput) (fact.Values, error)
    OnJoin(ctx context.Context, input JoinInput) (fact.Values, error)
}

type InitializeInput struct {
    Now            int64
    SeedAttributes *common.Ticket // 单个 seed 的只读属性视图
    SeedFacts      fact.Values     // 只读 Object Fact
    TickFacts      fact.Values     // 只读当前轮次 Fact
}

type JoinInput struct {
    Now              int64
    SeedAttributes   *common.Ticket // 单个 seed 的只读属性视图
    SeedFacts        fact.Values     // 只读 seed Object Fact
    TickFacts        fact.Values     // 只读当前轮次 Fact
    Candidate        *common.Ticket // 单个当前候选的只读属性视图
    CandidateFacts   fact.Values     // 只读当前候选 Object Fact
    MatchFactsBefore fact.Values     // 候选加入前的完整 MatchFact 快照
}
~~~

上面类型是最小接口的示意；实现应复用现有 Ticket/Fact 模型，不新增第三种属性或 Fact 表示。所有指针、map 和 slice 在回调期间均按只读借用处理，Provider 不得修改或留存它们。

输入权限固定如下：

| 回调 | 允许读取 | 明确禁止 |
| --- | --- | --- |
| Initialize | seed 属性、seed Fact、当前轮次 Tick Fact | 任意候选、Match 成员、Match 对象、MatchID 或 Provider 外部状态回查 |
| OnJoin | seed 属性/Fact、当前轮次 Tick Fact、一个当前候选属性/Fact、候选加入前的完整 MatchFact | 已加入成员的属性/Fact、成员列表/迭代器、Match 对象、LogicalNode/Prefilter 状态、expression AST/Program/Value |

Provider 不能通过任何句柄遍历或访问已有 Match 成员；MatchFactsBefore 是它能读取的唯一 Match 级输入。CanJoin 的输入边界与 OnJoin 相同，但只读且不产生更新。

Provider 计算完整 fact.Values：Initialize 返回 seed 建立后的完整 MatchFact；OnJoin 返回当前候选加入后的完整 MatchFact，包含所有声明的 match-scoped Fact（未变化字段也必须复制到新快照）。LogicalNode 先按 contract 校验类型、scope 和 MaxValues，再复制并一次性提交；失败保留旧快照，成功才同时提交候选和新快照，禁止原地改写已可见旧值。

Provider 错误、context 取消/超时、返回非法或不完整快照均 fail closed：Initialize 失败则当前 seed 不产生 Match；OnJoin 失败则当前候选不加入、当前 MatchFact 不变，并终止当前 seed 尝试返回错误。不得静默跳过、隐式重试、回退旧表达式、半更新或拼接 patch。

不引入 MatchID、CAS、FactVersion、Snapshot、Advance、Complete、patch 或 FactValue；“快照”仅表示返回值的完整性，不增加 Snapshot 对象。

Provider-specific policy 不属于核心 Provider 接口，也不能改变上述输入权限、原子提交和失败语义。

### 4.5 重复事实模型

Provider 的输入、输出和内部提交模型统一使用 fact.Values。common.MatchFacts 仅在 transport/output 边界保留现有镜像时才存在，提交时做一次受控复制；它不是第二条更新路径，也不是 Provider 的替代模型。不得新增第三种模型或按更新字段增加适配器。

后续如移除 common.MatchFacts 镜像，只需调整输出边界，不改变 Provider 接口、输入权限、完整快照和原子提交语义；这不是 MatchFact 更新是否使用 Provider 的决策点。

## 5. 公共面、删除项和文件组织

### 5.1 文件预算

文件预算是软质量目标：减少协议层和微文件，不为数字硬合并不同生命周期代码。

方向性目标约为 expression <=6 个生产文件、prefilter <=10、evaluation <=4、fact <=3；这些是软目标和膨胀观察指标，绝非硬门槛。

禁止一类型一文件；只有独立生命周期、所有权或稳定测试边界才新增文件。

方向性布局可为 expression compiler/program/json/schema；prefilter compiler/query/store/json/index；evaluation compile/json/predicates/errors；fact fact/frame。

实际布局以所有权和依赖为准。

### 5.2 expression 删除清单

最终删除 Bitmap 结果模型及 BitmapLeafInstruction。

删除各 typed LeafCompiler、Bitmap Kind、BitmapState、BitmapProperties、BitmapInstructions、Bitmap lattice 和 Roaring 语义。

删除 DomainDescriptor、DomainField、DomainLeaf、LeafHandle、DomainLeafCompilers 及 callback。

删除 CompileProfile 的 DomainLeaf 字段。

删除 Program 的公共 Instructions、RootInstruction、InstructionID 和其他 IR 视图。

JSON-only 目标下，Arena、Node、NodeRef、Root、通用 Compiler 不再是跨包公共 authoring API，应私有化或删除。

P1/P2 Bitmap 窄桥只能在内部文件暂存，P3 结束不得残留桥接 re-export。

### 5.3 prefilter 删除清单

Prefilter JSON loader 是唯一目标入口；typed Builder 没有明确生产消费者时直接删除，内部 DTO 不公开。

Prefilter 只公开稳定查询、索引、执行和错误边界，动态 Program 作为 opaque 句柄保存。

Bitmap 逻辑编码和 Prefilter fingerprint 由 Prefilter 维护；P2 不定义 Prefilter canonical 公共概念。

### 5.4 evaluation 删除清单

删除 MatchFactsConfig、Initialize/OnJoin/Update 表达式入口、Phase、AllowedPhases、DomainRegistry/domain.go、candidate scorer registry、scorer registry、workflow/provider registry；保留唯一的 MatchFactProvider 边界，不保留 Provider registry。

删除 Advance、Complete 更新入口、Program map、Root 数组、可配置 phase 和多阶段 ValidatedContext。

保留 CanJoin/CanComplete 的 Bool 类型检查和只读上下文校验；删除公开 typed Config、Arena、Root builder；CompileJSON 只返回薄 Predicates。

### 5.5 fact 和编排边界

fact.Values 是 Provider 快照的唯一目标模型；每个声明了 Match Fact 的 LogicalNode 必须绑定 MatchFactProvider。Provider 实现可位于 fact 或应用编排边界，但不得迁入 expression/evaluation，也不得由旧 Evaluation 更新路径替代或旁路。

LogicalNode 持有当前快照并控制替换时机；Provider 成功后由 LogicalNode 原子提交候选和快照，Evaluation 只收到所需只读视图。

## 6. 配置、版本和身份

### 6.1 JSON-only

公开生产入口为 JSON-only；Evaluation 和 Prefilter 目标 loader 只解析 ADR 冻结的最新 JSON。

typed Go 路径不得表达旧结构；内部 DTO 可以存在，但不能成为公共 authoring API。

解析失败必须 fail closed，不得尝试旧 parser、旧 builder、旧 executor 或自动升级。

### 6.2 负向版本矩阵

目标 Prefilter loader 必须拒绝当前 prefilter/v2 并只接受 ADR 的 prefilter/v3；目标
Evaluation loader 必须拒绝当前 evaluation/v2 并只接受 ADR 的 evaluation/v3。

Contract v2 仅在 ADR 确认 shape/语义不变时接受；若改变则拒绝 Contract v2，不能笼统地将所有 current v2 同样处理。

缺失 schemaVersion、null、空值、非法类型和未知版本均 fail closed。

### 6.3 正向版本矩阵

每个目标 Prefilter 合法配置都必须通过 loader 并进入 compile。

每个目标 Evaluation 合法配置都必须通过 loader、compile，并让 CanJoin/CanComplete 进入 evaluate。

正向测试必须证明实际进入 compile/evaluate，不能只证明“不被拒绝”。

### 6.4 身份和发布

expression canonical 只描述标量 Program 规范化；prefilter fingerprint 只描述 Prefilter 逻辑/物理计划、sidecar 和运行约束。

当前仓库没有核心 release manifest 类型或消费者；Provider policy 不进入核心契约、canonical 或 fingerprint。

部署系统若确有需要，可使用可选 release bundle 清单；不作为包级 DoD，也不要求重构 Agent 新增 manifest 类型。

## 7. 五阶段迁移计划

### P0：ADR、目标 loader 和生产方盘点

严格顺序：

1. ADR 冻结 shape、事件顺序、Provider 强制边界、输入权限、完整快照/原子提交语义、错误码和身份边界。
2. 实现目标 JSON loader 和包内私有/internal DTO。
3. 盘点所有生产方、配置、typed Go 构造方、示例和部署入口。
4. 离线迁移所有生产方；逐项处理，不声称存在通用自动迁移。
5. 完成全量 loader、compile、evaluate、golden 和依赖验证。
6. 只有全量验证通过后才允许 runtime 切换目标 loader。

P0 不通过删除旧实现制造完成假象；旧实现只能做离线迁移工具或对照 oracle，切换前不得双读双写。

### P1：收缩 Evaluation 并接 Provider

严格顺序：

1. 先实现唯一的 MatchFactProvider.Initialize/OnJoin 边界及其输入校验、完整快照和原子提交语义。
2. 逐项登记每个现有 Initialize/OnJoin 规则，并为每一项提供且只提供一个对应 Provider 实现；不允许遗留无 Provider 的 MatchFact 更新规则。
3. 为每项 Provider 写明允许读取的 seed/Tick/candidate/加入前 MatchFact、完整输出、校验和失败行为，并证明不能访问已有 Match 成员。
4. 不声称旧 Evaluation 规则可以通用自动迁移；迁移必须由规则拥有者逐项重写为 Provider。
5. 用固定流程 trace 和 golden 与旧实现对照，确认 Provider 成功前不提交候选或事实。
6. 对照通过后切 LogicalNode 使用 Provider，并让 Provider 失败 fail closed。
7. 最后删除旧 Evaluation 更新路径、mergeMatchFacts 和所有更新表达式入口。

DomainRegistry、MatchFactsConfig、更新表达式和 scorer registry 在第 7 步随旧 Evaluation 更新路径最后删除；此前只能断开新路径引用，scorer 改为 LogicalNodeSpec 或上层回调。MatchFactProvider 不得在任何阶段被删除或变为可选，只删除旧 Provider registry（若存在）。

P1 不改变 Prefilter 物理执行；P1/P2 可保留 Bitmap 窄桥，oracle 对照完成前保留三处 Complete 行为。

### P2：Prefilter 独占 Bitmap

把 Bitmap AST、anchor、sidecar、query bind、索引执行和 Roaring 集中到 Prefilter。

loader 解析 Bitmap/query AST；动态表达式交 expression 编译为 opaque Program；Prefilter 保存 Program 和期望 ResultType。

不得公开 InstructionID、NodeRef、DomainLeaf 或 callback；重新生成 Bitmap 逻辑编码和 Prefilter fingerprint，不定义 Prefilter canonical。

P2 不负责 expression 最终清理，P1/P2 桥接只能是内部实现。

### P3：expression 删除 Bitmap/DomainLeaf

删除 BitmapLeafInstruction、typed LeafCompiler、DomainDescriptor、DomainField、DomainLeaf 全套结构及 CompileProfile DomainLeaf 字段。

删除 Program 公共 Instructions/RootInstruction/InstructionID IR 视图，私有化或删除 Arena、Node、NodeRef、Root、通用 Compiler authoring API。

删除 P1/P2 Bitmap 窄桥和 re-export；合并无状态小 helper，但不跨生命周期硬合并。

ParseOptions alias、optional Arena、legacy NodeID 保持已清理，不得回退。

### P4：原子发布和旧入口清理

离线更新所有 JSON、示例、golden、README 和部署输入；目标配置与二进制成组原子发布。

切换前完成完整回滚演练；旧 loader、旧入口、旧派生产物不得进入线上 compile/evaluate。

核心包不新增 release manifest；部署需要时另行维护可选 release bundle 清单。

P4 结束不保留双读、双写、兼容开关或运行时升级分支。

## 8. 验证和验收

### 8.1 固定流程 trace

trace 必须证明 Provider.Initialize、seed-only Complete、Prefilter、scorer、CanJoin 的顺序，以及 CanJoin 成功后的 OnJoin、原子提交和 CanComplete 顺序。

CanJoin=false 不调用 OnJoin；CanJoin=true 时 OnJoin 只能接收一个当前候选和加入前 MatchFacts；每次成功 OnJoin 后才同时提交候选与完整快照，再调用 CanComplete。循环末尾重复 Complete 在 oracle 阶段保留并可观测。

CanComplete 不能触发 Provider 更新，调用顺序不能被配置 phase 改变。

### 8.2 Provider 原子性

覆盖 Initialize/OnJoin 成功、拒绝、非法值、不完整快照、超限、超时和内部错误。

失败后逐字段确认旧 fact.Values 完整不变，当前候选未加入且当前 seed 尝试 fail closed；成功后确认新 fact.Values 是包含全部 match-scoped Fact 的完整集合，并且候选与快照一起提交。

确认没有半更新、原地修改、隐式 patch、静默跳过或隐式重试；确认 Provider 只能读取单个 seed、当前 Tick、单个当前候选和加入前 MatchFacts，不能接收或访问已有 Match 成员，也不接收 expression AST、Program 或 transaction。

### 8.3 配置版本

覆盖目标 Prefilter/Evaluation 合法配置正向进入 compile/evaluate。

覆盖当前 prefilter/v2、evaluation/v2 被目标 loader 拒绝，以及 Contract v2 按 ADR 接受或拒绝。

覆盖缺失版本、未知版本、null、空值、非法类型和旧 typed Config/Arena/Root builder 公共入口消失。

### 8.4 依赖、golden、fuzz、benchmark

扩展 scripts/check-expression-deps.ps1，确认 expression 不依赖 prefilter/evaluation/Roaring，evaluation 不依赖 Prefilter store/Bitmap/query，MatchFactProvider 不依赖 expression AST/Program/Value，也不存在 Evaluation/LogicalNode 直接更新 MatchFact 的旁路。

确认没有旧 loader、双读、双写或 fallback；Golden 覆盖 scalar result、expression canonical、Bitmap 逻辑编码、Prefilter fingerprint、流程 trace、Provider 请求和错误分支。

Fuzz 覆盖标量语法、limits、Bitmap 组合、固定流程、Provider 输入权限、完整快照/原子提交、Provider 失败和版本字段；benchmark 分离 scalar、Prefilter bind/Bitmap、CanJoin/CanComplete、固定流程和 Provider。

不要把尚不存在的 cache 命中率写成验收指标。

### 8.5 发布

目标二进制和目标配置成组发布，回滚恢复上一组完整发布物，不能只回滚单个 JSON 或 fingerprint。

发布 bundle 若存在，由部署系统单独验证，不是四个核心包的契约。

## 9. 完成清单

- [x] [P0 ADR](expression-engine-v3-adr.md) 已冻结 JSON shape、两个谓词、事件顺序、Provider 强制边界、输入权限、完整快照/原子提交语义、错误边界和身份。
- [ ] 目标 JSON loader/internal DTO 已实现，生产方已盘点并离线迁移。
- [ ] 全量验证通过后才切换 runtime loader，无双读双写。
- [ ] Evaluation 只有 JSON-only CompileJSON 和薄 Predicates，无 workflow/state machine。
- [ ] CanJoin/CanComplete 只读约定输入，scorer 由 LogicalNodeSpec/上层回调注入。
- [ ] 每个现有 Initialize/OnJoin 规则都有且只有一个 Provider 实现和 golden 对照；不存在表达式、LogicalNode 或 merge fallback 更新 MatchFact。
- [ ] Provider 只能读取 seed 属性/Fact、Tick Fact、单个当前候选属性/Fact 和加入前 MatchFact，不能访问或遍历已有 Match 成员。
- [ ] Provider 成功返回包含全部 match-scoped Fact 的完整快照，并与候选一起原子提交；失败保留旧 fact.Values、候选不加入并 fail closed，未引入 MatchID/CAS/version/Snapshot/Advance/Complete/patch/FactValue。
- [ ] Prefilter 唯一入口为 JSON loader，动态 operand 只保存 opaque Program 和期望 ResultType。
- [ ] Bitmap 最终只在 Prefilter；不公开 InstructionID/NodeRef/DomainLeaf/CompileProfile。
- [ ] expression 已删除 BitmapLeafInstruction、typed LeafCompiler、DomainDescriptor/DomainField、DomainLeaf、CompileProfile DomainLeaf 字段和公共 IR 视图。
- [ ] Arena/Node/NodeRef/Root/通用 Compiler 不再是跨包 authoring API，ParseOptions alias/optional Arena/legacy NodeID 未回退。
- [ ] common.MatchFacts 未新增第三模型，统一或后续 ADR 原因已记录。
- [ ] 当前 Prefilter/Evaluation v2 按目标规则拒绝，Contract v2 按 ADR 处理，目标合法配置正向进入 compile/evaluate。
- [ ] 核心仅维护 expression canonical 与 Prefilter fingerprint；release bundle 若存在只是可选外部记录，Provider-specific policy 未改变 Provider 强制边界或进入核心契约。
- [ ] 文件预算作为软质量目标执行，未为数字硬合并不同生命周期代码。
- [ ] trace、Provider 原子性、版本矩阵、依赖扫描、golden、fuzz、benchmark 和回滚演练均有记录。

清单全部满足后才能标记拆分完成；common.MatchFacts 仅作为输出边界镜像时，后续 ADR 不得改变 Provider 强制边界或阻塞核心拆分。
