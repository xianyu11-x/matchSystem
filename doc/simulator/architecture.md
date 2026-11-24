# 匹配模拟器架构

## 1. 目标与边界

匹配模拟器是现有进程内匹配核心的独立宿主和可视化客户端。它负责场景配置、
多 PhysicalNode（物理节点）编排、Ticket/Fact 观测、批量数据生成、HTTP API 和运行事件；
`matchsystem` 仍只负责 LogicalNode（逻辑节点）内的索引、候选选择、规则求值和
匹配提交。

依赖方向固定为：

```text
apps/web -> HTTP/SSE -> simulator API -> simulator application -> matchsystem
                                                              -> client.Router
```

`matchsystem` 不导入 simulator、HTTP 或前端类型。前端也不直接复制 Go 内部结构，
只依赖版本化的 OpenAPI/JSON Schema。

## 2. 技术选型

模拟器从匹配内核的结构化日志构建逻辑节点时间线，回放任务另外创建候选规则的逻辑节点，使用录制的轮次时间和调用顺序。Web 与桌面端共享同一套提取和回放 HTTP 接口。[逻辑节点时间线](timeline-replay.md)

客户端采用 React 19 + TypeScript + Vite SPA：

- React Flow：规则树/图编辑、typed port 和连接预校验；
- JSON Schema 2020-12 + Ajv：跨语言结构校验；
- TanStack Query：服务端状态、分页和缓存失效；
- Zustand：规则编辑器的本地 document state；
- TanStack Table + TanStack Virtual：大批量 Ticket 的服务端分页和可视窗口；
- Apache ECharts：后续聚合指标、直方图和时间序列。

服务端使用 Go 标准库 HTTP。普通命令和查询使用 REST/JSON；只读运行事件使用 SSE。
前后端独立启动、构建和部署，Go 服务不嵌入前端静态文件。

`apps/web` 是唯一客户端源码，同时支持浏览器部署和桌面封装。Windows 桌面交付优先在
`apps/desktop` 使用 Tauri 2 shell，并把 `simulator-api.exe` 作为 sidecar；桌面端仍通过
同一套 HTTP/SSE API 通信，也可以改连远程服务。Wails v2 可作为纯 Go toolchain 的备选，
但不得使用业务 JS-to-Go binding 绕开 API 边界。

