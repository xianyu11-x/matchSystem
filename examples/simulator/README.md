# 模拟器 Python 示例

`builtin_provider.py` 是模拟器内建 Go Provider 的 Python 参考实现。它由生产
JSON-lines runner 加载，提供 `describe()` 和 `provide(method, data)`，不是独立的
命令行程序；使用时在 Rule 的 `pythonProvider.script` 配置服务端脚本路径，并省略
`runner` 以使用模拟器内建 runner。

脚本固定声明当前内建的五项能力：`waitingCount`、`preferredRoles`、`latencyMs`、
`waitingTime` 和 `memberCount`。模拟器在启动握手中一次传入当前 Contract Facts 与静态
Tick 值，业务响应按 Contract 子集投影；object 请求中的 `ObjectFacts` 是已验证的
ObservationRegistry 快照。脚本不会把未声明的 Fact 写回 Validator，也不会因 Python
进程耗时改变模拟器的逻辑时间。

协议、配置和限制见
[`doc/simulator/python-provider.md`](../../doc/simulator/python-provider.md)。
