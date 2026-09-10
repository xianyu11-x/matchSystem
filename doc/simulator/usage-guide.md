# 匹配模拟器使用指南

模拟器由三个可独立交付的部分组成：Go API、React/Vite Web 客户端和可选的
Tauri 2 Windows shell（桌面壳）。Web 与桌面端都只通过同一套 REST/JSON + SSE API
访问模拟器，不直接依赖 `internal/matchsystem`。

| 目标 | 工作目录 | 命令/入口 | 主要输出 |
| --- | --- | --- | --- |
| API 开发服务 | 仓库根目录 | `go run ./cmd/simulator-api --addr 127.0.0.1:8080` | `/api/v1` HTTP/SSE |
| Web 开发服务 | `apps/web` | `npm run dev` | Vite 开发地址 |
| Desktop 开发 | `apps/desktop` | `npm run build:sidecar` 后 `npm run dev` | Tauri 窗口与动态 sidecar |
| 独立便携包 | `apps/desktop` | `npm run build:portable` | `apps/desktop/dist/*-portable.zip` |
| 完整客户端发布 | 仓库根目录 | `./scripts/build-client.ps1` | `dist/release/` |

## Web 开发模式

终端一，在仓库根目录启动 Go API：

```powershell
go run ./cmd/simulator-api --addr 127.0.0.1:8080
```

服务在 stdout 输出一行 `ready` JSON；健康检查地址是
`http://127.0.0.1:8080/api/v1/health`。

终端二，启动 Web 客户端：

```powershell
Set-Location apps/web
npm install
$env:VITE_API_BASE_URL = "http://127.0.0.1:8080/api/v1"
npm run dev
```

只有显式设置 `VITE_DEMO_MODE=true` 时才使用前端演示数据；默认连接真实 API。

## 表单配置

Rules（规则页）提供字段契约、预筛选、加入/成局、种子/评分/预算、Facts 和场景部署
表单；可直接新建安全空规则、复制与删除规则，切换规则时保留未保存草稿，
统一保存全部规则并在 Go 侧验证。完整配置范围、操作说明与边界见[规则与场景表单](rule-forms.md)。

## 规则文件

每条规则使用一份完整的 `match-rule/v1` RuleJSON。`ruleKey` 中的 namespace 和 ruleId
必须与场景 `logicalNode.rule` 一致；场景中的 `placementId`、PhysicalNode、权重和启用
状态属于部署拓扑。最小规则文件如下：

```json
{
  "schemaVersion": "match-rule/v1",
  "ruleKey": {"namespace": "demo", "ruleId": 1},
  "contract": {"schemaVersion": "logical-node-contract/v3", "attributes": [], "facts": [], "indexes": []},
  "prefilter": {"schemaVersion": "prefilter/v3", "bitmap": {"resultType": "bitmap", "expr": {"op": "none"}}},
  "evaluation": {
    "schemaVersion": "evaluation/v3",
    "canJoin": {"schemaVersion": "expression-scalar/v3", "resultType": "bool", "expr": {"op": "bool_literal", "value": true}},
    "canComplete": {"schemaVersion": "expression-scalar/v3", "resultType": "bool", "expr": {"op": "bool_literal", "value": true}}
  },
  "scoring": {"type": "constant", "params": {"value": 0}},
  "seedSelection": {"type": "arrival", "params": {}},
  "runtime": {"candidateScoringLimitPerSeed": 500, "candidateLimitPerSeed": 50, "maxPlayers": 8, "attemptLimitPerProduceMatch": 500, "attemptLimitPerMatchRound": 500}
}
```

`scoring` 支持 `constant`、`created_at`、`int64_field`；`seedSelection` 支持 `arrival`、
`oldest`、`int64_priority`、`random`。Tick、Object 和 Match Fact Provider 可以由 Go
宿主或场景的 `rules[*].pythonProvider` 提供；规则文件保存声明、表达式、内置算法参数和
运行预算，场景额外保存服务端 Python Worker 绑定。Go Provider 在加载阶段由进程内
`Describe()` 返回能力，Python Provider 由 JSON-lines 握手的 `describe()` 返回能力；脚本
路径由服务端解释。场景 JSON 不保存 Provider Descriptor。
字段和 JSON-lines 示例见[Python Provider 接入](python-provider.md)。

要把现有场景绑定到仓库内建 Go Provider 的 Python 参考实现，可在对应的
`rules[*]` 中保留原有 `rule`、`contract` 与 `tickFacts`，只设置下面的服务端脚本绑定；省略
`runner` 使用生产内建 runner：

```json
{
  "pythonProvider": {
    "script": "G:\\matchSystem\\examples\\simulator\\builtin_provider.py",
    "timeoutMs": 2000
  }
}
```

参考脚本见 [`examples/simulator/builtin_provider.py`](../../examples/simulator/builtin_provider.py)。
它只支持 `waitingCount`、`preferredRoles`、`latencyMs`、`waitingTime` 和 `memberCount` 的
规范 Contract 形状；使用其它 Fact 名称的自定义 Python 脚本不能仅通过替换路径迁移。删除
`pythonProvider` 并保存场景即可继续使用默认 Go Provider。

