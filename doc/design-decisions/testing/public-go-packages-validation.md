# 公开 Go 包迁移验证记录

## 环境与范围

- 日期：2026-11-06，Asia/Shanghai。
- 环境：Windows amd64，Go 1.24.6；Python 3.14.2，解释器为
  `C:\Users\yidingxie\AppData\Local\Programs\Python\Python314\python.exe`。
- 以下检查针对最终 `pkg/` 布局代码执行。测试完成后 Go 源码、脚本和模块文件未再修改；
  根模块检查与仓库外独立消费者检查为两个批次。

## 根模块完整检查

在仓库根目录设置解释器并执行：

```powershell
$env:PYTHON_PROVIDER_EXECUTABLE = 'C:\Users\yidingxie\AppData\Local\Programs\Python\Python314\python.exe'
go test ./... -count=1
go vet ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-expression-deps.ps1
```

三条命令均以退出码 0 完成。`go test` 列出 16 个包，其中 10 个为 `ok`，6 个为
`[no test files]`；各有测试的包耗时如下：

| 包 | 耗时 |
| --- | ---: |
| `cmd/match-benchmark` | 0.694 秒 |
| `cmd/simulator-api` | 0.720 秒 |
| `experiments/pythonprovider` | 4.065 秒 |
| `internal/simulator` | 6.663 秒 |
| `internal/simulatorapi` | 3.905 秒 |
| `pkg/matchsystem` | 1.826 秒 |
| `pkg/matchsystem/contract` | 1.451 秒 |
| `pkg/matchsystem/evaluation` | 0.789 秒 |
| `pkg/matchsystem/fact` | 1.079 秒 |
| `pkg/matchsystem/prefilter` | 1.719 秒 |

`go vet` 无输出并成功退出。依赖检查脚本输出
`expression dependency boundary: OK`；`ExecutionPolicy Bypass` 仅适用于该脚本进程。
根模块的 `go test ./...` 不遍历 `examples/go-consumer` 独立模块。

迁移期间的定向检查包括 `go list ./...` 和
`go test ./pkg/client ./pkg/common ./pkg/identity ./pkg/matchsystem/... -count=1`，
均成功。依赖脚本的 5 条禁止依赖边界分别进行了注入式负向检查，均被拒绝：
`expression` 不得依赖 `prefilter`、`evaluation` 或 RoaringBitmap，`evaluation`
不得依赖 `prefilter`，`fact` 不得依赖 `expression`。

## 仓库外独立消费者检查

在仓库根目录执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-go-consumer.ps1
```

命令以退出码 0 完成，输出包括：

```text
ok   example.com/matchsystem-consumer  0.461s
matched: 1001,1002
remaining: 0
duplicate: false
独立消费者验证通过: C:\Users\yidingxie\AppData\Local\Temp\matchsystem-consumer-241f7879b5a3452798d4ef430bdc03d8\go.mod
```

输出中的 `go.mod` 是该次运行的临时消费者模块，不是仓库根模块。脚本使用
`GOWORK=off` 和指向仓库绝对路径的 `replace`，先核对 `go env GOMOD`，再执行
`go mod tidy`、通过 `go list -deps` 检查不依赖模拟器应用层，然后执行消费者的
3 个测试和 `go run .`。

[消费者测试](../../../examples/go-consumer/main_test.go)覆盖真实路由、自描述 Provider、
两张 Ticket 成局、提交清理、同轮和下一轮不重复成局，以及不同 partition 不匹配；
也核对示例输出。另行设置 `$env:GOWORK='off'` 后执行
`go -C examples/go-consumer vet ./...`，退出码为 0 且无输出。外部检查后使用
`Test-Path` 确认上述临时目录不存在。

## 尚未验证

仓库外消费者使用本地 `replace`，不证明包含本次变更的代码已推送或发布；远端
`go get` 是否可用尚未验证。

## 2026-11-20 Client Router 验证

环境为 Windows 11 64 位、Go 1.24.6（windows/amd64），代码处于未提交工作树。
在仓库根目录执行以下检查，均通过：

```powershell
go test ./... -count=1
go vet ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-go-consumer.ps1
```

完整 Go 测试包含 `pkg/client` 的路由与选择器回归，以及模拟器身份转换测试。相关 Go
文件已通过 `gofmt` 格式检查，`git diff --check` 通过。独立消费者仍使用本地 `replace`，
不代表代码已发布。本节结果对应数值身份与选择器变更，物理节点级路由更新的验证另行
记录。测试入口和约束见[功能验证矩阵](functional-validation.md#client-router)。

## 2026-11-20 物理节点级路由更新验证

环境为 Windows 11 64 位、Go 1.24.6（windows/amd64），代码处于未提交工作树。以下
检查通过：

```powershell
# 仓库根目录
go test ./pkg/client -count=1
go test ./... -count=1
go vet ./...
git diff --check

