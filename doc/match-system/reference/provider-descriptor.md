# Fact Provider Descriptor

`LogicalNode` 在加载 `match-rule/v1` 时会对每个 Fact scope 执行一次启动握手。规则的 `contract.facts` 是规则侧定义；对应 Provider 通过独立的 `ProviderDescriptor` 声明自己的稳定 ID、版本和可计算的 `Facts []fact.Spec`。两者必须在 Scenario 中分别显式提供，不能用规则 Contract 或模拟器运行时值替代 Descriptor。

```go
spec := matchsystem.LogicalNodeSpec{
    Key:          key,
    RuleJSON:     ruleJSON,
    FactProvider: tickProvider,
    FactProviderDescriptor: &matchsystem.ProviderDescriptor{
        ID:      "party-tick-facts",
        Version: "v1",
        Facts: []matchsystem.FactSpec{
            {Name: "waiting-count", Type: matchsystem.FactTypeInt64, Scope: matchsystem.FactScopeTick},
        },
    },
}
```

`LogicalNodeSpec` 对 Tick、Object、Match Provider 分别提供 `FactProviderDescriptor`、`ObjectFactProviderDescriptor` 和 `MatchFactProviderDescriptor`。Contract 声明了某个 scope 的 Fact 时，Provider 和 Descriptor 都必须存在；Descriptor 必须覆盖 Contract 中该 scope 的每个 Fact，并对每个使用项严格匹配名称、类型、scope 和 `MaxValues`。Descriptor 还可以声明同一 Provider 能计算但规则不使用的其他合法 Fact，这些额外项不会自动加入 Contract。错误可以通过 `var handshakeErr *matchsystem.ProviderHandshakeError; errors.As(err, &handshakeErr)` 识别，并读取其 `Code`、`Provider`、`Scope`、`Name`、`Expected` 和 `Actual` 字段。

规则没有声明某个 scope 的 Fact 时，不要求 Provider 或 Descriptor；为保持兼容，调用方仍可传入 Provider。该 scope 显式提供的非空 Descriptor 也可以作为 Provider 能力目录保留，只要其中的 Fact 声明自身合法；它们不会形成 Contract 义务。Provider Descriptor 只在节点启动时校验，不会改变 Provider 的运行时调用，也不会把运行时值校验器加入核心匹配热路径。

模拟器 Scenario 对应的 JSON 字段是 `factProviderDescriptor`（Tick）、
`objectFactProviderDescriptor`（Object）和 `matchFactProviderDescriptor`（Match）。
`tickFacts` 是模拟器静态 Tick 值，Ticket 的 `objectFacts` 与成局记录的 `facts` 是
运行时观察值；这些字段不会反向生成 Descriptor。Descriptor 中的 `description`（如有）
仅作附加信息，Contract 的 `description` 才是规则文档元数据，握手不会比较描述文本。