### 在界面配置 Python Provider

打开“规则”页，选择规则后进入“场景与部署”标签，在对应“规则部署”中勾选“启用此
LogicalNode 的 Python Provider”，填写服务端可读的脚本路径、可选解释器/Runner 和 RPC
超时。启动握手必须由 `describe()` 按实际能力返回相应的 Tick/Object/Match Descriptor；
Contract 使用的每个 scope 都必须覆盖，无需手填。三类 scope 仍属于同一个 Worker，不会创建三个独立绑定；`providerDescriptors` 的 `source`
会在 Fact 元数据中显示 `python`，Go Provider 的运行态描述显示 `go`；描述不会回填场景 JSON。
完成后点击页面顶部“保存场景”才会应用到模拟器；成功保存会重建运行态并清空等待队列与
Match 历史。scope 映射、脚本方法和失败行为见[Python Provider 接入](python-provider.md)。

校验单条规则时，API 请求体是 `{"rule": <上述 RuleJSON>}`，提交到
`POST /api/v1/rules/validate`；场景替换时，每个 `rules[*].rule` 也使用同一份 RuleJSON。
所有字段、默认值和交叉约束见[匹配系统参数明细](../match-system/parameters.md)。

## API 连通性检查

启动服务后，可以先执行无副作用查询确认 API、能力目录、默认场景和拓扑一致：

```powershell
$api = "http://127.0.0.1:8080/api/v1"
Invoke-RestMethod "$api/health"
Invoke-RestMethod "$api/capabilities"
Invoke-RestMethod "$api/scenario"
Invoke-RestMethod "$api/topology"
```

典型端到端操作顺序是：校验 RuleJSON → `PUT /scenario` → 创建或批量生成 Ticket →
`POST /rounds` → 查询 `/matches` 和 `/matches/{matchId}` → 查询 Fact 元数据与事件。
请求体、分页和错误码以 [OpenAPI 3.1](../../api/openapi/simulator.yaml) 为准。

## Windows 桌面模式

桌面端启动时会自行拉起 `simulator-api` sidecar（伴生进程），使用动态回环端口，
等待健康检查成功后才创建主窗口。关闭窗口或退出应用时，它只终止本次启动并持有
handle（句柄）的子进程，不会按进程名清理其他 API 实例。

```powershell
Set-Location apps/desktop
npm install
npm run build:sidecar
npm run dev
```

生成 Windows 安装包使用 `npm run build`。如果不需要 MSI/NSIS，使用下面的命令生成
解压即用的 portable ZIP（便携压缩包）：

```powershell
npm run build:portable
```

产物位于 `apps/desktop/dist/`。完整解压后直接运行 `MatchScope.exe`；同目录的
`simulator-api.exe` 会自动启动，并随桌面进程退出。目标电脑不需要 Go、Node.js 或
Rust，但需要 Windows 10/11 和 WebView2 Runtime（网页视图运行时）。

构建机需要 Rust/Cargo、Go、Node.js、
Tauri 2 所需的 Windows 构建环境和 WebView2；sidecar 的目标 triple（目标三元组）
必须与桌面包一致。完整说明见 [桌面端 README](../../apps/desktop/README.md)。

## 常用验证

```powershell
# 仓库根目录
go test ./... -count=1
go vet ./...
go build ./...
go mod verify
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-expression-deps.ps1

# Web
Set-Location apps/web
npm run typecheck
npm test
npm run build

# Desktop（不需要 Rust 的静态检查）
Set-Location ../desktop
npm run check:config
```

HTTP 契约见 [OpenAPI 3.1](../../api/openapi/simulator.yaml)，规则结构见
[JSON Schema](../../api/schema/README.md)，包边界和多节点运行时见
[模拟器架构](architecture.md)。

批量生成的逐属性分布、数量及区间填写方法见[属性生成配置](attribute-generation.md)。

## 持续流量

在 Tickets 页切换到“批量与持续流量”，先选择目标规则与节点，再配置属性生成器、属性随机
种子、起始 TicketID 和共同 Object Facts。随后在“持续流量与定时匹配”的“模拟时钟”中选
“固定步长运行”，填写“虚拟步数”（`steps`，1..10000000）、“步长（毫秒）”（`stepMs`，
1..86400000）和“虚拟起始时间（Unix ms）”（`startTimeMs`，非负安全整数），并配置到达分布、
“匹配间隔（毫秒）”和每次最多产出 Match。固定模式的匹配间隔允许 1..86400000 毫秒；点按
“开始固定步长运行”后任务会推进有限的虚拟时间并在状态中显示“已完成”、虚拟时间和墙上耗时。
Python RPC 和核心计算只增加墙上耗时，不改变虚拟时间。字段默认值、实时模式限制和停止/接续
编号操作见[持续流量与定时匹配](continuous-traffic.md)。
