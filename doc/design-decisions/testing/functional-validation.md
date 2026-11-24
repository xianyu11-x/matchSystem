# 功能验证矩阵

本文把关键设计约束映射到可执行测试。它记录验证入口，不复制测试实现；新增行为时应在
同一行补充测试文件或增加新的行为域。

## 核心匹配

### 固定回放消息与外部日志兼容

2026-11-24，基于未提交工作区，在 Windows x64 验证最终便携包及其 sidecar。实际运行产生的
`matchscope-20261124-195538-390360005.log` 共 28 行、11,877 字节，其中 24 条是以
`[match_replay] ` 开头的回放记录，包含 `timelineFacts`。在最终 ZIP 的 sidecar 上分别导入
原始日志、自定义前缀、标准 `msg` 外层、JSON 的 `message` 外层以及带引号和不带引号的
`msg=` 文本，提取出的时间线逐项一致；旧版 `.jsonl` 也能识别。以 FIFO 规则 `91002`
回放时，Ticket 1–8 在源第二个时点 `1770000000080` 成一局。

`go test ./... -count=1`、`go vet ./...`、`git diff --check` 通过；Web 类型检查和全量
测试通过，共 48 个文件、301 项。专项脚本
`apps/desktop/scripts/test-desktop-logs.ps1 -PortableZip dist/release-2026-11-24-replay-tag/MatchScope-0.1.0-windows-x64.zip`
验证了两次独立日志、磁盘标记和 JSON 载荷、磁盘时间线提取，以及含中文和空格的路径；
但整脚本未通过：最后的启动失败对话框在隐藏启动下存在但不可见，等待可见窗口超时，
因此本次未验证该错误提示的用户可见性和对话框诊断文本。日志格式边界见
[外部日志格式](../../simulator/timeline-replay.md#外部日志格式)，最终包见
[Windows 发布打包验收](client-release-packaging.md#2026-11-24-固定回放消息-windows-x64-本地构建)。

### 横向事件时间轴

本节记录范围选区替换之前的时间轴版本；当前交互及验证见[时间轴范围选择与缩放](#时间轴范围选择与缩放)。

2026-11-24，基于未提交工作区，在 Windows x64、Edge 157.0.4341.42 的真实浏览器中验证
生产构建的 Web 资源；使用隔离的静态服务与旧标记日志版本的 Go sidecar，上传
`fifo-replay-example.log` 并从规则库选择八票规则 `91002`。样例提取出 14 条源事件，候选
回放为 2 轮、3 次调用、1 次成局，成员为 Ticket 1–8。

- 在 1440 像素视口下，时间轴由 1,029 像素放大到 8,304 像素；横向滚轮移动 600 像素，
  重置后恢复 100%，首尾标记未被裁切。
- 同一时点 `+61.472 ms` 的序号 10、11 聚合后可逐条查看；使用「第 14 条」跳转到
  候选成局详情，确认候选成员 1–8、源调用成员 5–8。首次提取与切换实例后详情为空，
  切换实例同时清除旧回放结果。
- 390 像素宽视口下页面没有横向溢出，时间轴在自身区域内滚动；浏览器未报告运行异常。
  空事件或无有效偏移后载入有效事件，窄屏时间轴和切回桌面后的宽度均正确。

发布构建时 Web 全量测试共 47 个文件、292 项通过；补充缩放锚点测试后重跑为 293 项通过。
组件复用与尺寸监听回归见
[时间轴交互测试](../../../apps/web/src/components/ReplayTimelineView.interaction.test.ts)，
交互方式见[使用指南](../../simulator/timeline-replay.md#阅读事件时间轴)。

### 时间轴范围选择与缩放

2026-11-24，基于未提交工作区，在 Windows x64 的 Edge 157.0.4341.42 中使用 DevTools
Protocol（浏览器调试协议）验证生产构建的 Web 资源。隔离静态服务连接标记日志版本的 Go
sidecar，上传 `fifo-replay-example.log` 并选择八票规则 `91002`：提取 14 条源事件，回放
结果为 2 轮、3 次调用和 1 次成局。

- 分别对源时间轴和回放结果实际拖动左右手柄及选区内部，确认上方时间轴同步更新；拖动选区
  到完整范围两端时停止于边界且跨度不变。源时间轴的下方抓手也能平移，操作回放结果不会
  改变源时间轴的范围。
- 两个控件均验证「显示全部时间」恢复全域、方向键调整端点及平移。390 像素宽视口中，页面
  宽度为 390 像素，缩略控件位于视口内且仍可拖动；浏览器未报告运行异常。
- 回放结果截图选中 `+53.48079 ms` 至 `+61.610305 ms`，可同时查看两端手柄、事件密度和
  上方事件标记。发布脚本执行 Web 类型检查和 48 个文件共 301 项测试，均通过。回归测试见
  [时间轴交互测试](../../../apps/web/src/components/ReplayTimelineView.interaction.test.ts)；
  最终包和实际桌面启动验证见[Windows 发布打包验收](client-release-packaging.md#2026-11-24-时间范围选区-windows-x64-本地构建)。

### 带标记的文本日志回放

以下记录针对固定消息正文改造前的版本，保留为历史验证；当前版本见[固定回放消息验证](#固定回放消息与外部日志兼容)。

2026-11-24，基于未提交工作区，Windows x64 便携版实际验证了标记文本日志的导入与回放。
从新发布目录启动 `portable/MatchScope.exe`，使用其实际 sidecar 上传发布目录中独立提供的
`fifo-replay-example.log`（12,157 字节、29 行，含未标记中文业务行以及完整时间线和 Fact
记录）。实例列表与源 JSONL 一致；两个可回放实例分别提取后，逐项比较所有字段、数组顺序和
精确整数，完整时间线均与源记录一致。选用八票 FIFO 规则 `91002` 后，Ticket 1–8 成一局。

在文本日志中加入损坏的标记事件后，API 正确报告第 2 行；桌面窗口验证后正常关闭。格式及
兼容范围见[外部日志格式](../../simulator/timeline-replay.md#外部日志格式)，最终包及其校验
见 [Windows 发布打包验收](client-release-packaging.md#2026-11-24-带标记文本日志的-windows-x64-本地构建)。
解析边界回归用例见[文本日志提取测试](../../../internal/simulator/replay_extract_text_test.go)，
核心标记生成见[时间线测试](../../../pkg/matchsystem/timeline_test.go)。

### 模拟器日志持久化

以下记录针对使用 `.jsonl` 文件的旧版实现；当前 `.log` 格式的验证见[固定回放消息验证](#固定回放消息与外部日志兼容)。

2026-11-24，基于未提交工作区，在 Windows 上使用独立模拟器 API 验证默认日志及跨进程回放。
在隔离的工作目录两次启动 API，只使用 `--addr` 指定动态端口，不传 `--log-dir`：两次运行
在工作目录的 `Logs/` 下生成了两个不同的文件，第一份日志共 28 行。

将第一份 JSONL 上传给第二次启动的服务，从中提取的时间线与第一次服务内存中的时间线
逐项一致。使用八票 FIFO 规则 `91002` 回放，在源记录的第二个时点 `1770000000080` 将
Ticket 1–8 匹配成一局。该验证覆盖了 Web 和桌面共用的 HTTP 日志提取与回放接口；桌面包
实际路径验证见[Windows 发布打包验收](client-release-packaging.md)。

另以最终 Windows x64 发布目录中的 `portable/simulator-api.exe` 重跑跨进程回放，得到相同
结果。使用含中文和空格的 `--log-dir` 启动该后端，产生的 JSONL 在运行期间可按共享读取，
上传后能提取含 `round_begin` 的时间线，启动工作目录没有生成额外的 `Logs/`。

直接启动最终发布目录中的 `portable/MatchScope.exe` 后，真实 sidecar 在程序同级生成
`Logs/matchscope-20261124-172516-3376822877.jsonl`（28 行，13,518 字节）。原四人规则
在两轮各成一局；上传同一日志，用八票规则在原第二轮将 8 张票成一局。正常关闭客户端后
日志仍保留在该目录。此日志为本次运行数据，不在发布 ZIP 内。

### 逻辑节点时间线与规则替换

2026-11-24，未提交工作区，Windows、Go 1.24.6、Node 24.16.0。用本地模拟器 API、Web 页面和 CLI 执行了[规则替换流程](../../simulator/timeline-replay.md)。

- 源规则由 FIFO 四人样例创建，向同一部署加入 3 张 Ticket，执行一轮匹配，再主动撤回 Ticket 3。导出的时间线共 7 条记录，含源实例流头、3 次入池、轮次开始、1 次匹配调用和撤票。
- 源规则尝试了 3 个种子，未成局。两人候选规则在同一次匹配调用、同一轮次 `now` 下尝试 1 个种子，返回成员 1、2。对比回放前后源队列，票据列表完全一致。
- 上传同一组打乱顺序且含重复行的 JSON Lines，提取结果与模拟器内录时间线一致。用 CLI 导出后再次提交到 HTTP 后端，也得到成员 1、2。
- 分别选用新增 `rank` 属性及新增未声明 Fact 的候选规则。服务端在执行前返回 HTTP 422，对应 `ATTRIBUTE_CONTRACT_MISMATCH`、`FACT_CONTRACT_MISMATCH`，浏览器显示了错误字段。

已通过 `go test ./... -count=1`、`go vet ./...`、`go build ./...`、`scripts/check-go-consumer.ps1`、Web 测试及生产构建。Vite 仍提示主包超过 500 kB。上述真实服务验收使用内置 Provider。回归用例见[模拟器回放测试](../../../internal/simulator/replay_test.go)、[核心录制测试](../../../pkg/matchsystem/timeline_test.go)及[HTTP 测试](../../../internal/simulatorapi/replay_test.go)。

额外的 Go `-race` 检测未能运行。当前 Windows 环境默认禁用 CGO，已安装的 MSVC 目标 clang 不支持 Go 所需的 `-mthreads` 选项。

### 八票 FIFO 示例规则

2026-11-24，未提交工作区，Windows、Go 1.24.6。验收文件为
[`fifo-8-match-rule.json`](../../../examples/simulator/fifo-8-match-rule.json)，RuleID `91002`；
Go `CompileRuleJSON` 完整规则指纹为
`baa0c14c4918bd3e47055d01ce4f77095c85b5717b3389c3872ad6f8e674107d`。

- 临时 Go 验收程序加载该文件并提供 `memberCount` Match Provider：7 张票据不成局且全部保留，补第 8 张后按创建时间取 1–8；17 张票据按创建时间倒序入池后，两局依次取 1–8、9–16，票据 17 保留。
- 508 张票据的边界实验中，创建时间较晚的 500 张先入池，随后入池 7 张更早的候选；评分池按 DocID 截断后，较早的 7 张被跳过。另用 `2^53` 与 `2^53+1` 验证评分转换为 `float64` 后可能并分；同时间候选的实验中，DocID 复用使后入池票据排在先入池票据之前。这些结果是示例继承的限制，不是严格 FIFO 保证。
- 模拟器内置 `memberCount` Provider 的单元测试通过：`go test ./internal/simulator -run '^TestDefaultMatchFactProviderComputesMemberCount$' -count=1`。使用从 ZIP 提取的 sidecar 联测四票源规则的两个轮次：先投 7 张、再投 1 张，源规则各成一局；八票候选规则回放在第二轮将 1–8 成为一局，状态为 `completed`。
- 仓库 5 份关联 JSON Schema 使用 AJV 8.20.0 的 2020-12 校验器注册并校验通过。技能自带的轻量校验器因未识别当前 `lookup_nearest` 算子而返回 `valid: false`；全局技能版本还不识别字符串形式的 RuleID。轻量校验器结果不能代替上述 Schema 和运行时验证。

| 行为域 | 主要保证 | 证据入口 |
| --- | --- | --- |
| RuleJSON 编译 | 严格结构、版本、fingerprint、算法参数和运行预算 | [`rule_config_test.go`](../../../pkg/matchsystem/rule_config_test.go) |
| LogicalNode 生命周期 | 创建、状态转换、Fact 元数据副本、候选上限 | [`logical_node_round_test.go`](../../../pkg/matchsystem/logical_node_round_test.go)、[`candidate_limit_test.go`](../../../pkg/matchsystem/candidate_limit_test.go) |
| 物理节点路由快照 | 部署顺序、等待 Ticket 数、排空和停止、快照所有权 | [`physical_node_route_snapshot_test.go`](../../../pkg/matchsystem/physical_node_route_snapshot_test.go) |
| 匹配流水线 | 固定阶段、成功提交、metrics 聚合、Provider 输入所有权 | [`logical_node_produce_match_test.go`](../../../pkg/matchsystem/logical_node_produce_match_test.go) |
| Ticket store | DocID、增删、Object slot、提交预检和原子性 | [`ticket_store_test.go`](../../../pkg/matchsystem/ticket_store_test.go) |
| Seed 策略 | arrival/oldest/priority/random、轮内不重复、下一轮恢复 | [`seed_order_test.go`](../../../pkg/matchsystem/seed_order_test.go) |
| 候选排序 | Top-L、评分上限、稳定 tie-break、错误/取消 | [`candidate_ranking_test.go`](../../../pkg/matchsystem/candidate_ranking_test.go)、[`candidate_ranking_fastpath_test.go`](../../../pkg/matchsystem/candidate_ranking_fastpath_test.go) |
| 完整规则算法 | scoring、seedSelection 与 RuleKey 绑定 | [`rule_algorithm_integration_test.go`](../../../pkg/matchsystem/rule_algorithm_integration_test.go) |

## Client Router

| 行为域 | 主要保证 | 证据入口 |
| --- | --- | --- |
| 路由与选择策略 | 共享规则键、数值部署 ID、亲和键回退、固定哈希向量、候选快照、权重与顺序、选择结果校验及 Owner 解析 | [`router_test.go`](../../../pkg/client/router_test.go)、[`route_selector_test.go`](../../../pkg/client/route_selector_test.go) |
| 队列选路 | 随机权重区间、阈值切换、同步后恢复、全满回退、等值决胜和重复选路时的队列数 | [`queue_route_selector_test.go`](../../../pkg/client/queue_route_selector_test.go) |
| 物理节点级路由更新 | 新增与完整替换、幂等下线、重新加入顺序、Owner 解析、失败保留旧表及共享表隔离 | [`router_update_test.go`](../../../pkg/client/router_update_test.go) |
| 共享身份与模拟器路由 | `uint64` 身份通过场景和路由往返，完整 Owner 解析、部署唯一性、正十进制亲和键 | [`route_table_test.go`](../../../internal/simulator/route_table_test.go)、[`scenario_json_test.go`](../../../internal/simulator/scenario_json_test.go) |

## 外部 Go 消费者

| 行为域 | 主要保证 | 证据入口 |
| --- | --- | --- |
| 独立模块接入 | 快照发布和 OwnerRef、两 Ticket 匹配与 Match Fact、提交后消费、跨轮次不重复及不同 partition 不匹配 | [消费者测试](../../../examples/go-consumer/main_test.go)、[隔离验证脚本](../../../scripts/check-go-consumer.ps1) |

## 子包契约

| 包 | 主要保证 | 证据入口 |
| --- | --- | --- |
| `contract` | Fact 描述、类型、限制和严格解析 | [`contract_description_test.go`](../../../pkg/matchsystem/contract/contract_description_test.go) |
| `evaluation` | trusted snapshot、`CanJoin`/`CanComplete` 求值 | [`predicates_test.go`](../../../pkg/matchsystem/evaluation/predicates_test.go) |
| `fact` | Frame、Writer、Object slot 缓存、Validator 生命周期 | [`frame_test.go`](../../../pkg/matchsystem/fact/frame_test.go) |
| `prefilter` | Store/TickSession、动态查询、范围索引和可信 Fact | [`store_test.go`](../../../pkg/matchsystem/prefilter/store_test.go)、[`int64_range_index_test.go`](../../../pkg/matchsystem/prefilter/int64_range_index_test.go) |
| Prefilter 最近值 | 方向、不同值计数、同值票据、Seed 排除、`int64` 极值、AND 分层及分支组合 | [预筛单元测试](../../../pkg/matchsystem/prefilter/lookup_nearest_test.go)、[RuleJSON 集成测试](../../../pkg/matchsystem/lookup_nearest_integration_test.go)、[模拟器端到端测试](../../../internal/simulator/service_test.go) |

`expression` 与 `jsonstrict` 的行为还会被 Contract、Prefilter、Evaluation 和 RuleJSON 的
集成测试覆盖；修改其公共错误或节点语义时，应补充对应包的直接测试，而不是只依赖上层。

## Fact 与 Provider

| 行为域 | 主要保证 | 证据入口 |
| --- | --- | --- |
| Provider 握手 | Contract Facts 是 Descriptor Facts 的子集；Provider ID/version 合法，使用项的 name、type、scope、`MaxValues` 严格对齐，额外合法 Fact 可保留 | [`provider_descriptor_test.go`](../../../pkg/matchsystem/provider_descriptor_test.go) |
| 可信快照 | Provider 值沿固定流程传递且不被运行时重复校验 | [`trusted_fact_flow_test.go`](../../../pkg/matchsystem/trusted_fact_flow_test.go) |
| Object Fact | refresh/cache/writer 错误 metrics 与生命周期 | [`object_fact_flow_test.go`](../../../pkg/matchsystem/object_fact_flow_test.go) |
| Match Fact | 完整快照模式、clone 与返回结果 | [`logical_node_produce_match_test.go`](../../../pkg/matchsystem/logical_node_produce_match_test.go) |

## 模拟器与 API

| 行为域 | 主要保证 | 证据入口 |
| --- | --- | --- |
| 场景 JSON | wire 名称、默认值、往返与空场景 | [`scenario_json_test.go`](../../../internal/simulator/scenario_json_test.go) |
| 场景替换与运行时 | 原子替换、批量回滚、多节点运行、查询快照 | [`service_test.go`](../../../internal/simulator/service_test.go) |
| 运行时物理节点 | 增删与路由发布、等待队列和历史隔离、失败回滚、持续流量、Provider 生命周期及界面 | [应用测试](../../../internal/simulator/physical_nodes_test.go)、[HTTP 测试](../../../internal/simulatorapi/physical_nodes_test.go)、[Web 测试](../../../apps/web/src/components/PhysicalNodeManager.test.ts) |
| 路由同步与配置 | 节点独立同步周期、队列阈值切换与恢复、定时器清理、运行态算法切换及旧场景兼容 | [运行时测试](../../../internal/simulator/routing_test.go)、[配置测试](../../../internal/simulator/routing_config_test.go)、[HTTP 测试](../../../internal/simulatorapi/routing_test.go)、[场景 Schema 测试](../../../apps/web/src/lib/scenarioSchema.test.ts) |
| 独立规则库与节点部署 | 规则保存隔离、修订号冲突、引用式部署、逐节点更新与队列保留、精确 Tick Facts | [应用测试](../../../internal/simulator/rule_library_test.go)、[核心配置转移测试](../../../pkg/matchsystem/physical_node_reconfigure_test.go)、[HTTP 测试](../../../internal/simulatorapi/rule_library_test.go)、[规则库前端测试](../../../apps/web/src/lib/ruleLibrary.test.ts)、[部署前端测试](../../../apps/web/src/lib/physicalNodes.test.ts) |
| Ticket 投递目标 | 按规则加权或按部署直投、身份精度、目标失效和定向流量绑定 | [选择测试](../../../apps/web/src/lib/ticketTargets.test.ts)、[流量测试](../../../internal/simulator/traffic_target_test.go)、[HTTP 测试](../../../internal/simulatorapi/traffic_target_test.go) |
| 物理节点队列清空 | 核心与观测同步、其他队列与历史隔离、取消边界、持续流量及确认提示 | [核心测试](../../../pkg/matchsystem/physical_node_clear_test.go)、[应用测试](../../../internal/simulator/physical_node_clear_test.go)、[HTTP 测试](../../../internal/simulatorapi/physical_node_clear_test.go) |
| Ticket 生成草稿 | 本地版本化恢复、空数值保真、异常数据与不可用存储回退 | [草稿测试](../../../apps/web/src/lib/ticketComposerDraft.test.ts) |
| Match 历史 | 顺序、保留上限、详情和独立副本 | [`match_history_test.go`](../../../internal/simulator/match_history_test.go) |
| HTTP 契约 | 路由、JSON wire、错误、SSE、分页和能力列表 | [`handler_test.go`](../../../internal/simulatorapi/handler_test.go) |
| Fact 查询 | 元数据分层、not found/ambiguous/legacy 边界 | [`logical_node_facts_test.go`](../../../internal/simulatorapi/logical_node_facts_test.go) |
| API 启动握手 | 动态端口 ready JSON | [`main_test.go`](../../../cmd/simulator-api/main_test.go) |
| Web 数据适配 | API、能力、Fact、规则 IO、图编辑和分析 | [`apps/web/src/lib`](../../../apps/web/src/lib) 中的 `*.test.ts` |

## 建议执行顺序

```powershell
# Go 单元与集成测试
go test ./... -count=1

# 独立 Go 消费者模块（根模块 ./... 不包含此嵌套模块）
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-go-consumer.ps1

# 包依赖约束
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-expression-deps.ps1

# Web 类型与行为
npm --prefix apps/web run typecheck
npm --prefix apps/web test -- --run

# Desktop 配置契约（不要求执行 Rust/UE 编译）
npm --prefix apps/desktop run check:config
```

性能数据不属于功能通过条件，单独见[性能基准](performance-benchmark.md)；发布和回滚检查
见[发布验证](release-validation.md)。

## 2026-11-24 Prefilter 最近值验证

环境为 Windows amd64、Go 1.24.6、Node.js 24.16.0、Python 3.14.2，基于 `6e9ed0a` 的未提交改动。

- 配置真实 Python 解释器后，`go test ./... -count=1`、`go vet ./...` 和 `scripts/check-expression-deps.ps1` 通过。
- `npm.cmd --prefix apps/web test` 的 263 项测试和 `npm.cmd --prefix apps/web run build` 通过。构建输出包含 Vite 资源体积提示。
- 使用仓库 Draft 2020-12 Schema 校验 `dist/fifo-match-rule.json`，并用本地模拟器的规则校验、规则库保存和场景部署入口验证。少于 4 张票时未成组，乱序投递后产生两组，组内入队时间偏移依次为 `[0, 0, 1, 1]` 和 `[2, 2, 3, 3]`，剩余 1 张继续等待。
- 模拟器返回的规则指纹为 `28bb7bbb9430bef83b9ce5d00ecd122cd2def3e646bc07c62359e06ce7c9a8e6`。`create-match-rule-json` 技能自带的旧检查器只接受数字形式的 RuleID，且尚未识别 `lookup_nearest`，本次以仓库 Schema 和模拟器编译结果作为格式与集成证据。

## 2026-11-24 内建创建时间验证

基于 `90b6f83` 的未提交修改，环境为 Windows amd64、Go 1.24.6、Python 3.14.2。

- `go test ./... -count=1`、`go vet ./...`、公开包独立消费者脚本与表达式依赖检查通过。
- [Contract 测试](../../../pkg/matchsystem/contract/builtin_attribute_test.go)验证目录、保留名和只读输入；
  [Evaluation 测试](../../../pkg/matchsystem/evaluation/builtin_attribute_test.go)和
  [Prefilter 测试](../../../pkg/matchsystem/prefilter/builtin_attribute_test.go)覆盖直接使用创建时间及计划依赖。
  [核心集成测试](../../../pkg/matchsystem/builtin_attribute_integration_test.go)验证只设置 `CreatedAt` 的
  Ticket 配合内建索引、两种字段排序与评分路径完成成组。
- Web 侧 `npm.cmd --prefix apps/web test` 通过 269 项测试，生产构建通过。
  [内建属性测试](../../../apps/web/src/lib/builtinAttributes.test.ts)覆盖只读选项和规则往返，
  [规则页面交互测试](../../../apps/web/src/pages/Rules.export.interaction.test.ts)覆盖导入时等待能力目录。
- [FIFO 规则](../../../examples/simulator/fifo-match-rule.json)通过仓库 Draft 2020-12 Schema。
  通过真实 HTTP API 查询到只读能力，拒绝同名声明，并以 `createdAt` 作为唯一时间字段
  完成规则验证、保存和部署。乱序投递后生成两组，四人一组，剩余一张继续等待。
  后端返回规则指纹 `076a1a560401643cbde56c3b8d0e7cf8815c7c450b80545f44b966625d665ec9`。
  本地旧格式检查器仍报告数字字符串 RuleID 和 `lookup_nearest` 未受支持，格式以仓库 Schema
  与真实服务端编译结果为准。

## 2026-11-24 物理节点匹配配额验证

基于 `90b6f83` 的未提交改动，环境为 Windows amd64、Go 1.24.6、Node.js 24.16.0、Python 3.14.2。

- `go test ./... -count=1`、`go vet ./...` 和 275 项 Web 测试通过。实时流量相关
  Go 回归重复执行 5 次均通过。
- [手动轮次测试](../../../internal/simulator/per_node_match_limit_test.go)覆盖物理节点独立计数、
  逻辑部署共用配额及轮询顺序。[流量测试](../../../internal/simulator/traffic_match_limit_test.go)覆盖
  两种时钟模式的正上限和只注入。[HTTP 测试](../../../internal/simulatorapi/match_limit_test.go)验证
  手动兼容参数及流量的显式 0。
- 在本地真实 HTTP 服务中配置三个物理节点，第一个节点有两个逻辑部署。手动轮次
  配额为 100 时按节点分别产出 100、100、3 局，全部属于同一轮。下一轮消费剩余票据。
- 正配额固定流量中，两个物理节点各产出 2 局。固定与实时流量使用 `maxMatches: 0`
  时继续注入，两种模式的匹配轮数与产出都为 0。缺省、`null`、负数和超范围输入均被拒绝。
- [Web 配置交互测试](../../../apps/web/src/components/TrafficControl.interaction.test.ts)覆盖
  显式 0、清空和非法值，HTTP 与草稿测试覆盖跨节点总数及显式 0 的保留。
- 使用 Draft 2020-12 Schema 验证实时和固定配置，接受显式 0..10000，拒绝缺省和非法值。
