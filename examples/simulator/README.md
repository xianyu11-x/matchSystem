# 模拟器示例

## Python Provider

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

## FIFO 规则

[fifo-match-rule.json](fifo-match-rule.json) 为四票规则，RuleID 为 `91001`；
[fifo-8-match-rule.json](fifo-8-match-rule.json) 为八票规则，RuleID 为 `91002`。
两者均可导入模拟器规则库，按 `Ticket.CreatedAt` 优先尝试较早的票据；八票规则沿用
四票规则的配置，只修改规则身份、成组门槛和单局上限。RuleJSON 用内建 `createTime`
建索引和查询，投递票据时填写创建时间即可。评分预算和同时间票据的排序边界见
[模拟器试用步骤](../../doc/simulator/usage-guide.md#试用-fifo-规则)。
