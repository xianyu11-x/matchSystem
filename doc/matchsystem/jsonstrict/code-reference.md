# internal/matchsystem/jsonstrict 代码索引

## 1. 公共 API

来自 jsonstrict.go：

~~~go
type Options struct {
    MaxBytes        int
    MaxDepth        int
    MaxObjectFields int
    MaxArrayItems   int
    MaxValues       int
    MaxStringBytes  int
}

type Error struct {
    Path string
    Code string
    Err  error
}

func ValidateWithOptions(data []byte, options Options) error
~~~

Options 的零值表示对应维度不限制；负数不会被本包统一转换为默认值，通常应由调用
方在计算有效 limits 时先拒绝负数。

## 2. Error Code 与路径

Error.Error 只用于展示；调用方可用 errors.As(err, *jsonstrict.Error) 读取：

| Code | 触发条件 |
| --- | --- |
| JSON_SIZE_LIMIT | data 超过 MaxBytes |
| INVALID_UTF8 | data 不是合法 UTF-8 |
| INVALID_JSON | token、括号或 decoder 读取失败 |
| NULL_NOT_ALLOWED | 任意值为 null |
| DUPLICATE_KEY | 同一 object 重复 key |
| TRAILING_JSON | 根值之后还有第二个 JSON value |
| DEPTH_LIMIT | 嵌套深度超过 MaxDepth |
| OBJECT_FIELD_LIMIT | 一个 object 字段超过 MaxObjectFields |
| ARRAY_ITEM_LIMIT | 一个数组元素超过 MaxArrayItems |
| VALUE_LIMIT | primitive token 总数超过 MaxValues |
| STRING_SIZE_LIMIT | key/value 字符串超过 MaxStringBytes |

Path 使用 $、$.field、$.array[0] 等形式；重复 key 的路径包含重复 key 名称。

## 3. 实现分层

ValidateWithOptions 先做字节限制和 UTF-8 检查，再以 UseNumber 的 json.Decoder
流式扫描。scanValue 在每个 token 进入时检查深度与 primitive 计数，递归处理对象/数组
并验证关闭 token。顶层扫描完成后再读取一次 token，确保没有尾随 JSON。

byteReader 是包内极小的 io.Reader，仅为 decoder 提供内存 byte slice；它不依赖其它
matchsystem 包，从而保持本包可复用且不产生领域循环依赖。

实现链接：[jsonstrict.go](../../../internal/matchsystem/jsonstrict/jsonstrict.go)。
