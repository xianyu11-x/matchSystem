# MatchSystem RuleJSON 需求与映射

本参考用于把自然语言需求转换成当前项目的 `match-rule/v1`。每次使用时仍要重新读取当前 schema；本文件解释 schema 之间的语义关系，不替代权威定义。

## 权威文件

下列 `api/`、`doc/` 和 `examples/` 路径均相对于当前 checkout 的仓库根目录，不相对于已安装 Skill 目录；通过 `git rev-parse --show-toplevel` 可定位 checkout。Skill 随附资源的链接相对于本参考文件。

- `api/schema/match-rule/v1.schema.json`：完整规则入口。
- `api/schema/logical-node-contract/v3.schema.json`：Attribute、Fact、Index 与 limits。
- `api/schema/prefilter/v3.schema.json`：Bitmap Prefilter。
- `api/schema/evaluation/v3.schema.json`：`canJoin` 与 `canComplete`。
- `api/schema/expression-scalar/v3.schema.json`：表达式节点、类型和 source。
- `doc/match-system/reference/logical-node-contract.md`、
  `doc/match-system/reference/expression-scalar.md`、
  `doc/match-system/reference/prefilter.md`、
  `doc/match-system/reference/evaluation.md`：schema 无法完整表达的编译期语义。
- [随附验证器](../scripts/validate_rule.py)：Skill 自带的轻量格式校验器；它只依赖 Python 标准库和本 Skill 的格式约束。

忽略 `doc/design-decisions/archive/` 中的旧格式。

## 保留的空白默认规则

[空白默认规则](../assets/default-rule.json)是 Skill 唯一的默认 RuleJSON，只能在用户明确要求“空白配置使用默认规则”或同等含义时采用。普通信息缺失不能触发它。

默认值为：

| section | 默认值与语义 |
| --- | --- |
| 输出路径 | 未指定时为 `rules/default-rule.json` |
| `ruleKey` | `namespace: "default"`, `ruleId: "1"` |
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
- `ruleId` 是哪个 `uint64` 正整数（最大值为 18446744073709551615）？
- 如需展示，`namespace` 使用哪个文本别名？该字段可省略。
- 宿主加载时使用的 `LogicalNodeKey.Rule.RuleID` 是否与规则编号一致？

`PlacementID` 不属于 RuleJSON，不要询问后写入规则文件。
生成规则时将 `ruleId` 写为无前导零的正十进制字符串，避免 JavaScript 丢失精度。
校验器也接受精确的 JSON 整数。`namespace` 作为可选文本别名原样保存。

### 2. Ticket 数据与 Fact

对每个业务字段确认：

- JSON 名称；
- 是 Ticket Attribute 还是 Fact；
- 类型为 `strings`、`uint64s` 还是 `int64`；
- 多值类型的 `maxValues`；
- Fact scope 为 `tick`、`object` 还是 `match`；
- 对应 Provider 是否存在，具体会提供哪些名称和值。

Attribute 与 Fact 名称在一个 Contract 内全局唯一。不要把自然语言中的“单个字符串”擅自解释为 `strings` 且 `maxValues: 1`；先确认其项目数据表示。

`createTime` 是内建只读 `int64` Attribute，值直接来自 `Ticket.CreatedAt`，无需声明；这是保留名，不得在 `attributes` 或 `facts` 中重复声明，也不得写入 Ticket 属性 map。需要按该值索引时，可在 `indexes` 声明 `{"type":"int64_range","name":"createTime"}`。允许的表达式上下文可用 `seed_attributes` 引用，`canJoin` 也可用 `candidate_attributes`；`int64_field` 评分和 `int64_priority` 种子选择也可引用它。宿主负责填写 `Ticket.CreatedAt` 并约定时间单位；模拟器使用 Unix 毫秒。详见 checkout 的 `doc/match-system/reference/logical-node-contract.md`。

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
- `lookup_range`、`lookup_nearest` 对应 `int64_range` + `int64` Attribute（包括内建 `createTime`）；
- 多条件组合明确使用 `and`、`or`、`exclude` 或 `if`；
- 每个多值索引必须确认 `maxDocumentValues` 和 `maxQueryValues`；
- 如需自定义 `runtime.containsProbeThreshold`，确认数值；否则必须让用户明确接受项目默认值。

Prefilter 只允许读取 seed Attribute、seed Object Fact 和 Tick Fact。它不能读取 candidate 或 Match Fact。除明确要求静态空候选集外，不要使用 `none`；`none` 不是全量候选。纯 `exclude` 也不能建立正向候选 scope。

使用 `lookup_nearest` 时，确认索引、完整 `expression-scalar/v3` 的 `int64` 目标 `target`（包括来源及时间单位等业务含义）、`direction` 和正整数 `limit`（不超过 `uint64` 最大值）。方向为 `left`（小于等于目标，从近到远）、`right`（大于等于目标，从近到远）或 `both`（两侧共享上限、按距离选值，等距优先较小值）。

`limit` 限制的是不同索引值的数量，不是 Ticket 数或玩家数：每个入选值下通过当前条件的 Ticket 都进入候选集，Seed 自身排除，只有 Seed 的值不占名额。后续评分决定尝试顺序，评分预算要按实际候选票数确认。

还需确认最近值是在何种条件过滤之后选取：同一 `and` 的直属 `lookup_nearest` 在该层普通条件之后执行；多个直属 `lookup_nearest` 按声明的相对顺序依次缩小集合。若放进内层含直属最近值的 `and`，该子树先独立从索引全集选值，再与外层候选集合相交；直接位于根节点或 `or`、`if`、`exclude` 分支时同样先从索引全集选值，再与传入集合相交。索引全集仅指当前 LogicalNode 中写入该索引的票据。不同层级会改变结果，不能只按视觉上的条件组合推断；详见 checkout 的 `doc/match-system/reference/prefilter.md`。

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
| `int64_field` | `field`, `direction` | field 必须是已声明 `int64` Attribute 或内建 `createTime`；可选 `weight`、`missingScore` 是否使用默认 |

