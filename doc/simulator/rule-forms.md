# 规则与场景表单

Rules（规则页）以表单编辑当前 `match-rule/v1` 和模拟器部署配置。JSON 文件仍可导入、
导出；页面不再提供 JSON 文本编辑入口。所有配置在“保存场景”时合并，经过规则本地
校验、Go 规则编译和场景启动校验后重建运行态。重建会清空等待队列与保留的历史。

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
| 全部 Facts / Provider | Tick/Object/Match 独立启用/清除 Descriptor；id、version、facts 的全部元数据；可添加未被 Contract 使用的合法能力 |
| 全部 Facts / Tick 值 | 按 Contract 类型逐字段提供/移除值，列表逐项增加、编辑、删除；未声明的现有值显式保留并提示 |
| 场景与部署 / 场景 | matchHistoryLimit；实际 Scenario 契约没有根级 seed 参数 |
| 场景与部署 / 物理节点 | id、endpoint、enabled、selector；支持添加节点、删除未被引用的节点；重命名同步部署引用 |
| 场景与部署 / 规则映射 | logicalNode.rule.namespace/ruleId、placementId、physicalNodeId、weight、enabled；同步 RuleJSON.ruleKey |

`schemaVersion` 和表达式 `resultType` 由版本与输入类型决定，不作为自由文本修改。
调度选择器支持轮询、最大队列、最长等待及平滑加权轮询。它与逻辑节点内部的
SeedOrder（种子顺序）是两个不同层次。

## 使用与校验

先声明 Attribute/Fact/Index，再在表达式中选择兼容的数据来源、字段或索引。
表达式表单从版本化 Schema 构建操作列表，覆盖 46 个实际操作；图编辑器的
NodeInspector（节点检查器）复用同一表单。集合逐项编辑可以保留逗号、空字符串和
前后空白；阶梯逐项输入起点与输出值，提供顺序与增删操作。

切换操作或评分/种子算法会重建该操作的参数，避免旧算法字段混入新配置；其他表单
修改保留无关字段。编辑中的非法数值会就地报错并阻止保存，不会按 0 提交。修改草稿
后清除旧 Go 校验结果，避免把旧配置的通过状态用于新配置。最终语义仍由 Go 编译器
与实际 Provider 握手验证。

`candidateLimitPerSeed` 可以大于评分上限，核心实际保留数同时受两者约束。
`weight` 必须大于 0，留空默认 1；`missingScore` 属于最终分数空间，不再乘权重。
`containsProbeThreshold` 留空或 0 使用 4096。场景草稿与当前规则草稿通过统一保存
合并，即使中途切换标签也不会遗漏部署修改。

## 边界

- Descriptor（握手声明）只是 Provider 能力元数据，不会生成任意动态 Provider。
  动态函数仍由 Go 宿主接入；Object 值在 Tickets 页面配置，Match 值由 Provider 产生。
- 内置 Tick `waitingCount` / `queueDepth` / `waiting-count` 会以实际等待数量覆盖静态值，
  表单明确提示此行为。内置 Fact 的完整含义见 [Fact 数据来源](fact-sources.md)。
- Web 数字模型沿用 JavaScript Number。新表单的整数输入只接受安全整数，超过
  `±9007199254740991` 会报错，不会通过输入框自动舍入。完整 int64/uint64 数据范围
  仍属于直接 Go/API 接入能力；本次没有改变线协议或引入字符串整数表示。
- 场景表单编辑已有规则部署；新增完整规则集合仍可通过场景文件导入。规则表单不会
  生成未经用户配置的业务默认条件。

## 实现与验证

主要入口为 `apps/web/src/pages/Rules.tsx`。`ExpressionEditor` 与
`lib/expressionForm.ts` 负责完整表达式输入；`RuleSettingsEditor`、`RuleFactsEditor`、
`ScenarioSettingsEditor` 分别负责算法预算、Facts 分层、场景部署。`ruleStore.setEnvelope`
同步草稿与图结构，`scenarioSettingsPayload` 合并部署草稿与规则配置。

前端回归 `ruleForms.test.ts` 覆盖全部操作渲染、参数保存、Provider 额外字段、列表
内容保留、部署身份修改与非法数值。后端 `rule_form_runtime_test.go` 通过完整
Scenario → CompileRuleJSON → LogicalNode 路径验证实际种子顺序、固定随机种子复现、
评分方向对最终成员的影响。匹配核心实现与线协议保持不变。

2026-09-06 真实 Web + Go API 检查：在表单设置 randomSeed=57、评分 weight=2.5、
matchHistoryLimit=321、路由 weight=3，联合保存后正确回读；非法负权重就地报错。
在 512 像素视口检查场景表单，字段与标签采用单列布局。

验证命令：`npm --prefix apps/web run build`、`npm --prefix apps/web test`、
`go test ./internal/simulator -count=1`、`go vet ./internal/simulator`。
