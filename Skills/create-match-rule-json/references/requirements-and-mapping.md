# MatchSystem RuleJSON 需求与映射

本参考用于把自然语言需求转换成当前项目的 `match-rule/v1`。每次使用时仍要重新读取当前 schema；本文件解释 schema 之间的语义关系，不替代权威定义。

## 权威文件

- `api/schema/match-rule/v1.schema.json`：完整规则入口。
- `api/schema/logical-node-contract/v3.schema.json`：Attribute、Fact、Index 与 limits。
- `api/schema/prefilter/v3.schema.json`：Bitmap Prefilter。
- `api/schema/evaluation/v3.schema.json`：`canJoin` 与 `canComplete`。
- `api/schema/expression-scalar/v3.schema.json`：表达式节点、类型和 source。
- `doc/logical-node-contract.md`、`doc/expression-scalar.md`、`doc/prefilter.md`、`doc/evaluation.md`：schema 无法完整表达的编译期语义。
- `scripts/validate_rule.py`：Skill 自带的轻量格式校验器；它只依赖 Python 标准库和本 Skill 的格式约束。

忽略 `doc/archive/` 中的旧格式。

## 保留的空白默认规则

`assets/default-rule.json` 是 Skill 唯一的默认 RuleJSON，只能在用户明确要求“空白配置使用默认规则”或同等含义时采用。普通信息缺失不能触发它。

默认值为：

| section | 默认值与语义 |
| --- | --- |
| 输出路径 | 未指定时为 `rules/default-rule.json` |
| `ruleKey` | `namespace: "default"`, `ruleId: 1` |
| Contract | Attribute、Fact、Index 均为空，不需要 Provider |
| Prefilter | `none`，候选集恒为空 |
| Evaluation | `canJoin: false`, `canComplete: false` |
| Scoring | `constant`, value `0` |
| Seed Selection | `arrival` |
| Runtime | `candidateScoringLimitPerSeed: 500`, `candidateLimitPerSeed: 50`, `maxPlayers: 8`, `attemptLimitPerProduceMatch: 500`, `attemptLimitPerMatchRound: 500` |

这是一份可编译、可加载但不会创建 Match 的安全空白基线，不是 match-all（匹配全部）规则。用户明确给出的字段覆盖对应默认值；不得顺带修改其他默认值。覆盖后的 JSON 仍需完整验证。

## 必须确认的需求

只询问用户尚未明确给出的项。可以分轮提问，但在所有阻塞项确认前不得保存最终 JSON。已明确激活空白默认规则时，默认表中的字段视为已确认，不再追问。

### 1. 身份与输出

- 输出文件的路径和名称是什么？
- `ruleId` 是哪个正整数？
- `namespace` 的明确值是什么，还是明确使用空 namespace？
- 宿主加载时使用的 `LogicalNodeKey.Rule` 是否与上述值一致？

`PlacementID` 不属于 RuleJSON，不要询问后写入规则文件。

### 2. Ticket 数据与 Fact

对每个业务字段确认：

- JSON 名称；
- 是 Ticket Attribute 还是 Fact；
- 类型为 `strings`、`uint64s` 还是 `int64`；
- 多值类型的 `maxValues`；
- Fact scope 为 `tick`、`object` 还是 `match`；
- 对应 Provider 是否存在，具体会提供哪些名称和值。

Attribute 与 Fact 名称在一个 Contract 内全局唯一。不要把自然语言中的“单个字符串”擅自解释为 `strings` 且 `maxValues: 1`；先确认其项目数据表示。

Fact 生命周期与常用 source：

| Fact scope | 运行时来源 | 常用表达式 source |
| --- | --- | --- |
| `tick` | 每次 `ProduceMatch` 的 `FactProvider` | `tick_facts` |
| `object` | 每个 Ticket 的 `ObjectFactProvider` | `seed_facts`、`candidate_facts` |
| `match` | `MatchFactProvider.Initialize/OnJoin` | `match_facts` |

Ticket Attribute 使用 `seed_attributes` 或 `candidate_attributes`。实际 profile（表达式上下文许可）更严格，必须以当前文档和编译器为准。

如果“组满 N 人”等条件依赖成员数，必须确认一个 `scope: match` 的 `int64` Fact 名称以及 Match Fact Provider 的初始化和递增行为；系统没有可凭空引用的隐式组人数变量。

### 3. Prefilter（索引初筛）

确认候选集如何由索引建立：

