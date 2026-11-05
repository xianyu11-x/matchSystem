# Fact Provider Descriptor

`LogicalNode` 在加载 `match-rule/v1` 时从已绑定 Provider 获取启动自描述能力。规则的
`contract.facts` 是规则侧定义；对应 Provider 通过进程内 `Describe()` 返回独立的
`ProviderDescriptor`，声明自己的稳定 ID、版本和可计算的 `Facts []fact.Spec`。Python
Provider 在 JSON-lines 启动握手中按实际能力返回 `Descriptors.tick/object/match`。两种实现都
在核心处进入同一套 Contract 覆盖校验；Provider 能力不会从 Contract 反向合成。

Go Provider 的入口是按作用域区分的强类型接口：`TickProvider`、`ObjectProvider` 和
`MatchProvider`。三者都必须同时提供业务回调和 `Describe()`；`LogicalNodeSpec` 的三个
字段分别使用对应接口，因此旧的函数形 Provider 不能隐式绕过自描述握手。需要迁移旧
回调时，应由宿主定义一个明确的 typed wrapper，并在同一个实现上提供 `Describe()`。

```go
type TickProvider interface {
    Provide(context.Context, matchsystem.TickFactInput) (matchsystem.Facts, error)
    Describe() (matchsystem.ProviderDescriptor, error)
}

func (provider) Describe() (matchsystem.ProviderDescriptor, error) {
    return matchsystem.ProviderDescriptor{
        ID:      "party-tick-facts",
        Version: "v1",
        Facts: []matchsystem.FactSpec{
            {Name: "waiting-count", Type: matchsystem.FactTypeInt64, Scope: matchsystem.FactScopeTick},
        },
    }, nil
}
```

`LogicalNodeSpec` 不再接收 `FactProviderDescriptor`、
`ObjectFactProviderDescriptor` 或 `MatchFactProviderDescriptor` 字段。Contract 声明了某个
scope 的 Fact 时，对应 Provider 必须存在并通过 `Describe()` 返回覆盖该 scope 的每个 Fact；
名称、类型、scope 和 `MaxValues` 必须严格匹配。Descriptor 还可以声明同一 Provider 能计算
但规则不使用的其他合法 Fact，这些额外项不会自动加入 Contract。错误可以通过
`var handshakeErr *matchsystem.ProviderHandshakeError; errors.As(err, &handshakeErr)` 识别，
并读取其 `Code`、`Provider`、`Scope`、`Name`、`Expected` 和 `Actual` 字段。

规则没有声明某个 scope 的 Fact 时，不要求该 scope 的 Provider；Provider 仍可以在自己的
Descriptor 中公开其它合法能力。Provider Descriptor 只在节点启动时校验，不改变 Provider
的运行时调用，也不会把运行时值校验器加入核心匹配热路径。Go 的 `Describe()` 是进程内
启动方法调用，不经过 JSON 序列化或 RPC；Python 的 `describe()` 通过 Worker 的 JSON-lines
握手适配，二者在核心统一校验能力覆盖。

模拟器 Scenario 只保存 `rules[*].pythonProvider` 的实现绑定和 `tickFacts` 静态值；不再
保存三个 Provider Descriptor 字段。Ticket 的 `objectFacts` 与成局记录的 `facts` 是运行时
观察值，这些字段不会反向生成 Descriptor。Descriptor 中的 `description`（如有）仅作附加
信息，Contract 的 `description` 才是规则文档元数据，握手不会比较描述文本。

Python Provider 将可用的 Tick、Object、Match 回调按 scope 绑定到一个服务端 JSON-lines Worker；脚本
必须在握手中通过 `describe()` 返回 `Descriptors`。返回值不写入 Scenario，Ready-only
响应不能从场景字段取得能力。场景替换时 Worker 先握手，失败则旧 runtime 保持运行；成功
替换或关闭模拟器时回收。运行态 Facts 查询中的 Descriptor `source` 为 `go` 或 `python`，
分别表示进程内 Go 自描述和 Python Worker 握手。字段和脚本协议见
[Python Provider 接入](../../simulator/python-provider.md)。

旧 JSON 中的 `factProviderDescriptor`、`objectFactProviderDescriptor`、
`matchFactProviderDescriptor` 以及 `pythonProvider.descriptorSource` 均是未知字段，
Schema/API 应拒绝它们；客户端必须迁移到 Provider 自描述，不能静默继续使用旧声明。
