# Repository Guidelines

## 项目结构与模块职责

- `internal/matchsystem/`：Go 匹配核心；`prefilter/` 负责索引预筛选，`evaluation/` 负责规则评估。
- `internal/simulator/` 与 `internal/simulatorapi/`：模拟器应用层与 HTTP 适配层；`cmd/` 提供示例、API 和基准入口。
- `apps/web/src/`：React + TypeScript 页面、组件、样式和测试；`apps/desktop/`：Tauri 2 桌面壳与打包资源。
- `api/`：OpenAPI 与 JSON Schema；`doc/`：权威文档，入口为 `doc/README.md`；`scripts/`：构建与验证脚本。

## 构建、测试与本地开发

在仓库根目录执行，Go 版本要求见 `go.mod`（1.24.5）。

| 命令 | 用途 |
| --- | --- |
| `go build ./...` | 编译全部 Go 包 |
| `go run ./cmd/app` | 运行核心示例 |
| `go run ./cmd/simulator-api --addr 127.0.0.1:8080` | 启动模拟器 API |
| `go test ./... -count=1` | 执行 Go 测试，禁用结果缓存 |
| `go vet ./...` | Go 静态检查 |
| `npm --prefix apps/web ci` | 按锁文件安装前端依赖 |
| `npm --prefix apps/web run dev` | 启动 Vite 开发服务器 |
| `npm --prefix apps/web run build` | 类型检查并构建前端 |
| `npm --prefix apps/web test` | 执行 Vitest 测试 |
| `npm --prefix apps/desktop run check:config` | 验证桌面配置 |

连接本地 API 前，在 PowerShell 设置 `$env:VITE_API_BASE_URL = "http://127.0.0.1:8080/api/v1"`。桌面构建前提与步骤见 `doc/simulator/client-build.md`。

## 代码风格与架构约束

Go 使用 `gofmt` 和制表符缩进；包名小写，导出标识符使用 PascalCase（大驼峰），文件采用 `snake_case.go`。前端使用两空格缩进、单引号、无分号，遵循 `apps/web/.prettierrc.json`；React 组件采用 PascalCase，函数采用 camelCase（小驼峰）。格式化仅限相关文件。

保持 `LogicalNode`（逻辑节点）的状态与索引隔离；宿主通过单一 owner goroutine（状态所有者协程）串行调用核心。业务规则通过 RuleJSON 与 Fact Provider（事实提供器）接入，避免将业务特例写入通用核心。

## 测试规范

Go 使用标准 `testing`，测试与源码同目录，命名为 `*_test.go`、`TestXxx`，基准使用 `BenchmarkXxx`。前端测试使用 Vitest，命名为 `*.test.ts`。修复行为缺陷时增加对应回归测试；先运行相关包，再执行受影响组件的完整检查。当前未设置统一覆盖率门槛。

修改表达式依赖时执行 `powershell -NoProfile -File scripts/check-expression-deps.ps1`；API 或规则格式变更需同步 Schema、测试与文档。

## 提交与 Pull Request

使用 Conventional Commits（约定式提交），格式为 `<type>(<scope>): <中文描述>`；作用域可省略。类型和作用域保留英文标识，例如 `feat`、`fix`、`docs`、`refactor`、`test`、`ci`，标题描述与正文必须使用中文，代码符号和必要的专业术语保留原文。每次提交聚焦一个目的，描述具体变化；正文按需说明原因、影响与验证结果。

示例：`fix(ci): 修复锁文件根版本解析`、`feat(simulator): 支持查看匹配历史`、`docs: 明确文档同步与任务执行规范`。

PR（拉取请求）应说明问题、行为变化和验证结果，关联已有 issue；界面变更附截图。提交前运行 `git diff --check`，不要提交二进制、依赖目录或构建产物，也不要把密钥放进 `VITE_*` 环境变量。

## 文档结构与同步要求

以 [文档中心](doc/README.md) 为唯一总入口，具体归档规则和记录要求见其“文档维护约定”。

- `doc/match-system/`：匹配核心的当前架构、包职责、代码索引、运行流程、参数与接入指南。
- `doc/simulator/`：模拟器、HTTP API、Web/Desktop 的当前行为、使用与发布流程。
- `doc/design-decisions/`：变更记录、ADR（架构决策记录）、评估与验证证据；`archive/` 仅保存历史。

**代码结构变更、功能更新和重要决策必须在同一任务中同步记录到文档，文档未完成则任务未完成。** 更新现有权威说明，并在 `doc/design-decisions/change-log.md` 记录变化、影响与文档链接；重要决策另建或更新 ADR。不得仅依赖提交信息、PR 或聊天记录。新增、移动、删除文档时同步维护分类索引与交叉链接。

## 任务执行规范

1. **确认现状**：阅读适用的 `AGENTS.md`、文档入口及相关源码；检查工作区改动，区分已实现行为、设计提案与历史记录。
2. **明确范围**：确定受影响模块、契约、测试和文档；复杂任务先列执行步骤与验收条件，重要方案记录取舍。
3. **实施与同步**：围绕任务修改代码、配置、测试及文档；重命名或迁移时搜索并清理旧符号、路径和引用，保留用户无关改动。
4. **验证结果**：运行与改动相关的检查，核对文档中的命令、符号与链接；记录实际结果，未执行或受阻的检查须说明原因，不得标记为通过。
5. **交付闭环**：核对变更记录和 ADR，汇报行为变化、文档位置、验证结果及剩余事项；只有代码、文档与必要验证均完成后才能宣告完成。

## Agent 工作约定

默认使用中文，专业术语附英文与中文说明。探索或编码存在可独立并行的子任务时使用 subagent（子代理），模型选择 `gpt-5.6-luna`，推理强度为 `max`。
