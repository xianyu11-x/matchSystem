# 匹配系统参数明细

本页是当前 `match-rule/v1` 的参数入口。JSON 形状以
[`api/schema/match-rule/v1.schema.json`](../../api/schema/match-rule/v1.schema.json) 为准，
跨字段、类型、scope 和索引语义最终由
[`CompileRuleJSON`](../../internal/matchsystem/rule_config.go) 校验。

## 顶层结构

所有顶层字段都必填，未知字段、重复键、`null`、尾随 JSON 和版本不匹配都会被拒绝。

| 字段 | 类型 | 作用 |
| --- | --- | --- |
| `schemaVersion` | string | 固定为 `match-rule/v1` |
| `ruleKey` | object | 规则的稳定身份 |
| `contract` | object | Attribute、Fact、Index 与限制的唯一声明 |
| `prefilter` | object | `prefilter/v3` Bitmap 候选过滤计划 |
| `evaluation` | object | `evaluation/v3` 的 `canJoin`、`canComplete` 谓词 |
| `scoring` | object | 候选评分算法及参数 |
| `seedSelection` | object | 每轮 Seed 顺序策略及参数 |
| `runtime` | object | 候选、组大小和尝试预算 |

完整文件的规范化 SHA-256 是规则 fingerprint；对象字段顺序不同不会改变 fingerprint，
任何实际配置值变化都会改变它。

## `ruleKey`

| 字段 | 必填 | 约束 |
| --- | --- | --- |
| `namespace` | 否 | string；空值表示默认命名空间 |
| `ruleId` | 是 | 大于等于 1 的整数，且能表示为 `int32` |

加载 LogicalNode 时，`ruleKey` 必须与 `LogicalNodeKey.Rule` 完全一致。
`placementId` 是部署身份，不属于 RuleJSON。

## `scoring`

候选按最终 score 从高到低保留；相同 score 使用稳定的 DocID 次序打破平局。

| `type` | `params` | 语义 |
| --- | --- | --- |
| `constant` | `value`：必填有限数 | 所有候选返回同一分数 |
| `created_at` | `direction`：必填；`weight`：可选 | 按 Ticket `CreatedAt` 评分 |
| `int64_field` | `field`、`direction`：必填；`weight`、`missingScore`：可选 | 按已声明的 `int64` Attribute 评分 |

公共参数约束：

- `direction` 只能是 `ascending` 或 `descending`；`ascending` 让较小原始值获得更高分，
  `descending` 让较大原始值获得更高分。
- `weight` 默认为 `1`，必须是有限数且满足 `0 < weight <= MaxFloat64 / MaxInt64`。
- `missingScore` 是最终分数空间中的值，不再乘方向或权重；省略时为最差有限分数。
- `int64_field.field` 必须是 Contract 中已声明的 `int64` Attribute。

## `seedSelection`

| `type` | `params` | 语义 |
| --- | --- | --- |
| `arrival` | 空对象 | 按进入 LogicalNode 的顺序取 Seed |
| `oldest` | 空对象 | 优先等待时间最长的 Ticket |
| `int64_priority` | `field`、`direction` | 按已声明的 `int64` Attribute 排序 |
| `random` | `randomSeed` | 使用固定 `int64` 种子，可确定性重放 |

`int64_priority.direction` 只能是 `ascending` 或 `descending`；字段必须存在且类型为
`int64`。一个 MatchRound 内已返回的有效 Seed 不会重复，失败 Seed 到下一轮才重新可用。

## `runtime`

| 字段 | 必填 | 默认值 | 约束与含义 |
| --- | --- | ---: | --- |
| `candidateScoringLimitPerSeed` | 否 | 500 | 每个 Seed 最多评分的候选数，正整数 |
| `candidateLimitPerSeed` | 否 | 50 | 评分后保留的 Top-L 候选数，正整数 |
| `maxPlayers` | 是 | — | 一个 Match 最多容纳的 Ticket 数，正整数 |
| `attemptLimitPerProduceMatch` | 是 | — | 单次 `ProduceMatch` 最多消费的有效 Seed 数，正整数 |
| `attemptLimitPerMatchRound` | 是 | — | 同一 LogicalNode 在一轮内累计最多消费的有效 Seed 数，正整数 |

`attemptLimitPerProduceMatch` 不得大于 `attemptLimitPerMatchRound`。后者跨同一轮中的多次
`ProduceMatch` 累计，并在 `BeginMatchRound` 重置；跳过已删除或失效的条目不消费预算。

`candidateScoringLimitPerSeed` 控制评分工作量，`candidateLimitPerSeed` 控制进入求值阶段的
Top-L 规模。两者没有强制大小关系：`candidateLimitPerSeed < candidateScoringLimitPerSeed`
时会保留有界 Top-L；反向配置也不会突破实际已评分候选数。

## `contract`

Contract 使用 `logical-node-contract/v3`，字段详见
[LogicalNode Contract](reference/logical-node-contract.md)。常用声明如下：

| 区域 | 关键字段 | 约束摘要 |
| --- | --- | --- |
| `attributes[]` | `name`、`type`、`maxValues` | `type` 为 `strings`、`uint64s` 或 `int64`；集合类型必须声明正数 `maxValues` |
| `facts[]` | `name`、`type`、`scope`、`maxValues`、`description` | scope 为 `tick`、`object`、`match`；`description` 仅用于文档元数据 |
| `indexes[]` | `type`、`name`、键类型与容量 | 索引名称同时引用同名 Attribute；类型和 Attribute 必须兼容 |
| `limits` | 一组资源上限 | 省略或填 0 使用默认值，负值无效 |

默认 Contract 限制：

| 参数 | 默认值 | 参数 | 默认值 |
| --- | ---: | --- | ---: |
| `maxBytes` | 1 MiB | `maxDepth` | 64 |
| `maxChildren` | 128 | `maxStringBytes` | 1024 |
| `maxIndexes` | 128 | `maxAttributes` | 256 |
| `maxFacts` | 256 | `maxValues` | 10000 |
| `maxDocumentValues` | 256 | `maxQueryValues` | 256 |

## `prefilter`、`evaluation` 与表达式

- `prefilter` 固定使用 `prefilter/v3`，顶层结果是私有 Bitmap expression；详见
  [Prefilter](reference/prefilter.md)。
- `evaluation` 固定使用 `evaluation/v3`，必须同时提供返回 Bool 的 `canJoin` 和
  `canComplete`；详见 [Evaluation](reference/evaluation.md)。
- 嵌套标量表达式固定使用 `expression-scalar/v3`；节点、字段、source 权限和资源限制见
  [表达式 JSON 完整指南](reference/expression-json-usage.md)。

## 聚合 JSON 资源限制

RuleJSON 外层先经过统一结构校验：最大 4 MiB、深度 96、单对象 256 个字段、单数组
10000 项、总值数 50000、单字符串 1024 字节。随后每个嵌套 section 还会执行自身限制。

## 校验入口

```powershell
# 通过模拟器 API 校验一条规则
Invoke-RestMethod -Method Post `
  -Uri http://127.0.0.1:8080/api/v1/rules/validate `
  -ContentType application/json `
  -Body '{"rule": <match-rule/v1 对象>}'
```

前端 JSON Schema 校验用于快速反馈，不能替代服务端编译。错误通常包含 phase、JSONPath
和 code，排查顺序是：外层结构 → Contract → Prefilter/Evaluation → Scoring/Seed →
Provider 握手。
