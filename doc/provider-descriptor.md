# Fact Provider Descriptor

`LogicalNode` 在加载 `match-rule/v1` 时会对每个 Fact scope 执行一次启动握手。规则的 `contract.facts` 是事实来源；对应 Provider 通过 `ProviderDescriptor` 声明自己的稳定 ID、版本和完整 `Facts []fact.Spec`。

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

`LogicalNodeSpec` 对 Tick、Object、Match Provider 分别提供 `FactProviderDescriptor`、`ObjectFactProviderDescriptor` 和 `MatchFactProviderDescriptor`。声明了某个 scope 的 Fact 时，Provider 和 Descriptor 都必须存在，Descriptor 中的名称、类型、scope、`MaxValues` 必须与规则完全一致。错误可以通过 `var handshakeErr *matchsystem.ProviderHandshakeError; errors.As(err, &handshakeErr)` 识别，并读取其 `Code`、`Provider`、`Scope`、`Name`、`Expected` 和 `Actual` 字段。

规则没有声明某个 scope 的 Fact 时，不要求 Provider 或 Descriptor；为保持兼容，调用方仍可传入 Provider。该 scope 的 Descriptor 如果列出了 Fact，会以 `EXTRA_FACT` 失败。Provider Descriptor 只在节点启动时校验，不会改变 Provider 的运行时调用，也不会把运行时值校验器加入核心匹配热路径。
