# Python Provider 场景/API 接入验收记录

日期：2026-09-09。状态：本次提交验收通过。环境：Windows amd64、Go 1.24.6、
Node 24.16.0、Python 3.14.2（`C:\Users\yidingxie\AppData\Local\Programs\Python\Python314\python.exe`）。
本记录只列已经执行的检查；它不构成跨机器性能或线上容量 SLA。

## 契约检查

- 使用 PowerShell `ConvertFrom-Json` 解析 `api/schema/simulator-scenario/v1.schema.json`
  和 `api/schema/simulator-traffic/v1.schema.json`：通过。
- 使用 Python 3.14.2 的 PyYAML `safe_load` 解析 `api/openapi/simulator.yaml`，并检查
  `PythonProviderConfig`、场景 PUT 的 500 响应、TrafficStatus 的 `completed` 状态和固定模式字段：通过。
- 解析 OpenAPI 外部 `$ref`：共检查 131 个本地引用，缺失 0 个。
- 从[Python Provider 接入](../../simulator/python-provider.md)提取完整 JSON 场景示例并
  解析，确认 `rules[0].pythonProvider.timeoutMs=2000`、脚本字段和完整 runtime 预算存在：通过。
- 对解释器解析说明与实现核对：显式 `python` 优先，其次 `PYTHON_PROVIDER_EXECUTABLE`；未配置时
  Windows 优先 `py -3`，再探测 `python`/`python3` 等可用解释器。
- 对本次修改涉及的文档执行本地链接检查：共检查 166 个本地链接，缺失 0 个。

## Go 与真实 HTTP

`$env:PYTHON_PROVIDER_EXECUTABLE=<Python 3.14.2 绝对路径>; go test ./internal/simulator -run 'Python|Fixed' -count=1 -v`：通过，12 项测试全部通过，且均直接走生产 `internal/simulator` Provider 实现。覆盖项如下：

- `TestScenarioPythonProviderReadsAllFactScopes`、`TestScenarioPythonProviderHandshakeFailureKeepsOldRuntime`、
  `TestPythonProviderFailureIsTerminalAndInspectable`：三类 scope、握手失败保留旧 runtime 和
  Provider 终态错误诊断。
- `TestProductionPythonProviderTimeoutReapsWorker`：使用真实 `time.sleep(60)` 脚本，75 ms
  RPC 超时及时返回 deadline 错误并确认 Worker 进程已回收。
- `TestProductionPythonProviderCancellationReapsWorker`：同样使用真实 sleep 脚本，取消调用上下文
  后及时返回 `context.Canceled`，不把调用方取消记录为 Provider 失败，并确认 Worker 已回收。
- `TestProductionPythonProviderDefaultPyLauncherReapsInterpreter`：清除显式解释器配置，实际通过
  Windows `py -3` 启动；日志记录 launcher PID `24992`、interpreter PID `72416`，超时后确认
  interpreter 进程回收。
- `TestFixedTrafficPythonProviderSpeedDoesNotChangeVirtualResults`、`TestFixedTrafficUsesVirtualClockAndCompletes`、
  `TestFixedTrafficRejectsVirtualTimelineOverflow`、`TestFixedTrafficProcessesAllDueArrivalsBeforeMatching`：
  fixed 虚拟结果与慢 Provider、完成状态、时间线溢出和所有到期到达顺序。
- `TestProductionPythonProviderScenarioReplacementAndCloseReapWorkers`：场景替换回收旧 Worker，
  服务关闭回收新场景 Worker；`TestPythonProviderCandidateBuildFailureReapsAllStartedWorkers`：
  候选构建在后续脚本缺失时回收此前已启动的全部 Worker。

`$env:PYTHON_PROVIDER_EXECUTABLE=<Python 3.14.2 绝对路径>; go test ./internal/simulator ./internal/simulatorapi ./experiments/pythonprovider -count=1`：通过。覆盖场景 Python Worker、HTTP 适配、实验适配器和 fixed traffic。

集成任务随后执行 `go test ./... -count=1` 和 `go vet ./...`：均通过。

前端契约同步由 `npm.cmd --prefix apps/web test`（18 个文件、105 项）和
`npm.cmd --prefix apps/web run build` 验证：均通过；build 仅报告既有 Vite chunk 大小提示。
PowerShell 中使用 `npm.cmd` 是为了绕过本机执行策略对 `npm.ps1` 的拦截。

启动临时编译的 `simulator-api` 服务（`127.0.0.1:18082`）后，从本文档的完整场景 JSON
提取场景，替换为临时服务端脚本，并执行实际 HTTP 链路。临时脚本返回 Tick `clock`、Object
`tier` 和 Match `members`；临时规则的 `canComplete` 同时读取 Tick/Match Fact，以确认三类
输入确实进入匹配评估。结果如下：

