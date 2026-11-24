# 匹配规则库表单

Rules（规则配置页）管理独立的 `match-rule/v1` 规则库。可在规则尚未部署时完成设计、校验、
保存和导入导出。默认打开种子、评分与预算表单，规则图位于最后的高级标签，节点检查器
只在规则图中显示。物理节点装载规则的操作见[运行时物理节点](interface-guide.md#运行时物理节点)。

## 配置覆盖

| 页面分组 | 完整可配置字段 |
| --- | --- |
| 字段契约 / Attributes | name、type、集合 maxValues；关联索引随名称同步，保留自定义索引上限 |
| 字段契约 / Facts | name、type、scope、集合 maxValues、description |
| 字段契约 / Indexes | 关联 Attribute、类型兼容的 multi_value/int64_range、keyType、maxDocumentValues、maxQueryValues；索引类型与键类型由属性类型决定 |
| 字段契约 / 高级 Limits | maxBytes、maxDepth、maxChildren、maxStringBytes、maxIndexes、maxAttributes、maxFacts、maxValues、maxDocumentValues、maxQueryValues；可清除恢复默认 |
| 预筛选 | 全部 8 个 Bitmap 操作的类型化输入、条件分支、关联索引；runtime.containsProbeThreshold 可省略或设置 |
| 加入与成局 | canJoin、canComplete 的全部 38 个 Scalar 操作；常量、来源/字段、比较、算术、clamp、steps、集合等 |
| 种子顺序 | arrival、oldest、int64_priority 的 field/direction、random 的 randomSeed |
| 候选评分 | constant.value；created_at.direction/weight；int64_field.field/direction/weight/missingScore |
| 运行预算 | maxPlayers、candidateScoringLimitPerSeed、candidateLimitPerSeed、attemptLimitPerProduceMatch、attemptLimitPerMatchRound |
| 规则身份 | 部署引用由正 `uint64` 十进制 `ruleId` 决定，`namespace` 是可选文本别名 |
| 规则生命周期 | 新建安全空规则、复制规则至新身份、删除规则、导入和导出规则文件 |

`schemaVersion` 和表达式 `resultType` 由版本与输入类型决定，不作为自由文本修改。
物理节点的调度选择器在拓扑中配置。每条匹配规则的 SeedOrder（种子顺序）由上表的种子表单编辑。

## 新建、复制与删除

“新建规则”创建字段/索引/Facts 为空、`canJoin=false`、`canComplete=false` 的完整规则，
且可以立即编辑。编号默认选择未占用的 ID。规则库可以保存零条规则，也可以在场景没有物理节点时使用。

复制规则会保留源规则的字段、表达式、算法和预算，可给副本指定独立身份。删除规则会在保存后
从规则库移除条目。已经部署的逻辑节点仍使用装载时的规则内容。

切换规则时保留当前规则草稿，保存时校验并提交整个规则库。运行中的逻辑节点使用各自的装载
快照，保存规则库不会重启运行时。后续部署可从库中选规则，已有部署可在拓扑显式应用库中的规则版本。

点击“导出规则”将当前草稿保存为 `match-rule/v1`，或点击“导出规则库”将全部草稿保存为
`match-rule-library/v1`。在保存对话框中选择目录并确认文件名。见[保存方式与 Web 兼容性](interface-guide.md#场景-json-导入与导出)。
导入旧 `simulator-scenario/v1` 文件会提取规则文档到规则库草稿，只有从运行总览导入场景才会部署节点。

## 使用与校验

先声明 Attribute/Fact/Index，再在表达式中选择兼容的数据来源、字段或索引。
「内建属性」列出当前服务的只读字段。`createTime` 来自票据创建时间，可直接在
表达式中引用，也可添加 `int64_range` 索引。导出的 Contract 记录用户声明和索引配置，
内建属性由运行时提供。见 [Contract 的内建属性](../match-system/reference/logical-node-contract.md#内建属性)。
表达式表单从版本化 Schema 构建操作列表。图编辑器的
NodeInspector（节点检查器）复用同一表单。集合逐项编辑可以保留逗号、空字符串和
前后空白；阶梯逐项输入起点与输出值，提供顺序与增删操作。

在预筛选表单或规则图中选择「最近整数查询」，填写 `int64_range` 索引、目标表达式、
查找方向和不同值数量。用 `and` 把查找前的普通条件放在同一层。需要对最近值结果继续
过滤时，用内层 `and` 包住查询。具体行为见[最近值查询](../match-system/reference/prefilter.md#查找最近的-int64-值)。

切换操作或评分/种子算法会重建该操作的参数，避免旧算法字段混入新配置；其他表单
修改保留无关字段。编辑中的非法数值会就地报错并阻止保存，不会按 0 提交。修改草稿
后清除旧 Go 校验结果，避免把旧配置的通过状态用于新配置。规则语义由 Go 编译器校验，
Provider 自描述能力在逻辑节点装载时校验。

`candidateLimitPerSeed` 可以大于评分上限，核心实际保留数同时受两者约束。
`weight` 必须大于 0，留空默认 1；`missingScore` 属于最终分数空间，不再乘权重。
`containsProbeThreshold` 留空或 0 使用 4096。

## 边界

- Descriptor（启动自描述）只是 Provider 能力元数据，不会生成任意动态 Provider。
  Provider 实现可以由 Go 宿主接入，也可以由场景设置中的 Python Provider 绑定接入；
  Object 值在 Tickets 页面配置，Match 值由 Provider 产生。Go 通过进程内 `Describe()`
  返回能力，Python 通过 JSON-lines `describe()` 返回能力。两者共享同一 Contract 语义：
  Go 可信 Provider 由实现与契约测试保证业务快照，Python 适配边界对每次业务响应执行
  Validator。Python 绑定的服务端路径、生命周期与协议见
  [Python Provider 接入](python-provider.md)。
- 内置 Tick `waitingCount` 会以实际等待数量覆盖静态值；`queueDepth`、`waiting-count` 等
  历史名称不会由当前内建 Provider 自动合成。内置 Fact 的完整含义见
  [Fact 数据来源](fact-sources.md)。
- 规则身份使用十进制文本，支持完整 `uint64` 范围。表达式和预算表单的 JavaScript
  Number 整数输入继续限制为安全整数，超过 `±9007199254740991` 会报错。完整
  int64/uint64 数据范围可通过 Go/API 接入。Ticket 属性生成还支持十进制文本区间，见
  [属性生成配置](attribute-generation.md)。
- 服务内保存的规则库可跨场景切换使用。通过导出规则文件可在服务重启或不同实例之间迁移。

## 实现

主要入口为 `apps/web/src/pages/Rules.tsx`。`ExpressionEditor` 与
`lib/expressionForm.ts` 负责完整表达式输入，`RuleSettingsEditor` 负责算法预算。
`ruleStore.setEnvelope` 同步当前编辑器与图结构。拓扑部署入口在
`apps/web/src/components/PhysicalNodeManager.tsx`。

规则表单、库管理及节点部署的验证入口见[功能验证矩阵](../design-decisions/testing/functional-validation.md)。

## 表达式逐级编辑

表达式面板只显示当前节点的操作、直接参数和子输入摘要，不再递归展开整棵子树。
点击“跳转到…”进入对应的下一级表达式；普通表单通过“返回上一级”逐级返回，修改即时
保留在当前规则草稿中。数组子输入逐项提供跳转、上移和删除；Scalar envelope 元数据及
未编辑的兄弟输入保持不变。

高级规则图的节点面板优先跳转到该输入连线的来源节点，并将画布定位到该节点；尚无
连线时进入本地子表达式编辑。跳转本身不修改规则、不新增连线。
