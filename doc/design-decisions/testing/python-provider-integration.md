# Provider 自描述与 Python Provider 场景/API 接入验收记录

日期：2026-09-09。状态：本文开头记录本任务的统一 Provider 自描述契约迁移；后续“历史基线”
章节记录旧版 Python 场景 Descriptor/Ready-only 的验收，已被当前契约替代，不再代表当前行为。
历史验证环境为 Windows amd64、Go 1.24.6、
Node 24.16.0、Python 3.14.2（`C:\Users\yidingxie\AppData\Local\Programs\Python\Python314\python.exe`）。
本记录只列已经执行的检查；它不构成跨机器性能或线上容量 SLA。复现当前契约时必须使用
最新工作区代码并重新执行相应检查。

## 2026-09-09 统一 Provider 自描述契约

本任务按用户明确要求完成契约文档与 Schema/OpenAPI 迁移：Go Provider 在 LogicalNode 加载
阶段通过进程内 `Describe()` 返回 `ProviderDescriptor`；Python Provider 在 JSON-lines
`handshake` 中由必需的 `describe()` 返回 `Descriptors`。三类场景手工 Descriptor 字段和
`pythonProvider.descriptorSource` 从 `simulator-scenario/v1` 移除，Contract 仍独立保留，
能力不会从 Contract 合成。运行态 `providerDescriptors.*.source` 统一为 `go`/`python`；
旧 JSON 由未知属性校验拒绝，不能静默生效。

本次文档/契约检查已执行：

- 使用 PowerShell `ConvertFrom-Json` 解析修改后的
  `api/schema/simulator-scenario/v1.schema.json`：通过；Schema 不再声明三个场景 Descriptor
  字段或 `descriptorSource`，并保留 `additionalProperties: false`。
- 使用 Python PyYAML `safe_load` 解析修改后的 `api/openapi/simulator.yaml`：通过；
  `FactProviderDescriptor.source` 的枚举为 `go`/`python`，场景请求/响应说明不再允许手工
  Descriptor 或来源开关。
- 遍历 `api/schema` 的 JSON 外部引用并检查本次 `api/`、`doc/` 非归档 Markdown 本地链接：
  外部引用缺失 0 个，Markdown 本地链接缺失 0 个；`git diff --check` 通过（仅报告工作区
  原有的 LF/CRLF 转换提示）。
- 重新解析 OpenAPI YAML 并核对其中 130 个 `$ref` 的相对目标：通过，缺失 0 个。
- 解析本次 `api/`、`doc/` 非归档文档中的 29 个 JSON/JSON-lines fenced 示例：通过；
  示例中未出现旧四字段作为 JSON 属性。
- 对当前 `api/` 与 `doc/` 非归档权威说明执行旧字段引用核对：仅保留迁移/拒绝旧 JSON 的
  明确说明；当前示例不再把 Descriptor 写入场景，也不再使用 `descriptorSource` 选择来源。
- 主代理在显式 Python 3.14.2 环境执行 `go test ./... -count=1` 与 `go vet ./...`：
  均通过，覆盖 Go Provider、Python Worker、Simulator 和 HTTP 适配的最终迁移实现；新增
  Provider handshake 回归用例也通过。
- 主代理执行 `go run ./cmd/app` 进行真实 Go 示例运行：输出 `match1/2` 且
  `produced=2`，确认 Go Provider 新自描述路径可运行。
- 前端代理在本次迁移快照执行 `npm.cmd --prefix apps/web test`：18 个文件、109 项测试通过；
  执行 `npm.cmd --prefix apps/web run build` 和 diff 检查也通过，仅有既有 Vite chunk 体积提示。
  测试覆盖三个旧场景 Descriptor 字段及 `descriptorSource` 的类型/草稿/序列化/UI 移除，
  四种旧字段导入均明确拒绝且不发 API；mock 能力独立于 Contract，运行态来源覆盖 `go`/`python`。
  本轮没有启动临时 API 或执行浏览器截图验证；历史基线中的浏览器截图不作为本轮结果。

## 2026-09-10 Go 内建 Provider 的 Python 参考实现

当前工作区新增 [examples/simulator/builtin_provider.py](../../../examples/simulator/builtin_provider.py)，
其固定公开 `waitingCount`、`preferredRoles`、`latencyMs`、`waitingTime`、`memberCount`，并使用
握手启动 Input 中的 `ContractFacts`/`TickFacts` 和业务 `ObjectFacts` 做响应投影；没有从 Contract
生成 Descriptor。以下结果针对本次提交：

