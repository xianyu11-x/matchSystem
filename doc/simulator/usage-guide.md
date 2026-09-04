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
`oldest`、`int64_priority`、`random`。Tick、Object 和 Match Fact Provider 仍由 Go
宿主动态提供；规则文件只保存声明、表达式、内置算法参数和运行预算。

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