如果自然语言只说“优先”，必须确认优先字段、方向和缺失值策略，不能替用户决定。

### 6. Seed Selection（种子选择）

必须选择并确认一个类型：

- `arrival`：按加入顺序，`params` 为空对象；
- `oldest`：按 `CreatedAt` 最早优先，`params` 为空对象；
- `int64_priority`：确认已声明的 `int64` Attribute 或内建 `createTime` 的 `field` 与 `direction`；
- `random`：确认可重放的整数 `randomSeed`。

候选评分与 Seed Selection 是两个独立决策，不能从其中一个推断另一个。

### 7. Runtime

五个值都必须是正整数，并逐项确认：

- `candidateScoringLimitPerSeed`：每个 seed 参与评分的候选 Ticket 数上限；Prefilter 结果超过上限时按 DocID 升序截断，默认基准为 500；
- `candidateLimitPerSeed`：从评分池中保留的 Top-L 候选 Ticket 数，默认基准为 50；
- `maxPlayers`：单个 Match 的最大 Ticket 数；
- `attemptLimitPerProduceMatch`：一次调用最多消费的有效 seed 数；
- `attemptLimitPerMatchRound`：整轮累计最多消费的有效 seed 数。

`attemptLimitPerProduceMatch <= attemptLimitPerMatchRound`。一张 Ticket 可以代表多个玩家，以上票数不能直接当作玩家人数。不要从 `maxPlayers` 或性能直觉推导这些预算。

### 8. 八票 FIFO 示例

checkout 中的 `examples/simulator/fifo-8-match-rule.json` 是 `ruleId: "91002"` 的八票示例。它以 `createTime` 建 `int64_range` 索引，向右取最近的 20 个不同创建时间值，使用 `oldest` Seed 和 `created_at` 升序评分；`memberCount` 是 `scope: match` 的 `int64` Fact，达到 8 张票时完成，`maxPlayers` 为 8。模拟器内置 Provider 按票据计数；外部宿主必须提供匹配的 Match Fact Provider，初始化计入 Seed 1 张，并在每次加入时加 1。若一票代表多人，8 票不等于 8 人，玩家人数的逻辑须另行确认。

该示例的评分池上限为 500、Top-L 上限为 50；DocID 截断、相同时间的平分处理及时间评分精度使其不保证所有队列规模下的严格 FIFO。示例是行为参考，不可视为对用户尚未确认的业务语义、Provider 或预算的授权。详见 checkout 的 `doc/simulator/usage-guide.md` 中“试用 FIFO 规则”。

### 9. 可选 limits 与默认值

`contract.limits`、Prefilter 的 `containsProbeThreshold`、Scoring 的 `weight`/`missingScore` 等字段虽然可选，但省略会选择项目行为。若用户没有明确提供，应询问“接受当前项目默认值，还是指定值”，不要静默省略。

## 可选的回放提示

若用户希望根据已有流量比较规则，可在交付时提示先将规则保存到模拟器规则库，再选择已有逻辑节点时间线和候选规则回放；这是可选的评估方式，不是生成规则的前置条件，也不应为迁就回放擅自改写规则。

回放前要核对实际兼容性：候选规则声明的业务 Attribute 必须在源 Contract 中有同名同类型声明，集合上限不得小于源契约上限；实际 Ticket 必须同时通过源、目标属性校验。候选规则使用的 Fact 须与源声明的名称、类型、scope 和集合上限一致。候选 Provider 应能按目标 Contract 握手，所需 Fact 的源/目标 Provider 标识和版本相同且能力匹配；日志还须包含所需的宿主 Object Fact 入池及后续变更输入。默认使用独立内置 Provider；Python Provider 可选择现有部署并单独启动 Worker，进程内自定义 Go Provider 暂无通用隔离复制方式。

时间线还需包含实例起点、连续事件及必要的入池前史；规则执行结果分化也可能导致回放中途冲突。不要仅凭 JSON 格式有效断言可回放；完整条件与操作见 checkout 的 `doc/simulator/timeline-replay.md`。

## 可机械生成的内容

在业务语义已确认后，下列内容可直接生成，不需要再询问：

- 固定版本：`match-rule/v1`、`logical-node-contract/v3`、`prefilter/v3`、`evaluation/v3`、`expression-scalar/v3`；
- 由表达式结果类型唯一确定的 `resultType`；
- `arrival`、`oldest` 等 schema 强制要求的空 `params: {}`；
- JSON 缩进、字段顺序与文件末尾换行；
- 由已确认字段类型唯一决定的 ref op，例如 `int64_ref`、`strings_ref`。

## 校验与交付判断

使用本次实际加载的 Skill 所随附的验证器，命令和 Windows `py -3` 用法见 [验证准确文件](../SKILL.md#4-验证准确文件)。

- `valid: true`：说明 JSON 通过本 Skill 的格式检查；不代表后端编译或生产集成已验证。
- `valid: false`：根据 `path`/`code` 修正纯机械错误；任何语义性修正必须回问用户。
- 即使 `valid: true`，使用 Fact 的规则仍需宿主提供符合 Contract 的 Provider。没有验证生产 Provider 时，只能表述为“RuleJSON 格式有效”，不能表述为“生产集成已验证”。
- 验证器给出的指纹是 Skill 本地校验指纹，不是核心编译器或回放日志中的规则 Fingerprint。
