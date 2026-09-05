# 模拟器增强验收记录

日期：2026-09-06。状态：前五项已完成整合验收；第六项另分支研究。环境：Windows amd64、Go 1.26.5、Node 24.16.0。

## 目标与提交边界

| 目标 | 用户可操作的验收条件 | 当前证据 |
| --- | --- | --- |
| 1 自动更新 | 客户端检测仓库新 Release，下载校验 ZIP，替换客户端及 sidecar，重启；免安装目录可更新，失败可恢复 | `eeffc15`；Windows 外部进程更新/恢复测试通过，独立任务验证实际 Tauri 窗口可见及启动确认 |
| 2 分布生成 | int64 可选择不同分布；多值字段可配置抽样数量与值分布；uint64 可输入多个区间，端点与大整数保持精确 | `fbf9cd5`；Go/HTTP/前端测试，真实表单区间与 MaxUint64 生成显示通过 |
| 3 属性关联 | 指定字段生成 TicketID；多个属性取同一生成值；非法类型、引用或循环有明确错误 | `3dc8bfd`；引用及边界测试通过；真实流量 Match 中 region、modes 均等于 TicketID |
| 4 持续模拟 | 配置流量分布、开始及停止；按间隔和数量上限匹配；能复用新增生成配置；替换场景及关闭不残留调度 | `feb67ce`；生命周期/泊松测试及跨模块 HTTP 测试通过，真实 UI 150 ms 间隔运行通过 |
| 5 比赛分析 | 区分成局等待和实际处理开销；逐局记录；选定多局后聚合范围正确，缺失样本不当作零 | `93567a3`；Provider 延迟计时、接口一致性及多选统计测试通过，真实页面两局聚合通过 |
| 6 Python Provider 研究 | 前五项完成后在独立分支研究，明确三类 Provider 的接入、性能、异常和契约边界，以实验支撑结论 | 尚未开始 |

第 1 至 5 项分别提交，第 2、3 项不能合并为一个提交。第 6 项保留在独立分支。

## 基线

基线提交：`ac9dd6f`。开始时已有未提交的 `AGENTS.md`、文档中心和变更记录修改；这些修改不属于本次功能实现。

- `go test ./internal/simulator ./internal/simulatorapi -count=1`：通过。
- `npm --prefix apps/web test`：9 个文件、54 项测试通过。
- `npm --prefix apps/web run build`：通过，存在原有 bundle 体积提示。
- `npm --prefix apps/desktop run check:config`：通过。

现有 `durationMs` 为最早成员创建至成局轮次时间的等待时长；当前基线没有逐局实际处理耗时，不能把该等待字段当成处理开销。

## 最终联动验证

整合分支：`codex/simulator-enhancements`，功能基于 `3dc8bfd` 及以上五个独立提交。

- `go test ./... -count=1`、`go build ./...`、`go vet ./...`：全部通过。
- `npm --prefix apps/web test`：10 个文件、70 项测试通过。
- `npm --prefix apps/web run build`、`npm --prefix apps/desktop run check:config`：通过，保留原有 bundle 体积提示。
- `powershell -NoProfile -File apps/desktop/scripts/test-update.ps1`：通过。用真实 Windows EXE 及外部 PowerShell 进程验证成功替换、健康确认、失败恢复、两个 ZIP 布局、不完整包及路径校验。
- [`TestEnhancementsGeneratedTrafficMatchHistory`](../../../internal/simulatorapi/enhancements_integration_test.go)：真实 HTTP 调用生成器、调度及历史链路，验证区间范围/去重、关联 ID、成局计时字段、每轮上限及停止后不再注入。

浏览器控制工具连接独立测试 API `127.0.0.1:18081` 与 Vite `127.0.0.1:15173`，未使用 demo 数据：

