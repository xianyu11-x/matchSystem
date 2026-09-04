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

`Skills/` 下的文件是 Codex 技能包自身的指令与资源，不属于项目产品文档分类；
`apps/desktop/portable/README.txt` 是随发布包分发的终端用户说明，权威构建流程仍位于
[模拟器 / 客户端构建与发布](simulator/client-build.md)。
