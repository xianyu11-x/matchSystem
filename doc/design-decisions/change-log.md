# 设计变更记录

本页按已提交的设计里程碑记录“发生了什么”和“当前影响”。它不是逐提交 changelog；
实现细节仍以对应提交、ADR 和当前源码为准。

| 日期 | 提交 | 设计变化 | 当前影响 |
| --- | --- | --- | --- |
| 2026-08-29 | `bcc88a5` | 引入全栈匹配模拟器 | 建立 Web → HTTP/SSE → simulator → matchsystem 的独立宿主边界 |
| 2026-08-30 | `a0fa65a` | 规则配置收敛为 `match-rule/v1` | RuleKey、Contract、Prefilter、Evaluation、评分、Seed 与预算统一发布/回滚 |
| 2026-08-30 | `d1dfeac` | 模拟器 API 与规则编辑器迁移到统一 RuleJSON | 客户端不再维护另一套规则形状，保存前调用生产校验入口 |
| 2026-08-31 | `adb0c65` | 收紧 trusted Fact 流程 | 生产热路径信任同仓库 Provider，契约完整性在启动和测试边界检查 |
| 2026-08-31 | `949b738` | 增加 Provider Descriptor 握手 | Tick/Object/Match Provider 按 scope 覆盖 Contract Facts；使用项元数据必须对齐，Descriptor 可包含额外合法 Fact |
| 2026-09-01 | `5da0988` | Provider 声明与运行时 Fact 值分离 | Contract、Descriptor、Runtime Values 三层不再互相推导 |
| 2026-08-31 | `e159a22` | 暴露 LogicalNode Fact 元数据 | API 可分别查询规则声明、Provider 能力和运行时 Tick 值 |
| 2026-08-31 | `eb15674` | 保留并查看已完成 Match | 模拟器增加内存有界、可展开成员的 Match 快照 |
| 2026-09-01 | `09be902` | 增加 Match History 分析 | 客户端可基于历史快照计算等待时间和分布，不改变核心提交语义 |
| 2026-09-01 | `6c176b1` | 增加 ProduceMatch 阶段指标 | 诊断路径可返回聚合耗时/计数，默认生产路径不承担 metrics 分配 |
| 2026-09-04 | `34dbf7d` | 候选排序限制可参数化 | 基准可独立验证 scoring 上限与 Top-L 路径，运行参数由 RuleJSON 管理 |

## 兼容性结论

- pre-v3 文档和配置只保留在[历史归档](archive/2026-08-27-pre-v3/README.md)。
- 当前运行时没有旧格式双读或自动迁移；旧配置必须离线转换并重新编译。
- 改动版本化契约、默认值、所有权或提交语义时，应新增 ADR，而不是只追加本表。