- 主代理在显式 Python 3.14 环境执行 `go test ./internal/simulator -count=1`：通过，新增
  Python 参考实现对齐测试已编译并执行；行为覆盖细节以该测试源码为准。
- 主代理随后在当前工作区显式 Python 3.14 环境执行 `go test ./... -count=1` 与
  `go vet ./...`：均通过；全仓结果包含上述 Python 参考实现测试。
- 本轮使用 Python 3.14 对参考脚本执行 AST 解析，解析全部 `api/` JSON 和 OpenAPI YAML；遍历
  非归档 `doc/` Markdown 的 399 个本地链接，缺失 0 个；解析 31 个 JSON fenced 示例和 1 个
  JSON-lines fenced 示例，均通过。未启动临时 API，也未执行浏览器截图验证。

## 历史基线：2026-09-09 Descriptor 扩展实际检查

- 显式设置 `PYTHON_PROVIDER_EXECUTABLE` 后执行
  `go test ./internal/simulator ./internal/simulatorapi ./experiments/pythonprovider -count=1`：
  三个包通过。另执行 `go test ./internal/simulator -run 'PythonProvider' -count=1 -v`，其中
  脚本 Descriptor 三范围加载、非法 `describe()`、重载更新、失败保留旧 runtime、超时回收
  等用例通过。
- 后端补齐来源解析错误包装和显式 Scenario Descriptor 静态校验后，重跑上述三包命令，仍全部通过；另以
  `go test ./internal/simulator -run 'TestValidateScenarioChecksExplicitPythonDescriptorsWithoutStartingWorker|TestValidateScenarioDefersScriptDescriptorsUntilRuntime|TestScenarioPythonProviderHandshakeFailureKeepsOldRuntime|TestScenarioPythonProviderScriptDescriptorsDriveAllScopes' -count=1 -v`
  检查静态 422、脚本延后握手、失败保留旧 runtime 和三范围加载，均通过。
- 用 PowerShell `ConvertFrom-Json` 解析两份 Simulator Schema，并用 Python 3.14.2 的
  PyYAML `safe_load` 解析 `api/openapi/simulator.yaml`：通过。解析结果包含
  `pythonProvider.descriptorSource`、Fact Descriptor 的 `source` 和外部引用所需结构。
- 启动临时 `go run ./cmd/simulator-api --addr 127.0.0.1:18083`，用仓库外临时 Python
  脚本导出 `describe()` 的 Tick/Object/Match 三类 Descriptor，删除 GET 场景中的三个
  显式 Descriptor 后执行真实 `PUT /api/v1/scenario`：响应成功；随后 `GET /api/v1/scenario`
  仍保留 `pythonProvider` 且没有回填 `factProviderDescriptor`，`GET
  /api/v1/logical-nodes/facts?ruleNamespace=demo&ruleId=1&placementId=default` 返回三类
  `source=script` 和 `id=http.script`。临时脚本与服务已停止并清理。
- 同一真实服务随后执行 `POST /api/v1/tickets/custom`（accepted=2）和
  `POST /api/v1/rounds`（produced=2），`GET /api/v1/matches?limit=10` 观察到 Python
  Object Facts `preferredRoles=["solo"]`、`latencyMs=25` 与 Match Fact `memberCount=1`。

最终后端执行 `go test ./... -count=1` 与 `go vet ./...`，均通过；显式配置 Python 3.14.2，关键 Python/HTTP 测试实际执行，未跳过。新增 HTTP 回归覆盖 `PYTHON_PROVIDER_FAILED`、握手诊断、失败保留旧场景、Ready 后候选进程回收，以及显式 Descriptor 静态错误返回 422。

脚本外部 Descriptor 校验按设计延后到 runtime；Scenario 模式及 Auto 带显式声明时，已知 Descriptor 覆盖错误仍在静态校验中返回 422。依赖 Worker 握手的来源、描述和 Contract 错误返回结构化的 `PYTHON_PROVIDER_FAILED`，并保留旧 runtime。