1. `PUT /api/v1/scenario` 成功；`GET /api/v1/scenario` 返回相同的
   `rules[0].pythonProvider.script`，证明 binding 场景往返保留。
2. `POST /api/v1/tickets/custom` 接受 1 个 Ticket；以 `now=1700000000123` 调用
   `POST /api/v1/rounds`，响应 `produced=1`，Object Fact `tier=1`，Python Match Fact
   `members=1700000000123`，`durationMs=123`。本次观测的 `processingDurationNs` 为
   `527900`；它随机器和脚本执行变化，只作为该调用的墙钟观测。
3. 以 `mode=fixed`、`steps=3`、`stepMs=100`、`startTimeMs=1800000000000`、
   `matchIntervalMs=1`、`rate=10` 调用 `POST /api/v1/traffic`；最终
   `state=completed`、`step=3`、`totalSteps=3`、`virtualNow=1800000000200`、
   `injected=2`、`produced=2`、`lagMs=0`、`wallElapsedMs=1`。
4. `GET /api/v1/matches?limit=100` 返回 `total=3`，包含前述单轮结果和 fixed 两次成局。

这次验收也证明 `matchIntervalMs=1` 在 fixed 模式符合 Schema 和当前前端固定模式输入限制；
realtime 仍使用 100 毫秒下限。

## 边界

Python 启动/握手成本发生在 runtime 构造阶段，不计入成功成局的 `processingDurationNs`；
`ProduceMatch` 内的 RPC 等待会计入该字段。fixed 的 `virtualNow` 不因 Python、核心匹配或
Go 调度墙钟耗时而推进，但这些成本会增加 `wallElapsedMs` 并消耗 CPU、内存、解释器和管道
容量；realtime 还会体现在 `lagMs` 和后续到期工作上。大规模模拟仍需另测 Worker 数量、候选
规模、脚本执行时间、并发部署和 realtime 调度延迟。

`git diff --check`：通过。上述检查针对当前共享工作区快照；若后续代码再次改变，应重跑受影响
的契约和 HTTP 验收。

## 前端最终审计状态

前端代码审计和回归已关闭此前的五项缺口：

- 场景序列化：`scenarioPayload` 对已有和新建规则都写入/删除
  `pythonProvider`；`apps/web/src/lib/api.test.ts` 的 Provider binding 往返测试覆盖该路径。
- 旧 `document` 同步：`Rules.tsx` 在场景 Provider 变更时同步当前文档，并在切换规则时以
  `documentMatchesSelectedRule` 阻止旧文档的 Contract、Descriptor 和 Runtime 数据泄漏；
  `factSources.test.ts` 覆盖当前编辑数据优先和空数据不回退。
- 诊断展示：`formatApiError` 保留 Provider 错误详情中的 `diagnostic`，对应
  `api.test.ts` 的结构化 Provider 诊断测试通过。
- fixed 虚拟时间安全范围：`TrafficControl.fixedVirtualEnd` 拒绝超出 JavaScript 安全整数
  的最终时间，`TrafficControl.test.ts` 覆盖边界。
- 删除复活：`apps/web/src/components/ScenarioSettingsEditor.tsx:184-185,355-357` 的 Provider
  编辑通过 `replaceRule` 写回完整规则行，`replaceRuleDraft`
  （`apps/web/src/lib/scenarioDraft.ts:18-25`）整行替换，避免已清除字段从旧草稿合并回来；
  `apps/web/src/lib/scenarioDraft.test.ts:168-185` 验证同时删除 `pythonProvider` 和 Tick
  Descriptor 后，保存行及 `documentFromDraft` 均不存在这两个字段。

真实浏览器截图保存在本地 `artifacts/`：`web-python-binding-real.png` 记录 binding 保存画面，
`web-fixed-complete-real.png` 记录 fixed 完成画面，`web-fixed-failed-real.png` 记录 fixed 失败画面。
这些图片作为本地验收附件保留，按仓库约定不纳入 Git 提交；实际操作与回读结果记录如下。

最终真实浏览器验收连接 API `127.0.0.1:18080` 完成：启用 Provider 和三个 scope 后，GET
回读确认 binding 与三个 Descriptor 均存在；关闭 Tick 后 GET 回读为 Tick=false、Object/Match=true，
刷新页面后仍保持关闭；再次开启 Tick 后三个 scope 均为 true；关闭 binding 后 GET 不包含
`pythonProvider`，刷新后仍保持关闭。示例已有 Object Facts 时关闭 Object scope 被服务端以
`MISSING_DESCRIPTOR` 拒绝，属于保持 Contract/Descriptor 一致性的正常契约校验，不计为 UI 缺陷。
API、Vite、Edge 已停止，`18080`、`15176`、`9239` 无监听，临时脚本和 profile 已清理；至此
代码审计、18 文件 105 项前端回归、真实 HTTP 和真实浏览器闭环均已记录。
