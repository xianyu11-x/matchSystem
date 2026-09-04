# internal/matchsystem/jsonstrict 架构说明

jsonstrict 是 Contract、Expression、Prefilter 和 Evaluation 共用的领域无关 JSON 结构
闸门。它只验证输入是否安全、完整和受资源限制，不知道任何业务字段、schema version、
表达式 op 或 Contract 类型。

## 1. 检查范围

~~~text
[]byte
  -> 最大字节数
  -> UTF-8 合法性
  -> JSON token/括号完整性
  -> 深度、对象字段、数组元素、primitive value、字符串字节上限
  -> 对象重复 key
  -> 尾随第二个 JSON value
  -> jsonstrict.Error{Path, Code, Err}
~~~

null 作为任意值会被拒绝；结构检查在遇到 null、非法 JSON、第二个顶层值时立即返回。
对象 key 和 string value 都会检查 MaxStringBytes。MaxValues 统计 primitive token，不
等同于数组长度；MaxArrayItems 专门限制每个数组元素数。

## 2. 与领域解析器的边界

| jsonstrict 负责 | 上层包负责 |
| --- | --- |
| bytes/UTF-8、重复 key、尾随 value | schemaVersion 和顶层字段 |
| 深度和结构闭合 | 字段类型、null 语义（领域差异） |
| 对象/数组/value/string 预算 | Contract、expression op、索引和 source |
| 稳定 Path/Code | 将 Error 适配成各包 Error |

Contract/Expression/Prefilter/Evaluation 先调用本包，再把 Error 映射到自己的
Phase/Path/Code；本包不吞错，也不提供兼容旧 schema 的解析器。

## 3. 并发和资源

ValidateWithOptions 是无状态函数，每次创建自己的 decoder 和计数器，可由外层 owner
调用。它不保存输入、不会修改 data，也不提供全局配置。options 字段为零表示对应
限制不限；上层通常会先把零值替换为安全默认，再传入本包。
