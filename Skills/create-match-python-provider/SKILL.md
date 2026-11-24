---
name: create-match-python-provider
description: 根据 match-rule/v1 和已知业务语义生成可由 MatchSystem 模拟器绑定的 Python Fact Provider，覆盖 Tick、Object、Match 事实及部署和验证；适用于创建、修改或核查 Provider，不用于仅编辑匹配规则。
---

# 创建模拟器 Python Fact Provider

为给定的 `match-rule/v1` 生成能被模拟器生产 runner 加载的 Python 脚本；规则声明了什么与事实值如何计算是两件事。先阅读 [协议与验证参考](references/protocol-and-validation.md)，它包含不依赖源码仓库的输入、输出、部署和回放方式。若当前有 MatchSystem 仓库，可对照其 `doc/simulator/python-provider.md`、`doc/simulator/fact-sources.md` 与 `internal/simulator/python_provider.go` 核实版本；仓库示例 `examples/simulator/builtin_provider.py` 实现了五项标准能力。仓库路径相对于当前 checkout，Skill 的 `references/`、`assets/` 路径相对于实际加载的 Skill 目录。

## 识别事实与语义

1. 读取完整 RuleJSON 的 `contract.facts`、表达式及相关属性，按 Fact 列出名称、类型、scope、`maxValues`、用途、输入来源、更新时机及缺失值处理。保留规则及其标识；不要把 Attribute、Index 或 `createTime` 误认为 Fact。
2. 用用户提供的业务说明、现有实现和项目已明确规定的内建语义确定计算方式。规则中的比较或 Fact 名称通常不足以推断来源、单位、聚合算法和空值策略。缺少会改变结果的语义或不可用的输入时，列出具体待决问题；先完成不依赖答案的分析，不要以假值、默认零值或凭空推断冒充可用实现。
3. Contract 无 Fact 时不需要 Provider；若用户仍明确要求绑定，说明绑定没有业务事实可提供，使用至少一个合法的空能力描述。只覆盖标准五项能力且语义与模拟器内建一致时，可参考仓库现成脚本；不要把内建语义套到同名不同类型或自定义需求上。

## 实现

- 以 Python 标准库优先，导出可调用的 `describe()` 和 `provide(method, data)`；`describe()` 声明实现真正支持的固定能力，按名称、类型、scope 和 `maxValues` 覆盖规则所用的每个 Fact。不能在运行时仅从 Contract 复制能力或把虚构的能力填进场景。
- 为所用 scope 实现 `tick`、`object`、`initialize`、`join` 对应的计算。返回当前 scope 且当前 Contract 声明的值；特别是 Match 的初始化和每次加入都必须返回全部 Match Fact 的完整快照，而不是增量。正确处理 int64/uint64 边界、列表上限、输入缺失与错误，不通过截断或静默修正改变业务语义。
- 面向单一规则时可只实现该规则需要的固定能力；若实现共享能力目录，利用握手上下文将业务响应投影到当前 Contract。一个绑定的 Worker 自包含；不要假设会自动调用 Go Provider 补齐其他 scope。脚本普通日志写 stderr，不能污染 JSONL 协议。
- [assets/member_count_provider.py](assets/member_count_provider.py) 是仅支持标准 `memberCount`（Match、int64）的最小完整示例，适用于该 Fact 是唯一 Match Fact 且没有其他 Fact 的规则，例如仓库的八票 FIFO 示例。它可直接复制、运行或作为扩展起点；不要据此捏造其他 Fact。
- 按用户指定的位置保存 `.py`；未指定时选取当前任务工作目录中可辨认的位置，并报告绝对路径。生成的注释使用中文。

## 验证和交付

先导入生成文件并调用 `describe()` 与必要业务方法，检查各 scope 的事实值、匹配初始化和多次加入、缺失输入、边界值及用户指定语义。再使用独立模拟器实例实际绑定脚本、检查运行态 Descriptor 并用有代表性的 Ticket 执行匹配；需要时验证隔离回放，步骤见参考文件。模拟器不可用时完成可执行的模块自测，清楚报告未完成的握手或集成验证，不把自测或静态规则校验描述为已验证模拟器接入。

交付脚本路径、每个 Fact 的数据来源和计算规则、服务端绑定方法、实际验证结果与尚需提供的输入。修改或应用用户现有部署仅在请求范围内执行；演示和验收优先使用独立实例，避免清空现有等待队列。