桌面 shell 拥有它启动的 sidecar 生命周期：启动时传入 `--addr 127.0.0.1:0` 和客户端可执行
文件同级的日志目录，读取 stdout 唯一一行 `ready` JSON 获得动态端口，等待 health endpoint
成功后再开放 UI 请求；正常关闭窗口或应用时，只终止本次持有 handle 的 child。错误信息写入
stderr，避免污染握手协议。独立 API 默认写入工作目录的 `Logs/`；磁盘日志与用于当前服务
提取的内存记录共存。Web 部署不启动 sidecar，而是从环境配置读取本地或远程 API base URL。
日志位置、写入失败和使用方法见[时间线录制](timeline-replay.md#自动记录与文件位置)。

场景与规则 JSON 由客户端保存到用户选择的本机路径。总览和规则配置共用前端保存逻辑。
Web 使用浏览器的 File System Access API（文件系统访问 API）。桌面端通过
`save_json_export` 命令使用 Tauri 原生保存对话框，并在桌面进程内写入用户确认的文件。
使用方式见[场景与规则导出](interface-guide.md#场景-json-导入与导出)。

## 3. 仓库结构

```text
api/
  openapi/simulator.yaml       # HTTP wire contract
  schema/                      # match-rule/v1 与嵌套 v3 section 的 JSON Schema
apps/
  web/                         # 独立前端应用
  desktop/                     # 可选 Tauri 2 Windows shell
cmd/
  app/                         # 保留现有内核示例
  simulator-api/               # 独立 HTTP 服务入口
pkg/
  matchsystem/                 # 公开匹配核心及子包
  client/                      # 公开路由计算，无网络传输
  common/                      # transport-neutral 核心值对象
  identity/                    # 稳定身份
internal/
  simulator/                   # 场景、运行时、生成器、观测模型
  simulatorapi/                # HTTP DTO、handler、SSE/CORS
```

## 4. 多物理节点运行时

一个 simulator process 可以创建多个 `matchsystem.PhysicalNode`。每个 PhysicalNode
由自己的 owner goroutine 和命令队列串行调用，保持核心的 non-goroutine-safe（非并发安全）
契约。不同 PhysicalNode 可以承载同一 RuleID，但必须使用不同 PlacementID；每个
LogicalNode 的 Ticket store、DocID、索引、Fact frame、seed round 和 evaluator 绝对隔离。

场景校验通过后，模拟器装载物理节点及逻辑部署，经各节点的 owner 协程读取
`PhysicalNode.RouteSnapshot()`，补充场景中的 Endpoint、启用开关和权重，再发布到
`client.Router`。运行时通过 Router 选路、解析 Owner 和发布部署更新。

新 Ticket 的流程为：

```text
Simulator.AddTicket
  -> client.Router.RouteNew
  -> 按 OwnerRef.PhysicalNodeID 选择节点 worker
  -> PhysicalNode.Add
  -> 登记观测记录并返回包含 RouteDecision 的 TicketView
```

观测 registry 以 `(OwnerRef, TicketID)` 为身份，不能只使用 TicketID。匹配成功后，
simulator 根据核心返回的成员移除等待记录并追加 immutable match event。

模拟器和 `client.Router` 共用 `uint64` 规则和部署身份。场景与 HTTP 把规则 ID、
物理节点 ID 和部署位置 ID 写为十进制字符串，浏览器可保留完整精度。`namespace`
字段是可选文本别名。Go
可以精确读取 JSON 整数。输入端解析身份，读取到的 `uint64` 进入共享
[`pkg/identity`](../../pkg/identity) 类型。

请求中的 AffinityKey 和 RequestID 使用规范十进制字符串。省略 AffinityKey 时 Router
使用 TicketID，加权 Rendezvous 算法用这个值选择目标。DecisionID 在 HTTP 中以十进制
字符串传输。[Client Router 路由与选择器](../match-system/client-router.md)

每个物理节点按配置的实际时间间隔重新发布完整路由快照，默认每 10 秒一次。模拟器运行时锁
串行化路由查询、配置变更和快照发布，节点 owner 协程串行读取自己的队列状态。节点撤下、
场景替换和实例关闭时取消对应同步任务。快照同步失败时仍使用最近成功发布的路由视图，下一次
定时同步会重试。通过算法设置接口切换时，模拟器用当前快照创建并原子切换到新 Router。
[路由配置](routing.md)

### 运行时管理物理节点

模拟器在同一把运行时锁内新增、编辑和删除单个 PhysicalNode。创建和编辑时可提交多条逻辑
部署，每条部署选择一个规则身份并单独设置 PlacementID、权重、启用状态、Tick Facts 和
Provider。新部署从规则库解析规则，已有部署保留已装载的规则快照，显式应用库版本时除外。
空部署列表表示待部署节点，节点在拓扑中可见。

编辑时先校验候选场景并准备路由、新增或变化的 LogicalNode 和 Provider。现有物理节点的
owner 协程通过 `PhysicalNode.Reconfigure` 直接接收准备好的逻辑节点，成功后发布路由、场景
和拓扑变更。编辑地址、启用状态、权重或调度器会保留规则及 Fact 输入未变化的逻辑节点。
模拟器清理被移除或重新装载的逻辑节点的等待 Ticket，并通过 SSE 发布 `physical_node_updated`。

清空队列通过现有物理节点的 owner 协程完成。应用层持运行时锁，先清核心队列和索引，再清对应的
等待观测，并通过 `physical_node_tickets_cleared` 事件发布清除数量。已完成 Match 保存在独立的历史中。

删除时通过空部署列表将节点从 Router 下线，同时移除该节点的运行时、规则部署和等待 Ticket，
并关闭对应的节点 worker 和 Python Provider。其他节点的等待队列、已完成 Match 历史、匹配轮次
和事件订阅均保留。持续流量仍按路由表处理后续 Ticket。若目标规则失去全部可用部署，下一次
注入会遇到 `ErrNoRoute` 并结束该次持续流量。SSE（服务端推送）发布
`physical_node_added` 和 `physical_node_removed`，删除事件还带有 `removedTickets`。

新装载的逻辑节点会加入当前已启动的轮次。新 Ticket 的可见性继续遵循核心的轮次约定，宿主可在下一轮
处理新增节点收到的 Ticket。界面入口见[运行时物理节点](interface-guide.md#运行时物理节点)。

## 5. 规则与合法性

当前生产配置是一份 `match-rule/v1` RuleJSON，顶层固定包含 `ruleKey`、`contract`、
`prefilter`、`evaluation`、`scoring`、`seedSelection` 和 `runtime`。其中 Contract、
Prefilter 和 Evaluation 的内部结构分别复用 `logical-node-contract/v3`、`prefilter/v3`
和 `evaluation/v3`；标量表达式继续使用 `expression-scalar/v3`。RuleJSON 的 fingerprint
覆盖 RuleID 及全部业务配置。

规则配置页通过独立规则库管理这些文档。库在模拟器服务内独立于运行场景保存，初次启动时
从初始场景取得规则。`GET /api/v1/rule-library` 返回十进制字符串 `revision` 和 `rules`，
`PUT` 以该修订号提交完整列表。服务端验证全部规则及 RuleID 唯一性，过期修订号返回 409。
规则库保存、导入和删除只修改库中的定义。已装载的规则内容可通过现有场景快照查询。
跨服务迁移使用规则库的 JSON 导出与导入。 [匹配规则库表单](rule-forms.md)

`scoring` 由内置类型目录驱动：`constant`、`created_at`、`int64_field`；`seedSelection`
支持 `arrival`、`oldest`、`int64_priority`、`random`。运行时通过同一文件配置候选上限、
最大组人数和两级 Seed 尝试上限。未知类型、未声明字段、非法参数和 RuleID 不匹配都会
在规则编译阶段拒绝。

前端节点 palette 只展示 simulator capability endpoint 返回的合法节点类型，并根据
Contract 限制 Attribute、Fact、source、index 和端口类型。连接时检查类型、基数和环；
保存前再调用服务端 validation endpoint。最终仍由 `CompileRuleJSON`/`NewLogicalNode`
复用严格 parser/compiler 验证未知字段、未知 op、非法引用、类型、scope、index 和复杂度
上限。JSON Schema 和前端校验只负责快速反馈，不能代替 Go 编译结果。

## 6. Ticket 与 Fact

核心 `common.Ticket` 不包含 DocID，API 也不暴露 DocID。API Ticket 同时携带：

- `ticketId`、`createdAt` 和三类 typed Attribute map；
- object-scope Fact snapshot（由 simulator provider registry 提供）；
- `RouteDecision/OwnerRef` 和等待/已匹配状态。

tick-scope Facts 可由宿主通过动态 Fact Provider 提供；match-scope Facts 由服务端配置的
reducer provider 产生完整 snapshot，匹配结果原样展示。Provider 实现不写入 RuleJSON，
场景的 `rules[*].pythonProvider` 保存服务端 Python Worker 的实现绑定。Go Provider 在
LogicalNode 加载阶段调用进程内 `Describe()`，Python Worker 在 JSON-lines 启动握手中返回
`Descriptors`；两者的有效能力只保存在运行态。所有提交输入先按 Contract 校验；非法
Attribute/Fact 名称或类型不会进入核心，Descriptor 额外能力也不会扩展业务响应允许的
Contract。Python Worker 按 LogicalNode 独立持有，替换成功或模拟器关闭时关闭；错误不会
静默回退。Fact 元数据接口报告有效 Descriptor 的 `source`（`go` 或 `python`）。
仓库提供与 Go 内建 Provider 对齐的 [Python 参考实现](../../examples/simulator/builtin_provider.py)：
其固定能力目录不会从 Contract 生成；生产 runner 只在握手时传入 `ContractFacts`/`TickFacts`，
并在 object 请求中传递已有 `ObjectFacts`，由脚本按 Contract 投影业务响应。

## 7. 大批量数据

批量生成在服务端执行并支持显式随机种子，以保证场景可重放。前端只提交 generator
spec，不生成并上传百万条 JSON。Ticket 查询采用 cursor/page limit、服务端筛选和排序；
浏览器只虚拟化已加载窗口。统计图消费服务端聚合或采样，不传输全部原始事件。

## 8. 第一阶段 API

```text
GET  /api/v1/health
GET  /api/v1/capabilities
GET  /api/v1/scenario
PUT  /api/v1/scenario
GET  /api/v1/rule-library
PUT  /api/v1/rule-library
POST /api/v1/rules/validate
GET  /api/v1/topology
POST /api/v1/physical-nodes
PUT  /api/v1/physical-nodes
DELETE /api/v1/physical-nodes?physicalNodeId={id}
DELETE /api/v1/physical-nodes/tickets?physicalNodeId={id}
GET  /api/v1/logical-nodes/facts
GET  /api/v1/tickets
POST /api/v1/tickets
POST /api/v1/tickets/custom
POST /api/v1/tickets/batch
DELETE /api/v1/tickets/{ticketId}
POST /api/v1/rounds
GET  /api/v1/matches
GET  /api/v1/matches/{matchId}
GET  /api/v1/events
```

完整请求、响应和错误结构以 [OpenAPI 3.1](../../api/openapi/simulator.yaml) 为准；本表只列
出资源入口。

场景替换必须先完整构造并验证新 cluster，包括调用 Go Provider 的 `Describe()`、启动
Python Worker 并完成 `describe()` 握手，再把有效 Descriptor 交给现有 Contract 校验，
成功后再原子替换；失败时旧场景继续运行。外部 Descriptor 缺失、冲突或不匹配需等实际
握手后在 PUT 构造候选 runtime 时返回 Provider 失败。`rules[*].pythonProvider`
的格式和协议见
[Python Provider 接入](python-provider.md)。
第一阶段以内存状态和确定性模拟为目标，不承诺持久化、鉴权或跨进程一致性。

单条规则校验的请求体为 `{"rule": <match-rule/v1 RuleJSON>}`。场景中的
`rules[*].rule` 保存相同的完整文件，`rules[*].logicalNode` 只描述该 RuleKey 的
PlacementID，`physicalNodeId`、权重和启用状态只属于部署路由。相同 RuleKey 可以部署到
不同 PhysicalNode，但每个部署的 LogicalNode 仍保持 Ticket、索引、Fact frame 和轮次
状态隔离；同一 RuleKey 若出现不同 RuleJSON，场景校验会拒绝。

## 持续调度

手动轮次和定时轮次复用应用层的轮询。调度器按物理节点分别累计成局数，节点达到本轮
配额后跳过该节点，其他节点继续执行。一轮仍使用同一个轮次编号和时钟，响应汇总所有
物理节点的实际产出。每个物理节点内部继续使用自己的逻辑节点选择器。

[持续流量](continuous-traffic.md)绑定当前场景，使用 Simulator 锁串行注入与匹配，替换或关闭时取消；
traffic request 可选择 realtime 墙钟调度或 fixed 有限虚拟时钟，后者仍不写入场景。HTTP
入口为 `/api/v1/traffic` 的 GET/POST/DELETE。流量上限为 `0` 时，任务只推进到达和注入，
跳过定时轮次。