1. 在 Tickets 表单启用 region（当前 Ticket ID）、modes（共享 region）、playerLevel（三角分布）。恒定 10 条/秒、150 ms 匹配间隔、每轮上限 2；停止时显示注入 74 条、产出 73 局、49 轮。Match `match-73` 成员 `1072` 的 region/modes 均为 `1072`，playerLevel 为 `26`。
2. 选择 `match-73` 和 `match-72`，窗口仍为 73 局而分析为 2 局；等待 50/0 ms 对应均值 25 ms、P95 47.5 ms。切换处理纳秒字段后保持 2 个真实样本。
3. 测试场景加入 uint64s 属性 pool，在表单输入 `1-100,200-400`、数量 4、偏大值抽样，生成的两条列表分别为 `81,285,272,342`、`293,286,11,206`；均在区间内且无重复。
4. 集合改为 `18446744073709551615`、数量 1，列表中两条新 Ticket 都显示完整原值。浏览器未捕获到控制台 error。

更新独立任务还验证了公开仓库历史 `v0.1.1` ZIP 的真实下载、SHA-256 校验及解包，Rust 测试和构建、实际 Tauri 程序的启动确认与窗口可见性；未对用户实际安装执行升级，未创建远端 Release。

## 验证边界

- 当前无 gcc 且 CGO 未启用，未运行 Go race detector；已有串行所有权与生命周期测试不能替代竞态检测。
- 更新面向 Windows 独立目录便携版，不支持 NSIS/MSI、私有仓库或重命名主程序；ARM64 未实机验证。首次正式发布需递增版本并包含启动确认协议；历史无协议版本作为更新目标会回滚。
- 计时为成功调用的墙钟耗时，不是 CPU 时间；Windows 极短调用可测得真实 `0 ns`。详见[测量边界](../../simulator/match-history.md)。
- 区间不展开；多值抽取数量上限 4096，仍受 Contract.maxValues 限制。流量调度是普通进程墙钟调度，不承诺实时 SLA，延迟由 lagMs 显示。

## 2026-09-06 图表与导出补充验证

分析图表任务工作区验证（提交前）：`npm --prefix apps/web test` 通过 11 个文件、74 项测试；`npm --prefix apps/web run build` 通过类型检查与生产构建，仍有 Vite 单包超过 500 kB 的体积提示。新增回归覆盖窗口交集、空选集、缺失/零/不安全整数、Fact 样本加权、分组键冲突、CSV 转义及六个时间桶边界。

Windows Chrome 无头浏览器以本地 Vite demo 数据实际操作分析页：选择全部窗口，保存 PNG（120754 字节）、SVG（7812 字节）、CSV（374 字节），下载事件均成功，PNG 打开检查为 2800 × 1200，图表显示 3 场等待 32/40/48 ms。清空选择禁用图片导出、逐局重新勾选、全部模式取消一场保留其余两场、切换折线与分组、切换缺失处理耗时显示空态均通过。模拟下载动作抛错后，页面出现明确失败提示；没有未捕获页面错误。

边界：该浏览器验收使用明确的 demo 数据，不宣称是真实引擎测量；处理耗时 0 与超大整数语义由单元测试验证。未实机验收 Tauri/WebView2 下载目录设置或操作系统保存对话框；下载提示只确认已交给宿主，不宣称最终磁盘保存成功。PNG/SVG 导出全部分析范围而非当前缩放视口，CSV 导出图点聚合数据而非全量原始历史。

## 2026-09-06 Tauri/WebView2 桌面导出实测

针对图表提交 `70d4419`，使用该工作树的 Go sidecar、生产 Web 构建和 `cargo build --manifest-path apps/desktop/src-tauri/Cargo.toml --release --features custom-protocol` 生成真实 Tauri 程序（命令成功）。环境：Windows x64，Tauri 2.11.5，WebView2 `Edg/152.0.4191.62`；页面源为 `http://tauri.localhost`，桌面注入标记为真。此次只核验实现，不增加文件系统权限、原生命令或保存依赖。

测试实例通过自己的随机端口 API 创建 3 张 Ticket 并运行成局，真实等待值分别为 12、11、10 ms，处理实测值均为 0 ns。进入比赛分析并选择全部窗口，依次点击 PNG/SVG/CSV 保存按钮。在默认下载行为下，三个文件实际生成于系统「下载」目录：

| 格式 | 实际字节 | 内容验证 |
| --- | ---: | --- |
| PNG | 117489 | 2800 × 1200，打开图像检查，三局柱值为 12/11/10 ms |
| SVG | 8111 | XML 解析成功，1400 × 600 |
| CSV | 362 | 三行分别为 match-1/2/3，durationMs 均值 12/11/10，样本数均为 1，与 API 响应一致 |

