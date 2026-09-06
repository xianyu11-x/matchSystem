# 设计变更记录

本页按已提交的设计里程碑记录“发生了什么”和“当前影响”。它不是逐提交 changelog；
实现细节仍以对应提交、ADR 和当前源码为准。

| 日期 | 提交 | 设计变化 | 当前影响 |
| --- | --- | --- | --- |
| 2026-09-06 | 本次变更 | Windows 便携客户端完整更新 | GitHub 稳定版检查、ZIP 校验、目录备份替换、启动确认和失败恢复；参见[ADR](adr/portable-desktop-update.md) |
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

| 2026-09-06 | 本次变更 | [成局等待与调用耗时分离](adr/match-processing-observation.md) | 独立记录成功成局调用实测耗时，Web 支持选定多局聚合与等待/处理 P95；核心隔离不变 |

## 兼容性结论

- pre-v3 文档和配置只保留在[历史归档](archive/2026-08-27-pre-v3/README.md)。
- 当前运行时没有旧格式双读或自动迁移；旧配置必须离线转换并重新编译。
- 改动版本化契约、默认值、所有权或提交语义时，应新增 ADR，而不是只追加本表。

## 2026-09-06 属性分布与集合抽样

新增逐属性生成界面、四种确定性分布、多值数量与放回策略、精确 uint64 区间，保持旧生成调用兼容。参见[属性生成配置](../simulator/attribute-generation.md)。
## 2026-09-06：持续流量

新增恒定、泊松、周期突发注入与独立匹配间隔，复用批量生成器配置；替换/关闭取消任务，数量仅为规则允许时的上限。见[持续流量](../simulator/continuous-traffic.md)。

## 2026-09-06 属性关联来源

新增当前 Ticket ID 与同类型属性共享来源，客户端支持来源选择；生成前检测引用环、缺失来源和类型/范围冲突，依赖优先执行并复制列表。参见[属性生成配置](../simulator/attribute-generation.md)。

## 2026-09-06 联动验收

前五项以独立提交整合，补充生成配置→持续注入→成局观察的真实 HTTP 回归测试及客户端操作验证。完整命令、提交与边界见[模拟器增强验收记录](testing/simulator-enhancements-acceptance.md)。

| 2026-09-06 | 本次变更 | [模拟器输入界面与配置完整性](../simulator/interface-guide.md) | 单条/批量分组、窄屏导航、明确整数错误、运行锁定与接续编号、批量共同事实和时间/路由参数接入 |
## 2026-09-06 比赛分析图表与导出

多局选择、时间窗口和图表导出共用分析范围；增加柱状/折线、指标与分组统计配置、PNG/SVG 图片和 CSV 数据保存、缺失值空态与下载失败提示。总览改为真实保留历史的最近 30 分钟成局数量，不再使用硬编码曲线。见[Match 历史与图表保存](../simulator/match-history.md#图表与保存)。

界面联调将逐局选择移至图表前方、折叠详细统计，并修正总览空时间桶的数据行标识；同步批量事实/时间与流量界限。

对象列表补齐服务端游标分页与页大小选择；统一表单字号、网格对齐和键盘焦点，见[界面操作](../simulator/interface-guide.md)。
## 2026-09-06 桌面图表下载核验

在真实 Tauri/WebView2 中验证现有 PNG/SVG/CSV 下载可直接落盘，内容与自有 sidecar 生成的比赛一致；保留现有实现，无新增原生命令或权限。明确下载请求提示不等于保存成功，取消不算成功。见[桌面导出实测](testing/simulator-enhancements-acceptance.md#2026-09-06-tauriwebview2-桌面导出实测)。
## 2026-09-06 规则与场景完整表单

规则页以类型化表单覆盖全部 46 个表达式操作、SeedOrder、候选评分、预算、Provider
声明、Tick 值及物理节点/规则部署参数，移除 JSON 文本编辑入口。统一保存当前规则与
场景草稿，保留无关配置；回归验证实际种子顺序和评分对匹配成员的影响。
见[规则与场景表单](../simulator/rule-forms.md)。

总览修正 Ready/Stopped 状态映射、逻辑节点计数及多规则同部署名的唯一标识，移除无测量依据的负载百分比。

字符串候选集合改为逐项表单，保留逗号/空字符串/空白的精确值，修正前端集合基数校验。
## 2026-09-06 规则生命周期与多规则草稿

规则页补齐安全空规则新建、复制、删除及空场景入口；切换前写回当前编辑器，统一
保存全部规则和部署草稿，保存失败保留草稿并显示错误。默认进入配置表单，规则图
作为高级视图，NodeInspector 仅随图显示。见[规则与场景表单](../simulator/rule-forms.md)。

界面增强已完成主工作区联合验收，见[验收记录](testing/simulator-enhancements-acceptance.md#2026-09-06-界面优化主工作区联调)与[表单交互 ADR](adr/simulator-form-configuration.md)。

2026-09-06（本次变更）：修复客户端留白与更新弹窗遮挡。统一输入面板和运行表单边距、趋势状态区；更新窗口使用顶层 dialog。见[界面说明](../simulator/interface-guide.md)与[验收记录](testing/simulator-enhancements-acceptance.md)。

2026-09-06（本次变更）：表达式配置只显示一级，通过子输入按钮逐级编辑；高级规则图同步选择和定位下一级节点。见[规则表单](../simulator/rule-forms.md#表达式逐级编辑)与[验收记录](testing/simulator-enhancements-acceptance.md)。

2026-09-06（本次变更）：比赛分析接入 Ticket 成员属性，支持按规则筛选、数值统计、分类成员分布及图表导出。见[Ticket 属性统计](../simulator/match-history.md#ticket-成员属性分析)与[验收记录](testing/simulator-enhancements-acceptance.md)。

2026-09-06（本次变更）：int64 属性生成新增可配置正态分布，打通均值/标准差的表单、HTTP、Schema 与后端抽样，批量和持续流量共用。见[属性生成](../simulator/attribute-generation.md#可配置正态分布)与[验收记录](testing/simulator-enhancements-acceptance.md)。

2026-09-06（本次变更）：检查四个页面及规则页全部标签的响应式样式，统一图表与数据列表留白，修复全局筛选框宽度污染、卡片拉伸和面板间距。见[界面说明](../simulator/interface-guide.md)与[全页面验收记录](testing/simulator-enhancements-acceptance.md)。
