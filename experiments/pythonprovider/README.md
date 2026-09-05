# Python Provider 实验

独立研究包，不是客户端功能。无需修改 `internal/matchsystem`，使用本机 Python 标准库，
无第三方 Python 包或 CGO 依赖。仓库根目录执行：

```powershell
python --version
go test ./experiments/pythonprovider -count=1 -v
go test ./experiments/pythonprovider -run '^$' -bench . -benchmem -benchtime=2s
```

可设置 `$env:PYTHON_PROVIDER_EXECUTABLE='C:\path\python.exe'`。Python 不存在时测试会
明确 SKIP，不能把 SKIP 视为接入成功。Go 的编译检查不需要 Python。

- `provider.go`：每节点一个 Worker（进程适配器），绑定 Tick/Object/Match 接口。
- `runner.py`：逐行 JSON RPC（进程间调用），标准输出仅用于协议。
- `example.py`：可编辑业务逻辑，包含三类 Provider。
- `provider_test.go`：真实 LogicalNode 和模拟器 RunRound、精度、异常、回收、重载、隔离及耗时实验。

宿主在节点所属执行线程上调用 `Start(python, runner, script, timeout, specs)`，分别绑定
`w.Tick`、`w.Object`、`w`，并提供独立的三个 ProviderDescriptor（能力声明）。
示例是 `TestSimulatorRunRound`。宿主必须在模拟器停止后显式 `Close`；模拟器不会发现或关闭
注入回调持有的 Python 进程。Worker 非并发安全，不能跨逻辑节点共享。

修改脚本后，在轮次之间关闭旧 Worker，重新 Start 并重新绑定 Provider；同一个运行中的
Worker 不自动读取修改。测试 `TestExplicitReloadAndProcessIsolation` 自动写临时脚本、
证明修改前后行为差异。仅改脚本无需重编 Go，但目前宿主装配仍要写 Go。

协议 v1 是实验内部协议，不承诺兼容：请求 `{Version:1, Method, Input}`，方法为
`tick/object/initialize/join`；返回 `{Version:1, Facts}` 或 `{Version:1, Error}`。
Input 使用现有 Go 值结构的 PascalCase 字段；Facts 是 `StringLists/Uint64Lists/Int64Values`。
整数以十进制 JSON 数字传递，Python 直接 int，Go 直接解到 int64/uint64，禁止经 float64
或 JavaScript Number 中转。跨浏览器产品 DTO 需另行设计十进制字符串。

请求与响应各小于 1 MiB；超时包含管道写入/读取，关闭直接进程与管道后等待 I/O goroutine
退出，不遗留“后台继续执行”的回调。错误使 Worker 永久关闭，必须显式替换，不能自动重放。
标准错误当前丢弃，脚本普通 print 被重定向到标准错误。脚本是可信本地代码；本原型不构成
沙箱，也不清理脚本派生的进程树。详见 [ADR](../../doc/design-decisions/adr/python-provider-research.md)
与[实验记录](../../doc/design-decisions/testing/python-provider-research.md)。
