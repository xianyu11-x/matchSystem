# MatchSystem

MatchSystem 是一个进程内匹配核心，并提供独立的匹配模拟器与 MatchScope 可视化客户端。
每条匹配规则使用一份 `match-rule/v1` RuleJSON，统一描述 Contract、Prefilter、
Evaluation、候选评分、Seed 选择和运行预算；宿主负责注入动态 Fact Provider。

## 文档

项目文档按职责分为三类，统一入口见 [文档中心](doc/README.md)：

- [模拟器](doc/simulator/README.md)：模拟器架构、启动使用、Fact 数据来源、Match 历史与客户端构建。
- [匹配系统](doc/match-system/README.md)：核心架构、接入指南、参数明细、运行流程，以及根包和全部子包的代码索引与用户指南。
- [设计决策](doc/design-decisions/README.md)：ADR、设计评估、设计约束、功能验证、性能测试、发布验证和历史归档。

组件目录中的 README 只承担就近入口职责；上述三类目录是项目说明的权威来源。

## 快速开始

运行核心示例：

```powershell
go run ./cmd/app
```

启动模拟器 API：

```powershell
go run ./cmd/simulator-api --addr 127.0.0.1:8080
```

再启动 Web 客户端：

```powershell
npm --prefix apps/web install
$env:VITE_API_BASE_URL = "http://127.0.0.1:8080/api/v1"
npm --prefix apps/web run dev
```

规则配置与代码接入从 [匹配系统使用指南](doc/match-system/usage-guide.md) 开始；
完整模拟器使用方式见 [模拟器使用指南](doc/simulator/usage-guide.md)。

## 仓库结构

```text
api/                 OpenAPI 与 JSON Schema
apps/web/            React/Vite 客户端
apps/desktop/        Tauri 2 Windows 桌面壳
cmd/                 示例、模拟器 API 与性能基准入口
internal/matchsystem 匹配核心
internal/simulator   模拟器应用层
internal/simulatorapi HTTP 适配层
doc/                 三类项目文档
```

## 常用验证

```powershell
go test ./... -count=1
go vet ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-expression-deps.ps1
npm --prefix apps/web run typecheck
npm --prefix apps/web test -- --run
npm --prefix apps/desktop run check:config
```

Windows 安装包和便携版的环境要求、命令与产物见
[客户端构建与发布](doc/simulator/client-build.md)。
