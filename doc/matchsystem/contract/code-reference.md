# internal/matchsystem/contract 代码索引

## 1. 类型、常量和限制

FactType 是 Fact 值类型的公开别名，FactTypeStrings、FactTypeInt64、
FactTypeUint64s 是当前类型常量。IndexType 是索引类型枚举，
IndexTypeMultiValue、IndexTypeInt64Range 是当前支持的 IndexType 常量；
IndexSpec.Type 必须使用这些已知值。

来自 contract.go：

| 符号 | 说明 |
| --- | --- |
| SchemaVersion | logical-node-contract/v3，唯一接受的 wire 版本 |
| ScopeTick、ScopeObject、ScopeMatch | 指向 fact.Scope 的 scope 常量 |
| FactTypeStrings、FactTypeInt64、FactTypeUint64s | 指向 fact.Type 的类型常量 |
| IndexTypeMultiValue、IndexTypeInt64Range | 两种物理索引实现 |
| KeyTypeString、KeyTypeUint64 | multi_value key 类型 |
| IndexSpec | Type、Name、KeyType、文档/查询上限 |
| Limits | 10 个 JSON/声明资源上限 |
| Contract | Attributes、Facts、Indexes、Limits 的快照模型 |

IndexSpec.Name 是三重身份：Attribute 名、index 名和 Prefilter 查询中的 index token。
Int64 range 的 KeyType、MaxDocumentValues、MaxQueryValues 必须保持零。

## 2. 公共函数和方法

| API | 语义 |
| --- | --- |
| DefaultLimits() | 返回默认有界限制；不返回共享可变状态 |
| (Contract).Validate() | 校验 typed Contract，返回结构化 compile 错误 |
| (Contract).FactSpecs() | 返回 Fact 声明的切片副本 |
| (Contract).AttributeSpecs() | 返回 Attribute 声明的切片副本 |
| (Contract).Clone() | 复制三个声明切片与 limits |
| Parse(data, limits) | 严格解析 logical-node-contract/v3 并应用 limits |
| (Contract).CompileAttributeValidator() | 为 Ticket Attribute 命名空间创建不可变 validator |

## 3. AttributeValidator

attribute_validator.go 中的 AttributeValidator 只检查 Ticket 当前实际出现的字段：

- Ticket 不能为 nil；
- 每个字段名必须已声明；
- 字段必须出现在对应类型 map；
- 集合值数量不能超过 Attribute MaxValues；
- TicketID、CreatedAt 属于元数据，不参与 Attribute 命名空间校验。

它不要求每个声明的 Attribute 都在每个 Ticket 出现；表达式在运行时引用缺失字段时
会由对应 Lookup 返回 missing error。Prefilter 的 IndexStore 在此校验之后再复制 Ticket
并执行 index-specific 文档值上限。

## 4. JSON 错误边界

Error 的字段为：

~~~go
type Error struct {
    Phase string // json 或 compile
    Path  string
    Code  string
    Err   error
}
~~~

常见 Code 有 UNKNOWN_SCHEMA_VERSION、UNKNOWN_FIELD、NULL_FIELD、
DUPLICATE_ATTRIBUTE、DUPLICATE_FACT、DUPLICATE_INDEX、DUPLICATE_NAME、
MISSING_ATTRIBUTE、ATTRIBUTE_TYPE_MISMATCH、INVALID_FACT_SCOPE、INVALID_KEY_TYPE
和 UNKNOWN_INDEX_TYPE。调用方应按 errors.As(err, *contract.Error) 读取结构化字段，
不依赖文本。

实现链接：[contract.go](../../../internal/matchsystem/contract/contract.go)、
[attribute_validator.go](../../../internal/matchsystem/contract/attribute_validator.go)。
