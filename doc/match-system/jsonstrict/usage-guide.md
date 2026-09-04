# internal/matchsystem/jsonstrict 使用指南

## 1. 基本调用

~~~go
err := jsonstrict.ValidateWithOptions(data, jsonstrict.Options{
    MaxBytes:        1 << 20,
    MaxDepth:        64,
    MaxObjectFields: 64,
    MaxArrayItems:   10000,
    MaxValues:       10000,
    MaxStringBytes:  1024,
})
if err != nil {
    var structural *jsonstrict.Error
    if errors.As(err, &structural) {
        log.Printf("reject %s at %s: %v", structural.Code, structural.Path, structural.Err)
    }
    return err
}
~~~

data 不会被修改；函数不保存 decoder 或计数状态。可把 MaxBytes 等设为零表示不限制，
但对外暴露的配置边界通常应使用上层安全默认，不建议直接放开。

## 2. 与领域解析组合

推荐顺序：

1. 计算领域的有效限制并拒绝负数；
2. 调用 ValidateWithOptions 做结构闸门；
3. 按领域规则检查 schemaVersion、未知字段、必填字段和 null；
4. 解析 typed DTO，再执行语义校验。

jsonstrict 不会识别 expression op、Contract index 或 Prefilter bitmap；不要期待
Unknown Field、root 类型或业务 scope 错误由本包返回。

## 3. 测试边界

~~~go
cases := [][]byte{
    []byte(`{"x":1,"x":2}`), // DUPLICATE_KEY
    []byte(`{"x":1} {"y":2}`), // TRAILING_JSON
    []byte(`null`),          // NULL_NOT_ALLOWED
}
for _, data := range cases {
    if err := jsonstrict.ValidateWithOptions(data, jsonstrict.Options{}); err != nil {
        var structural *jsonstrict.Error
        if errors.As(err, &structural) {
            _ = structural.Code
        }
    }
}
~~~

领域包可以把 jsonstrict.Error 转换为自己的 Error，同时保留 Path 和 Code；不要丢失
结构化错误或改成默认值。

实现参考：[jsonstrict.go](../../../internal/matchsystem/jsonstrict/jsonstrict.go)、
[Contract 解析](../../../internal/matchsystem/contract/contract.go)。
