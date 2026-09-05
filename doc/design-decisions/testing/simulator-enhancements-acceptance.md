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
