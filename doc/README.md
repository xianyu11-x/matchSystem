# MatchSystem 文档中心

这里是项目文档的唯一总入口。文档按读者要解决的问题分成三类，当前规范、历史材料和
测试记录不再混放。

| 分类 | 内容范围 | 建议入口 |
| --- | --- | --- |
| [模拟器](simulator/README.md) | 模拟器架构、使用说明、Fact 数据、Match 历史、Web/Desktop 与发布 | [使用指南](simulator/usage-guide.md) |
| [匹配系统](match-system/README.md) | 核心架构、参数、规则契约、运行流程、各包代码索引与接入指南 | [架构](match-system/architecture.md) · [参数明细](match-system/parameters.md) |
| [设计决策](design-decisions/README.md) | ADR、设计变更、评估、约束、功能/性能/发布验证与历史归档 | [决策索引](design-decisions/README.md) |

## 按任务阅读

- 首次运行模拟器：先读[模拟器使用指南](simulator/usage-guide.md)，再读
  [架构](simulator/architecture.md)。
- 接入匹配核心：依次阅读[匹配系统架构](match-system/architecture.md)、
  [参数明细](match-system/parameters.md)和[使用指南](match-system/usage-guide.md)。
- 编写 RuleJSON：从[参数明细](match-system/parameters.md)进入 Contract、Expression、
  Prefilter 和 Evaluation 的专题参考。
- 定位代码：使用[包级文档矩阵](match-system/packages.md)，每个包都有架构说明、
  代码索引和用户指南。
- 了解“为什么这样设计”：查看[设计决策](design-decisions/README.md)，不要从历史归档
  推断当前 API。

## 文档维护约定

1. 当前行为说明只放在“模拟器”或“匹配系统”；设计取舍、测试结果和历史演进放在
   “设计决策”。
2. 同一事实只保留一个权威说明，根目录和组件 README 只链接到它。
3. 代码符号使用仓库相对链接；版本化 JSON 契约同时以 Go 编译器和 `api/schema/` 为准。
4. `design-decisions/archive/` 仅用于历史对照，不是当前规范来源。
5. 改动代码、参数或路径时，同步更新所属分类的 README 和所有相关交叉链接。
6. 代码结构变更、功能更新和重要决策必须在同一任务中完成文档同步；提交信息、PR
   或聊天记录不能替代仓库文档。执行流程见 [Repository Guidelines](../AGENTS.md)。

### 变更应记录在哪里

| 变更类型 | 必须更新的文档 | 最少记录内容 |
| --- | --- | --- |
| 代码结构变更 | 所属分类的架构、代码索引；涉及核心包时更新包级文档矩阵 | 变更前后职责、依赖与入口，新增/删除/重命名路径及迁移影响 |
| 功能新增、更新或行为修复 | 所属分类的使用指南、参数或专题说明；涉及接口时同步 `api/` 契约 | 新旧行为、使用方式、默认值、限制、兼容性及示例 |
| 重要决策 | `design-decisions/adr/` 中的 ADR，并加入决策索引 | 背景、备选方案、采用方案及理由、代价、影响和实施状态 |
| 验证证据 | 涉及功能、性能或发布验证时更新 `design-decisions/testing/` 对应记录 | 代码版本或未提交状态、环境、命令、结果与限制 |

上述代码结构、功能与决策变化还须在[设计变更记录](design-decisions/change-log.md)中
留下简短条目并链接详细文档；同一任务的相关改动可合并一条，无需逐文件记流水账。
未提交时明确标注“未提交”，不要编造提交号。纯排版或错字修正无需新增变更条目。

重要决策包括模块责任边界、技术选型、版本化契约、默认值、所有权、并发、提交语义、
兼容性和关键性能取舍。ADR 使用可读的英文短横线文件名，例如
`adr/expression-engine-boundaries.md`，标明日期及“提议 / 已接受 / 已实施 / 已取代”状态；
决策被替代时保留原背景并链接后继决策。当前行为文档只描述已实现内容，提案不得写成事实。

`Skills/` 下的文件是 Codex 技能包自身的指令与资源，不属于项目产品文档分类；
`apps/desktop/portable/README.txt` 是随发布包分发的终端用户说明，权威构建流程仍位于
[模拟器 / 客户端构建与发布](simulator/client-build.md)。