Windows Computer Use 能读取 UIA 控件，但激活失败（`failed to activate captured window`），因此通过仅给本测试实例启用的 WebView2 CDP 连接操作真实网页按钮。Playwright 连接会默认重定向下载到临时目录，故在验收前显式设置 `Browser.setDownloadBehavior` 的 `behavior: default` 恢复宿主默认；未调用 `saveAs`，未指定替代下载路径。直接检查系统下载目录内的文件和内容，排除了“测试框架代为保存”的假阳性。

本结果补充前一节未实测 WebView2 的限制，确认现有锚点下载在该环境可用。页面仅报告已提交下载请求，未把取消、下载失败或未知结果标成成功；本次未触发系统「另存为」或取消对话框，不宣称所有 WebView2 版本和个人下载设置行为相同。测试实例及其自有 sidecar 在验证后关闭，主任务端口与用户应用未终止。

## 2026-09-06 界面优化主工作区联调

本次在 `codex/simulator-enhancements` 集成独立表单和图表任务；未合并 Python 研究原型。

| 用户目标 | 已核对的实际证据 |
| --- | --- |
| 表单配置，避免手写 JSON | 规则表单从真实 Schema 派生 38 个 Scalar 和 8 个 Bitmap 操作；Contract、Provider、Tick、Runtime 与场景部署均有类型控件；无 JSON 文本编辑器 |
| SeedOrder 与评分实际接入 | `TestRuleFormSeedSelectionReachesRuntime`、`TestRuleFormCandidateScoringReachesRuntime` 观察真实匹配成员；主浏览器保存 int64_priority/playerLevel 及 int64_field/weight=1.75/missingScore=-2 后 API 回读一致 |
| 图表显示与导出保存 | 真实对象 4000/4001 成局后图表显示等待 89834/89584 ms，均值 89709 ms；取消一场后统计与图表缩为一场；浏览器与真实桌面文件保存证据见上文 |
| 简洁且可操作的输入 | 单条非法小数直接指出字段；批量/单条切换与折叠保留输入；持续流量锁定、停止和接续编号；512px 导航与表单可操作 |
| 生成参数覆盖 | UI 创建两条共同 Facts 为 latencyMs=42、preferredRoles=[support,tank] 的对象，创建时间相差 250 ms；真实 HTTP 回归覆盖新增请求字段 |
| 完整对象浏览 | UI 生成 120 条，下一页显示 5100–5119 共 20 条，末页按钮禁用；页大小切换回首页 |
| 真实总览状态 | Ready/Stopped 与物理节点 enabled 的回归，移除无测量依据的负载比例；多规则同 placement ID 使用唯一标识 |

主工作区已执行全仓 `go test ./... -count=1`、`go build ./...`、`go vet ./...`，均通过。
Go 生产改动只在模拟器 HTTP 生成输入适配层，未修改匹配核心。

最终主工作区前端验证：15 个文件、92 项 Vitest 全部通过；生产构建及桌面配置检查通过。
保留原有 Vite 单包体积提示，未运行 Go race detector。

整合后再次实际操作新建规则 2（固定分数 17）、复制为规则 3（容量 9），切回规则 2
仍保留分数，统一保存后 API 回读两条分数均为 17、容量分别 8/9、canComplete 均为 false；
原规则的评分权重 1.75 和缺失分数 -2 保留。删除规则 2 并保存后，总览准确显示剩余
2 个就绪逻辑节点与零等待对象，空趋势不再显示虚构数据。字符串候选逐项填写 a,b 后，
生成对象 6000 的 region 为单元素 ["a,b"]，未被拆分。

主要功能提交：输入表单 `58de8b0`、图表 `fe8facb`、规则完整表单 `d2c2b65`、
规则生命周期 `a09bf37`；另有界面联调/分页/真实拓扑/字符串输入修复及桌面验证提交。
实现取舍见 [ADR](../adr/simulator-form-configuration.md)，完整入口见
[界面操作](../../simulator/interface-guide.md)与[规则表单](../../simulator/rule-forms.md)。
