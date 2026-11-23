# 外部 Go 模块接入

MatchSystem 是进程内 Go 库。模块路径是 `github.com/xianyu11-x/matchSystem`，
最低要求 Go 1.24.5。`pkg/` 下四个公开包及 `matchsystem` 的六个子包属于同一个根模块，
随该模块一起选择版本，`pkg/` 下没有独立的 Go 模块；模拟器的 `internal/simulator` 和 `internal/simulatorapi`
仍是仓库内部实现。`apps/web/go.mod` 是隔离 Web 依赖目录的独立模块，不是 Go 库入口。

## 获取模块与导入

迁移后的代码推送到该仓库并可由 Go 模块工具获取后，在自己的 Go 模块中运行：

```powershell
go get github.com/xianyu11-x/matchSystem/pkg/matchsystem@latest
```

生产项目应选择包含本次迁移的版本或提交；`@latest` 只有在其解析到的版本包含 `pkg/`
布局时才能使用，推送代码本身不保证它立即指向该版本。根据需要导入，例如：

```go
import (
    "github.com/xianyu11-x/matchSystem/pkg/client"
    "github.com/xianyu11-x/matchSystem/pkg/common"
    "github.com/xianyu11-x/matchSystem/pkg/identity"
    "github.com/xianyu11-x/matchSystem/pkg/matchsystem"
    "github.com/xianyu11-x/matchSystem/pkg/matchsystem/fact"
)
```

导入时仅保留应用实际使用的包。原模块名为 `matchSystem`，四个包及其子包位于
`matchSystem/internal/...`；调用方需将导入改为上面的 `github.com/xianyu11-x/matchSystem/pkg/...`
路径并更新依赖。没有旧路径兼容包装层，Go 对不同导入路径的类型按不同包身份处理，
迁移宿主时应一起更新相关代码。

## 本地工作树替换

在**仓库外**的消费者目录中初始化自己的模块，将 `G:/matchSystem` 换成本地仓库根目录；
替换目标是包含本项目 `go.mod` 和 `pkg/` 的仓库根目录，不是 `pkg/`：

```powershell
go mod init example.com/my-match-host
go mod edit -require=github.com/xianyu11-x/matchSystem@v0.0.0
go mod edit -replace=github.com/xianyu11-x/matchSystem=G:/matchSystem
```

`v0.0.0` 在此仅是本地替换所需的占位版本，不声明该版本已经发布。写入使用上述导入
路径的 Go 源文件后，在消费者目录运行 `go mod tidy` 和 `go test ./...`。移除 `replace`
并切换到已发布的模块版本后，依赖将从模块源获取。

## 独立消费者示例与验证

仓库提供[独立消费者模块](../../examples/go-consumer)，可查看外部宿主如何使用公开 Go
包。它有自己的 `go.mod`；在仓库根模块执行 `go test ./...` 会覆盖根模块下的 `pkg/`，
但不会覆盖这个嵌套模块，需单独执行验证。在仓库根目录运行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-go-consumer.ps1
```

[验证脚本](../../scripts/check-go-consumer.ps1)将消费者复制到仓库外的临时目录，使用
`GOWORK=off` 和指向仓库绝对路径的 `replace`；脚本先检查消费者模块位置，再执行
`go mod tidy`、通过 `go list -deps` 检查同仓依赖是否都在 `pkg/` 下，最后执行 `go test ./... -count=1`
和 `go run .`。这样可以检查实际跨模块导入，而不依赖根工作区的设置。

## 包与宿主边界

`pkg/` 的 Go 源码和测试在本仓库内只导入 `pkg/` 下的包，可以使用 Go 标准库和第三方
模块。运行 `go test ./pkg/...` 时，[导入边界测试](../../pkg/dependencies_test.go)扫描源文件中
的导入声明，覆盖当前平台未启用的文件。

| 包 | 职责 |
| --- | --- |
| [`client`](../../pkg/client) | 构建路由表，并根据请求计算或解析 PhysicalNode 的路由；只返回目标身份和 Endpoint，不执行 RPC 或其他网络传输。`Router` 的可变操作由同一 owner goroutine 串行调用。 |
| [`common`](../../pkg/common) | 跨路由和核心共享的 Ticket、Match、Endpoint 与路由值对象。 |
| [`identity`](../../pkg/identity) | RuleKey、LogicalNodeKey、OwnerRef 和 PhysicalNodeID 等稳定身份。 |
| [`matchsystem`](../../pkg/matchsystem) | 进程内 PhysicalNode/LogicalNode、规则编译、匹配轮次与提交入口。 |

以下子包位于 `github.com/xianyu11-x/matchSystem/pkg/matchsystem/` 下，也可直接导入其
导出 API：

| 子包 | 职责 |
| --- | --- |
| `contract` | 规则使用的 Contract 模型与校验。 |
| `evaluation` | CanJoin、CanComplete 谓词编译与求值。 |
| `expression` | 共享标量表达式的编译与求值。 |
| `fact` | Fact 值、Provider 接口和校验工具。 |
| `jsonstrict` | 严格 JSON 结构检查。 |
| `prefilter` | 索引预筛选的编译和运行时。 |

子包的具体导出 API 与依赖边界见[包级文档矩阵](packages.md)；库根包的若干类型是
`common` 或 `fact` 类型别名，部分类型只在各自子包中导出。宿主负责提供动态 Fact
Provider，并确保每个 PhysicalNode 及其 LogicalNode 的调用由单一 owner goroutine
串行执行。从 `BeginMatchRound` 到本轮 `ProduceMatch` 消费结束期间，宿主不得调用
`Add`；期间收到的新 Ticket 由宿主自行暂存，在下一轮开始前投递。核心不提供轮内
`Add` 的快照或延迟缓冲保证。宿主需要跨进程通信时，自行实现传输、服务发现和调度
适配；`client.Router` 不会发送请求。匹配规则、Provider 与轮次的完整使用步骤见
[使用指南](usage-guide.md)。
