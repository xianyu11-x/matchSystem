---
name: create-match-rule-json
description: 将自然语言的匹配需求转换为当前项目的 match-rule/v1 JSON 文件；对于每个缺失的语义决策，要求用户逐项确认，只有在用户明确请求时才应用保留的无作用默认规则。用于创建、修改或验证 MatchSystem 规则 JSON；不要用于已归档的 pre-v3 格式或完整模拟器场景，除非用户明确请求。
---

# 创建匹配规则 JSON

根据用户的规则描述创建一个符合生产形态的 `match-rule/v1` 文档。严格保留用户意图：对于缺失的决策提问，不自行选择业务语义、限制、排序、标识符、Provider 行为或输出位置。

## 生成依据

基于当前 checkout，不要依据记忆或已归档示例。

1. 必要时，使用 `git rev-parse --show-toplevel` 定位仓库根目录。
2. 阅读当前的 `api/schema/match-rule/v1.schema.json` 以及它引用的每一个 schema。
3. 阅读 [references/requirements-and-mapping.md](references/requirements-and-mapping.md)，了解项目特定的语义约束和澄清检查清单。
4. 查阅其中链接的当前非归档文档，了解 JSON Schema 无法表达的语义。
5. 随附的验证器有意保持为轻量级、Skill 本地的格式检查工具。它不能替代主机侧编译或 Provider 检查。

上述文件仅用于指导规则生成和语义澄清。下面的验证命令只读取目标 JSON 以及本 Skill 中嵌入的规则；它不会加载仓库 schema 或项目包。

不要从 `doc/design-decisions/archive/` 推导当前行为。

## 工作流程

### 1. 建立需求台账

将请求转换为以下方面的明确决策：

- 输出文件路径；
- 完整的 `ruleKey` 标识；
- Contract 属性、Fact、Index 以及限制策略；
- Prefilter 候选集语义；
- `canJoin` 和 `canComplete` 谓词；
- 候选评分和种子选择；
- 运行时限制，包括候选评分池上限和保留的 Top-L 上限；
- 必需的 Tick/Object/Match Fact Provider。

将每一项标记为已确认、机械推导或未解决。schema 强制要求的常量（例如 `schemaVersion`、`resultType`、必需的空 `params` 以及 JSON 语法）属于机械推导项。除非用户明确启用下面的默认规则模式，否则项目默认值不等于用户确认。

### 默认规则模式

只有当用户明确要求对空白/空配置使用默认规则时，才使用 [assets/default-rule.json](assets/default-rule.json)，例如“空白配置使用默认规则”“生成空白配置并采用默认规则”，或同样明确的请求。仅要求空白规则、遗漏细节或说“随便生成”，都不会启用此模式。

启用后：

1. 将资产作为完整 RuleJSON 的起始内容复制。不要询问默认配置已经提供的字段。
2. 仅应用用户明确提供的覆盖项。如果覆盖项含义不明确，只询问该覆盖项。
3. 如果未提供目标位置，则写入 `rules/default-rule.json`。
4. 报告成功前，使用常规验证器验证准确写入的文件。

保留的默认规则有意设置为无作用：`prefilter.none`、`canJoin: false` 和 `canComplete: false` 表示它可以编译并加载，但不能创建 Match。它不声明任何 Attribute、Fact、Index 或 Provider 义务。其标识为 `namespace: "default"`、`ruleId: 1`；其余固定值记录在 [references/requirements-and-mapping.md](references/requirements-and-mapping.md) 中。在交接说明中陈述这一行为，避免将 `default` 误认为匹配全部。

### 2. 先提问，再做决定

在默认规则模式之外，如果任何语义项尚未解决，应在写入最终 JSON 文件前停止，并提出聚焦且编号的问题。将相关问题分组，并使用项目术语解释有效选项。只询问仍未解决的内容，但不得默默选择：

- 名称、类型、作用域、基数限制或索引边界；
- 匹配兼容性、完成阈值或空值/缺失值行为；
- 评分类型/方向/权重或种子排序；
- 运行时容量和尝试预算；
- 标识符、命名空间行为或目标路径；
- 是否接受可选字段/Contract 默认值；
- 必需的 Fact Provider 是否已经存在，以及它们准确提供哪些值。

等待期间不要创建填充了占位符的 `.json` 文件。用于讨论的草稿必须明确标记为非最终稿，且不得保存为请求的产物。

### 3. 仅构造已确认的语义

使用当前的 `match-rule/v1` 外层结构生成一个格式化的 JSON 对象。根据已确认的表达式推导机械上必需的声明，但绝不臆造其边界或行为。保持以下不变量：

- `PlacementID` 属于部署拓扑，绝不能放入 RuleJSON。
- RuleJSON 的 `ruleKey` 必须与主机使用的 `LogicalNodeKey.Rule` 完全一致。
- 每个被引用的属性、Fact 和 Index 都必须在 `contract` 中声明，并具有匹配的类型和作用域。
- Prefilter 操作数使用完整的 `expression-scalar/v3` 外层结构；Evaluation 包含完整的 Bool 根节点。
- `prefilter` 仅限索引。`none` 表示空候选集，而不是“所有候选”；绝不要伪造宽泛的锚点。
- `int64_field` 评分和 `int64_priority` 种子选择必须引用已声明的 `int64` 属性。
- `attemptLimitPerProduceMatch` 不得超过 `attemptLimitPerMatchRound`。
- Fact 声明不实现 Provider。在交接说明中记录外部 Provider 义务。

除非用户明确要求场景，否则不要将规则包装在模拟器场景中。

### 4. 验证准确文件

从仓库根目录运行：

```powershell
python Skills/create-match-rule-json/scripts/validate_rule.py <path-to-rule.json>
```

该脚本仅使用 Python 标准库和本 Skill 中的格式规则；它不会导入项目包，也不需要 Go。它会检查 JSON 语法、必需字段/未知字段、schema 版本、基本类型/枚举、表达式外层结构以及运行时数值关系。它有意不编译表达式、不解析每个声明引用，也不验证运行时/Provider 行为。直接修复机械性的格式错误。如果修复会改变语义、字段类型、边界、谓词、默认值或运行时行为，应改为询问用户。

不要仅因为格式检查返回 `"valid": true` 就声称规则已准备好供主机使用。如果规则使用 Fact，应单独说明所需的 Provider 绑定；此验证器无法证明生产主机正确提供了这些绑定。

### 5. 交接

报告保存路径、格式验证结果和指纹、用通俗语言描述的规则行为，以及任何外部 Fact Provider 义务。明确区分“RuleJSON 格式有效”和“主机/Provider 集成已验证”。