前端最终执行 `npm.cmd --prefix apps/web test`（18 文件、108 项）及 `npm.cmd --prefix apps/web run build`，均通过，仅有既有 Vite chunk 体积提示。新增来源区域中文化后再次构建通过。

真实浏览器验证脚本模式保存、GET 回读和刷新：三类运行态描述均为 `source=script`，场景中的三个显式 Descriptor 字段均不存在；来源草稿切换的编辑权限、Ready-only 旧场景展示及握手错误诊断正常。新启用默认 script 和双节点隔离通过代码复核，未单独执行新启用点击或双节点浏览器测试。代码复核同时确认运行态描述优先、完整 LogicalNode 身份隔离及 Auto 冲突处理。

本轮浏览器截图在当时的 Windows Temp 目录生成：`descriptor-e2e-script-after-save-20260909.png`、`descriptor-e2e-legacy-facts-20260909.png`、`descriptor-e2e-error-diagnostic-20260909.png`。截图已查看，不纳入 Git；API、Vite、Edge CDP、临时 profile、脚本及日志均已停止或清理。

## 历史基线：契约检查

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

## 历史基线：Go 与真实 HTTP

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

前端契约同步由 `npm.cmd --prefix apps/web test`（18 个文件、108 项）和
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

## 历史基线：边界

Python 启动/握手成本发生在 runtime 构造阶段，不计入成功成局的 `processingDurationNs`；
`ProduceMatch` 内的 RPC 等待会计入该字段。fixed 的 `virtualNow` 不因 Python、核心匹配或
Go 调度墙钟耗时而推进，但这些成本会增加 `wallElapsedMs` 并消耗 CPU、内存、解释器和管道
容量；realtime 还会体现在 `lagMs` 和后续到期工作上。大规模模拟仍需另测 Worker 数量、候选
规模、脚本执行时间、并发部署和 realtime 调度延迟。

`git diff --check`：通过。上述检查针对当前共享工作区快照；若后续代码再次改变，应重跑受影响
的契约和 HTTP 验收。

## 历史基线：前端检查记录
以下包含早期前端代码和浏览器检查快照以及本次 Descriptor 扩展的实测；未覆盖的浏览器事项仅作代码复核，不在此处预先宣告完成。
此前一次前端代码审计记录了以下五项检查（不代表当前 Descriptor 扩展客户端闭环已最终验收）：

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

早期浏览器验收时生成过 `web-python-binding-real.png`、`web-fixed-complete-real.png` 和 `web-fixed-failed-real.png` 等本地附件；这些附件按仓库约定不纳入 Git，本记录以实际命令和 API 回应为证据。

此前一次真实浏览器验收连接 API `127.0.0.1:18080` 完成：启用 Provider 和三个 scope 后，GET
回读确认 binding 与三个 Descriptor 均存在；关闭 Tick 后 GET 回读为 Tick=false、Object/Match=true，
刷新页面后仍保持关闭；再次开启 Tick 后三个 scope 均为 true；关闭 binding 后 GET 不包含
`pythonProvider`，刷新后仍保持关闭。示例已有 Object Facts 时关闭 Object scope 被服务端以
`MISSING_DESCRIPTOR` 拒绝，属于保持 Contract/Descriptor 一致性的正常契约校验，不计为 UI 缺陷。
本次 Descriptor 扩展的实际浏览器检查覆盖：通过真实 API 保存 `script` 来源后回读、刷新页面，三个 scope 均显示 `source=script`，且场景中三个 Descriptor 字段均不存在；来源草稿切换权限与错误 `diagnostic` 展示也已实际检查。
新启用复选框默认来源与双节点隔离未单独用浏览器实测，当前仅有代码复核；本次实测使用的 API、Vite、Edge CDP、临时 profile、脚本和日志均已停止或清理。
API、Vite、Edge 已停止，`18080`、`15176`、`9239` 无监听，临时脚本和 profile 已清理。
上述是早期快照的实际证据；本次 Descriptor 扩展的客户端闭环仍待主代理以最终实证复核。

上述历史快照中的“场景 Descriptor”“Ready-only”“`descriptorSource`”以及
`source=script/scenario` 均属于旧基线。统一 Provider 自描述契约生效后，Go 使用进程内
`Describe()`，Python 必须通过 JSON-lines `describe()` 握手，运行态来源只有
`source=go/python`；任何旧字段的迁移验证必须重新执行，不能沿用本节的旧结果。