# examples/go-consumer 目录
go test ./... -count=1
```

变更的 `pkg/client` Go 文件已通过 `gofmt` 格式检查。
[路由更新测试](../../../pkg/client/router_update_test.go)覆盖新增节点与零值 Router、完整
部署替换、幂等下线及重新加入、候选顺序、Owner 解析、无效输入回滚和共享路由表隔离。
独立消费者使用本地 `replace`，上述检查对应未发布的工作树。

## 2026-11-23 共享 uint64 身份验证

环境为 Windows 11、Go 1.24.6（windows/amd64）和 Python 3.14。未提交工作树中的
完整 Go 测试、静态检查和仓库外独立消费者脚本均通过。完整测试使用本机 Python 3.14
作为 `PYTHON_PROVIDER_EXECUTABLE`。

```powershell
go test ./... -count=1
go vet ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-go-consumer.ps1
```

[共享身份测试](../../../pkg/identity/key_test.go)覆盖 `uint64` 最大值、旧命名空间和
无损 JSON 往返。[核心规则测试](../../../pkg/matchsystem/rule_config_test.go)确认同一个
规则键的不同 JSON 表示具有同一指纹。[模拟器测试](../../../internal/simulator/route_table_test.go)
校验真实路由结果和 Owner 解析。[HTTP 测试](../../../internal/simulatorapi/identity_wire_test.go)
覆盖两个相邻的高位 ID、十进制字符串输出和溢出拒绝。

规则工具的默认文件通过 Python 校验，完整 `uint64`、旧名称与指纹的三项 Python
回归通过。OpenAPI 和两个更新的 JSON Schema 均通过文档解析检查。

Node.js 24.16.0 下的 Web 构建和全部 162 项 Vitest 测试通过。回归覆盖相邻大规则
编号、部署请求中的数值精度、十进制身份导入与超范围输入。场景导入拒绝无法精确
读取的 JSON 数字身份，并提示改用十进制字符串。

### 模拟器直接连接验证

精简路径后，在 Windows 11 和 Go 1.24.6 的未提交工作树重新运行
`go test ./... -count=1` 与 `go vet ./...`，均通过。用例覆盖共享 `uint64` 身份、
场景 JSON 往返、物理节点热更新和 Ticket 观测结果中的路由决策。

### 身份编解码精简验证

在同一环境执行 `go test ./... -count=1`、`go vet ./...` 和独立 Go 消费者验证脚本，
均通过。共享身份测试覆盖 `uint64` 最大值、默认命名空间省略，以及旧标签和十进制
输入。场景与 HTTP 用例继续验证十进制字符串输出和超范围输入拒绝。

### 规范十进制身份与现有规则迁移验证

在 Windows 11 的未提交工作树执行 `go test ./... -count=1`、`go vet ./...`、独立
Go 消费者验证和离线规则校验器的 4 项 Python 测试，均通过。真实模拟器 HTTP 验证中，
工作目录里两个 `dist/` 场景文件均可加载，规则键、部署位置 ID 和物理节点引用往返一致。
仓库里的默认规则模板通过 Go 编译和 Python 校验。旧文本名称及带前导零的字符串被拒绝。

Web 生产构建和 176 项 Vitest 测试通过，覆盖前端身份解析、场景导入、Schema、
节点部署编辑和批量生成前缀校验。

### RuleID 唯一身份与别名验证

未提交工作树的 Go 全量测试、`go vet ./...`、独立 Go 消费者验证、3 项 Python
规则校验测试、Web 生产构建与 183 项 Vitest 测试通过。真实模拟器 HTTP 检查加载了
两份 `dist/` 场景，并验证不同别名的同一 RuleID 返回相同的 Owner 和 DecisionID。
规则改名的指纹一致，运行时部署改名后保留已排队的 Ticket，Facts 响应显示更新后的别名。

## 公开包导入边界验证

在 Windows 11 和 Go 1.24.6 的未提交工作树执行 `go test ./... -count=1`、
`go vet ./...`、独立消费者脚本和表达式依赖检查，均通过。将 `pkg/`、`go.mod`
和 `go.sum` 单独复制到仓库外并设置 `GOWORK=off`，运行 `go test ./pkg/... -count=1`
也通过。在隔离副本中加入带未启用构建标签的测试文件，检查确认拒绝从 `internal/simulator`
和 `pkg_extra` 导入，并逐条报告源文件行号。

## 2026-11-23 路由快照与队列选路验证

环境为 Windows 11 amd64 和 Go 1.24.6，基于 `b20de2f` 的未提交工作树。全量测试使用
本机 Python 3.14 解释器。以下检查通过：

```powershell
go test ./pkg/matchsystem -run TestPhysicalNodeRouteSnapshot -count=1
go test ./pkg/client -count=1
go test ./... -count=1
go vet ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-go-consumer.ps1
git diff --check
```

节点测试验证快照顺序、独立性、等待数和生命周期。客户端测试验证确定性随机输入的抽样
区间、阈值等值切换、重新同步后恢复、全满回退和非法发布回滚，并复跑默认算法的固定
路由结果。独立消费者使用本地 `replace` 验证公开 API、快照发布及排空和下线后的 Owner
解析，示例运行输出两张 Ticket 已成局、剩余 0、未重复成局。
