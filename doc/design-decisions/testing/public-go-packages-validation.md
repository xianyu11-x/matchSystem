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

## 公开包导入边界验证

在 Windows 11 和 Go 1.24.6 的未提交工作树执行 `go test ./... -count=1`、
`go vet ./...`、独立消费者脚本和表达式依赖检查，均通过。将 `pkg/`、`go.mod`
和 `go.sum` 单独复制到仓库外并设置 `GOWORK=off`，运行 `go test ./pkg/... -count=1`
也通过。在隔离副本中加入带未启用构建标签的测试文件，检查确认拒绝从 `internal/simulator`
和 `pkg_extra` 导入，并逐条报告源文件行号。
