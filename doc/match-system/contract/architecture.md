# internal/matchsystem/contract 架构说明

contract 是当前匹配系统唯一的业务 Contract 实现，接受严格的
logical-node-contract/v3 JSON。Prefilter、Evaluation、Fact validator 都从同一份
Contract 建立自己的防御性快照；包不拥有运行时 Ticket、索引 posting 或表达式执行。

## 1. Contract 的组成

~~~text
Contract
├── Attributes: Ticket 可携带的 strings / uint64s / int64 字段
├── Facts: tick / object / match 三个生命周期 scope
├── Indexes: 与 Attribute 同名的 multi_value 或 int64_range 物理索引声明
└── Limits: JSON、声明数量和每字段值数量的边界
~~~

Attribute 和 Fact 名称在整个 Contract 中全局唯一；Index 的 Name 同时是被索引的
Attribute 名、物理索引标识和 Prefilter JSON 的 index 引用，不存在单独的 field
字段。一个 Attribute 最多声明一个 Index。

## 2. 类型和 scope

支持的值类型与限制：

| 类型 | Go 值 map | MaxValues |
| --- | --- | --- |
| strings | []string | 必须为正数 |
| uint64s | []uint64 | 必须为正数 |
| int64 | int64 | 必须为零/不声明 |

Fact scope 的产生和读取边界：

| scope | 产生位置 | 可读取阶段 |
| --- | --- | --- |
| tick | FactProvider，一次 ProduceMatch | Prefilter、CanJoin、CanComplete |
| object | ObjectFactProvider，按 Ticket/调用缓存 | seed/candidate 的 Prefilter/Evaluation |
| match | MatchFactProvider.Initialize/OnJoin | CanJoin、CanComplete |

表达式 profile 会继续收紧 source capability；scope 不会因为缺失值而自动 fallback 到
其它 Fact 层。

## 3. 校验与不变性

Contract.Validate 先归一化零值 limits，再检查声明数量、名称、类型、重复名称、
scope、MaxValues、索引存在性和索引类型匹配。Multi-value Index 必须同时满足
keyType、maxDocumentValues、maxQueryValues；Int64 range Index 不接受这些
字段，只能引用 int64 Attribute。

Parse 的顺序是：先验证 JSON 字节/深度/字符串/重复 key/尾随值，再门控
schemaVersion，拒绝未知字段/null/错误结构，解析 Attribute/Fact/Index，最后调用
Validate。只接受 logical-node-contract/v3，不会解释旧版本字段。

Clone、FactSpecs、AttributeSpecs 和下游编译器均返回切片副本；调用方通过 Parse/Validate
后不应再把原始切片当作可变运行时配置。

## 4. 限制快照

DefaultLimits 的默认值：

| 字段 | 默认值 |
| --- | ---: |
| MaxBytes | 1 MiB |
| MaxDepth | 64 |
| MaxChildren | 128 |
| MaxStringBytes | 1024 |
| MaxIndexes | 128 |
| MaxAttributes / MaxFacts | 256 / 256 |
| MaxValues | 10000 |
| MaxDocumentValues / MaxQueryValues | 256 / 256 |

传入的非零值覆盖默认；负数无效。Prefilter/Evaluation 可进一步收紧表达式 JSON 限制，
但不能放宽 Contract 的重叠边界。

## 5. 依赖方向

~~~text
jsonstrict -> Contract.Parse
fact       -> FactSpec/Scope/Type
expression -> AttributeSpec/标量 profile
contract   -> LogicalNode / Prefilter / Evaluation
~~~

Contract 不反向依赖 Prefilter 或 Evaluation；新索引类型或新表达式节点应在各自领域实现，
不能通过修改 Contract 偷渡第二套运行时模型。

实现入口：[contract.go](../../../internal/matchsystem/contract/contract.go)、
[attribute_validator.go](../../../internal/matchsystem/contract/attribute_validator.go)。
