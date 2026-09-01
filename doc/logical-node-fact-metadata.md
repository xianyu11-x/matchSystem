# LogicalNode Fact 元数据

每个 `Fact` 的 `Spec` 都可以携带可选的 `description` 字段，用于说明该
Fact 的业务含义：

```json
{
  "name": "party-size",
  "type": "int64",
  "scope": "match",
  "description": "当前匹配组中的玩家数量"
}
```

`description` 是文档元数据，不属于 Provider 握手契约。LogicalNode 启动
握手仍只比较 Fact 的名称、类型、scope 和 `maxValues`；Provider 和规则可以
使用不同的描述文案而不会导致启动失败。规则 Contract 是描述的权威来源，
ProviderDescriptor 中的描述仅用于展示。

## 核心查询接口

核心层提供按 LogicalNode 标识查询的快照接口：

```go
facts, err := physicalNode.FactSpecs(ctx, key)
```

`LogicalNode.FactSpecs`、`PhysicalNode.FactSpecs` 和
`Simulator.FactSpecs` 都返回独立切片，调用方可以修改返回值而不会影响
运行中的规则或节点。LogicalNode 不存在时返回对应的 not-found 错误。

## HTTP 接口

内置 Simulator API 提供：

```text
GET /api/v1/logical-nodes/facts?ruleNamespace=ranked&ruleId=1&placementId=sea-1
```

`ruleId` 和 `placementId` 必填，`ruleNamespace` 在 RuleID 唯一时可以省略。
如果同一个 RuleID 在多个 namespace 下存在而未提供 namespace，接口返回
`409 LOGICAL_NODE_AMBIGUOUS`，要求调用方补充 namespace。
成功响应示例：

```json
{
  "logicalNode": {
    "rule": {"namespace": "ranked", "ruleId": 1},
    "placementId": "sea-1"
  },
  "facts": [
    {
      "name": "party-size",
      "type": "int64",
      "scope": "match",
      "description": "当前匹配组中的玩家数量"
    }
  ]
}
```

HTTP 返回的 `type` 和 `scope` 是字符串，避免调用方依赖核心的数值枚举。
查询不存在的 LogicalNode 返回 `404 LOGICAL_NODE_NOT_FOUND`；缺少或非法查询
参数返回 `400 INVALID_QUERY`。未实现 `LogicalNodeFactService` 的自定义 HTTP
Service 返回 `501 NOT_IMPLEMENTED`。
模拟器 HTTP 查询还会明确返回独立的 `contractFacts`、
`providerDescriptors.tick/object/match` 和 `runtimeFacts.tick`。旧的 `facts` 字段
仍是 `contractFacts` 的兼容别名。Provider Descriptor 是 Scenario 中显式配置的
握手声明，`runtimeFacts` 是模拟器的运行时值，二者不会互相推导。
