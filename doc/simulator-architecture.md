# 匹配模拟器架构

## 1. 目标与边界

匹配模拟器是现有进程内匹配核心的独立宿主和可视化客户端。它负责场景配置、
多 PhysicalNode（物理节点）编排、Ticket/Fact 观测、批量数据生成、HTTP API 和运行事件；
`internal/matchsystem` 仍只负责 LogicalNode（逻辑节点）内的索引、候选选择、规则求值和
匹配提交。

依赖方向固定为：

```text
apps/web -> HTTP/SSE -> simulator API -> simulator application -> matchsystem
                                                              -> client.Router
```

`matchsystem` 不导入 simulator、HTTP 或前端类型。前端也不直接复制 Go 内部结构，
只依赖版本化的 OpenAPI/JSON Schema。

## 2. 技术选型

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

桌面 shell 拥有它启动的 sidecar 生命周期：启动时传入 `--addr 127.0.0.1:0`，读取 stdout
唯一一行 `ready` JSON 获得动态端口，等待 health endpoint 成功后再开放 UI 请求；正常关闭
窗口或应用时，只终止本次持有 handle 的 child。后端日志必须写 stderr，避免污染握手协议。
Web 部署不启动 sidecar，而是从环境配置读取本地或远程 API base URL。

## 3. 仓库结构

```text
api/
  openapi/simulator.yaml       # HTTP wire contract
  schema/                      # v3 DSL 的 JSON Schema
apps/
  web/                         # 独立前端应用
  desktop/                     # 可选 Tauri 2 Windows shell
cmd/
  app/                         # 保留现有内核示例
  simulator-api/               # 独立 HTTP 服务入口
internal/
  matchsystem/                 # 现有匹配核心
  client/                      # 现有路由算法
  common/                      # transport-neutral 核心值对象
  identity/                    # 稳定身份
  simulator/                   # 场景、运行时、生成器、观测模型
  simulatorapi/                # HTTP DTO、handler、SSE/CORS
```

## 4. 多物理节点运行时

一个 simulator process 可以创建多个 `matchsystem.PhysicalNode`。每个 PhysicalNode
由自己的 owner goroutine 和命令队列串行调用，保持核心的 non-goroutine-safe（非并发安全）
契约。不同 PhysicalNode 可以承载同一 RuleKey，但必须使用不同 PlacementID；每个
LogicalNode 的 Ticket store、DocID、索引、Fact frame、seed round 和 evaluator 绝对隔离。

新 Ticket 的流程为：

```text
RouteRequest
  -> client.Router.RouteNew
  -> retain RouteDecision/OwnerRef
  -> dispatch to OwnerRef.PhysicalNodeID worker
  -> PhysicalNode.Add
  -> update simulator observation registry
```

观测 registry 以 `(OwnerRef, TicketID)` 为身份，不能只使用 TicketID。匹配成功后，
simulator 根据核心返回的成员移除等待记录并追加 immutable match event。

## 5. 规则与合法性

当前生产规则只有四种 v3 envelope：

- `logical-node-contract/v3`；
- `expression-scalar/v3`；
- `prefilter/v3`；
- `evaluation/v3`。

前端节点 palette 只展示 simulator capability endpoint 返回的合法节点类型，并根据
Contract 限制 Attribute、Fact、source、index 和端口类型。连接时检查类型、基数和环；
保存前再调用服务端 validation endpoint。最终仍由 `NewLogicalNode` 复用现有严格 parser/
compiler 验证未知字段、未知 op、非法引用、类型、scope、index 和复杂度上限。

JSON Schema 和前端校验只负责快速反馈，不能代替 Go 编译结果。

## 6. Ticket 与 Fact

核心 `common.Ticket` 不包含 DocID，API 也不暴露 DocID。API Ticket 同时携带：

- `ticketId`、`createdAt` 和三类 typed Attribute map；
- object-scope Fact snapshot（由 simulator provider registry 提供）；
- `RouteDecision/OwnerRef` 和等待/已匹配状态。

tick-scope Facts 属于 LogicalNode 运行配置，可通过控制面更新。match-scope Facts 由
服务端配置的 reducer provider 产生完整 snapshot，匹配结果原样展示。所有提交输入先按
Contract 校验；非法 Attribute/Fact 名称或类型不会进入核心。

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
POST /api/v1/rules/validate
GET  /api/v1/topology
GET  /api/v1/tickets
POST /api/v1/tickets
POST /api/v1/tickets/batch
DELETE /api/v1/tickets/{ticketId}
POST /api/v1/rounds
GET  /api/v1/matches
GET  /api/v1/events
```

场景替换必须先完整构造并验证新 cluster，成功后再原子替换；失败时旧场景继续运行。
第一阶段以内存状态和确定性模拟为目标，不承诺持久化、鉴权或跨进程一致性。