- `lookup_string` 对应 `multi_value` + `keyType: string` + `strings` Attribute；
- `lookup_uint64` 对应 `multi_value` + `keyType: uint64` + `uint64s` Attribute；
- `lookup_range` 对应 `int64_range` + `int64` Attribute；
- 多条件组合明确使用 `and`、`or`、`exclude` 或 `if`；
- 每个多值索引必须确认 `maxDocumentValues` 和 `maxQueryValues`；
- 如需自定义 `runtime.containsProbeThreshold`，确认数值；否则必须让用户明确接受项目默认值。

Prefilter 只允许读取 seed Attribute、seed Object Fact 和 Tick Fact。它不能读取 candidate 或 Match Fact。除明确要求静态空候选集外，不要使用 `none`；`none` 不是全量候选。纯 `exclude` 也不能建立正向候选 scope。

### 4. Evaluation（精确判定）

分别确认：

- candidate 在什么条件下可以加入临时 Match（`canJoin`）；
- Match 在什么条件下可以完成（`canComplete`）；
- 多条件之间是 AND 还是 OR；
- 边界是 `<`、`<=`、`==`、`!=`、`>=` 还是 `>`；
- 多值集合是相等、包含任一、包含全部、相交还是不相交语义；
- 字段缺失或空集合时的预期行为，以及 Provider 是否保证值存在。

不要把“接近”“相同水平”“合适”等模糊词自行翻译成阈值或操作符，应询问精确关系和数值。

### 5. Scoring（候选评分）

必须选择并确认一个类型：

| type | 必需 params | 需要额外确认 |
| --- | --- | --- |
| `constant` | `value` | 固定分数 |
| `created_at` | `direction` | 可选 `weight` 是否使用默认 |
| `int64_field` | `field`, `direction` | field 必须是已声明 `int64` Attribute；可选 `weight`、`missingScore` 是否使用默认 |

如果自然语言只说“优先”，必须确认优先字段、方向和缺失值策略，不能替用户决定。

### 6. Seed Selection（种子选择）

必须选择并确认一个类型：

- `arrival`：按加入顺序，`params` 为空对象；
- `oldest`：按 `CreatedAt` 最早优先，`params` 为空对象；
- `int64_priority`：确认已声明的 `int64` Attribute `field` 与 `direction`；
- `random`：确认可重放的整数 `randomSeed`。

候选评分与 Seed Selection 是两个独立决策，不能从其中一个推断另一个。

### 7. Runtime

四个值都必须是正整数，并逐项确认：

- `candidateScoringLimitPerSeed`：每个 seed 参与评分的候选人数上限；Prefilter 结果超过上限时按 DocID 升序截断。`candidateLimitPerSeed`：从评分池中保留的 Top-L 候选人数；默认基准分别为 500 和 50；
- `maxPlayers`：单个 Match 的最大玩家数；
- `attemptLimitPerProduceMatch`：一次调用最多消费的有效 seed 数；
- `attemptLimitPerMatchRound`：整轮累计最多消费的有效 seed 数。

`attemptLimitPerProduceMatch <= attemptLimitPerMatchRound`。不要从 `maxPlayers` 或性能直觉推导这些预算。

### 8. 可选 limits 与默认值

`contract.limits`、Prefilter 的 `containsProbeThreshold`、Scoring 的 `weight`/`missingScore` 等字段虽然可选，但省略会选择项目行为。若用户没有明确提供，应询问“接受当前项目默认值，还是指定值”，不要静默省略。

## 可机械生成的内容

在业务语义已确认后，下列内容可直接生成，不需要再询问：

- 固定版本：`match-rule/v1`、`logical-node-contract/v3`、`prefilter/v3`、`evaluation/v3`、`expression-scalar/v3`；
- 由表达式结果类型唯一确定的 `resultType`；
- `arrival`、`oldest` 等 schema 强制要求的空 `params: {}`；
- JSON 缩进、字段顺序与文件末尾换行；
- 由已确认字段类型唯一决定的 ref op，例如 `int64_ref`、`strings_ref`。

## 校验与交付判断

运行：

```powershell
python Skills/create-match-rule-json/scripts/validate_rule.py <rule-json-path>
```

- `valid: true`：说明 JSON 通过本 Skill 的格式检查；不代表后端编译或生产集成已验证。
- `valid: false`：根据 `path`/`code` 修正纯机械错误；任何语义性修正必须回问用户。
- 即使 `valid: true`，使用 Fact 的规则仍需宿主提供符合 Contract 的 Provider。没有验证生产 Provider 时，只能表述为“RuleJSON 格式有效”，不能表述为“生产集成已验证”。
