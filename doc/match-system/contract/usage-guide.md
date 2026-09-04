# internal/matchsystem/contract 使用指南

## 1. 直接构造 typed Contract

内部代码可直接构造 Contract，但交给 Prefilter/Evaluation 前必须调用 Validate；生产
节点配置仍应从 JSON 进入。

~~~go
schema := contract.Contract{
    Attributes: []contract.AttributeSpec{
        {Name: "mode", Type: fact.TypeStrings, MaxValues: 4},
        {Name: "rating", Type: fact.TypeInt64},
    },
    Facts: []fact.Spec{
        {Name: "capacity", Type: fact.TypeInt64, Scope: fact.ScopeTick},
        {Name: "tier", Type: fact.TypeInt64, Scope: fact.ScopeObject},
        {Name: "count", Type: fact.TypeInt64, Scope: fact.ScopeMatch},
    },
    Indexes: []contract.IndexSpec{
        {Type: contract.IndexTypeMultiValue, Name: "mode",
         KeyType: contract.KeyTypeString, MaxDocumentValues: 4, MaxQueryValues: 4},
        {Type: contract.IndexTypeInt64Range, Name: "rating"},
    },
}
if err := schema.Validate(); err != nil {
    return err
}
~~~

Index Name 必须与 Attribute 同名；不能再填写旧 Contract 中的 Field 字段。字符串和
uint64 集合必须声明正的 MaxValues，int64 则不能声明该字段。

## 2. 解析 v3 JSON

~~~go
raw := []byte(`{
  "schemaVersion":"logical-node-contract/v3",
  "attributes":[
    {"name":"mode","type":"strings","maxValues":4},
    {"name":"rating","type":"int64"}
  ],
  "facts":[
    {"name":"capacity","type":"int64","scope":"tick"},
    {"name":"count","type":"int64","scope":"match"}
  ],
  "indexes":[
    {"type":"multi_value","name":"mode","keyType":"string",
     "maxDocumentValues":4,"maxQueryValues":4},
    {"type":"int64_range","name":"rating"}
  ]
}`)
schema, err := contract.Parse(raw, contract.DefaultLimits())
if err != nil {
    var structured *contract.Error
    if errors.As(err, &structured) {
        log.Printf("%s %s", structured.Code, structured.Path)
    }
    return err
}
frozen := schema.Clone()
~~~

顶层 attributes、facts、indexes 必须存在且为数组；limits 可选。未知字段、重复键、
尾随 JSON、null、非法 UTF-8 和旧版本都会被拒绝。

## 3. 为 Ticket 建立 Attribute validator

~~~go
validator, err := schema.CompileAttributeValidator()
if err != nil { return err }

ticket := &common.Ticket{
    TicketID: 7,
    StringLists: map[string][]string{"mode": {"ranked"}},
    Int64Values: map[string]int64{"rating": 1200},
}
if err := validator.ValidateTicket("ticket", ticket); err != nil {
    return err
}
~~~

validator 只验证出现的字段；缺失字段是否允许由业务流程决定，表达式真正读取缺失值时
会返回 evaluate 阶段的 MISSING_VALUE。Validator 编译后不可变，可在 owner 内复用。

## 4. 与下游编译器衔接

~~~go
prefilterCompiler, err := prefilter.NewJSONCompiler(schema)
if err != nil { return err }
prefilterPlan, err := prefilterCompiler.Compile(prefilterJSON)

predicates, err := evaluation.CompileJSON(evaluationJSON, schema)
if err != nil { return err }
_ = prefilterPlan
_ = predicates
~~~

两个下游都应使用同一份已 Validate 的 Contract；它们各自 Clone，不会共享可变切片。
修改原始 typed 切片不会改变已经编译的计划。

## 5. 限制与错误处理

需要更小的边界时可填写 Limits 的非零字段；不能用负数或零来表示“取消安全限制”。
Parse 和 Validate 的错误均携带 Phase/Path/Code/Err，应使用结构化 Code 处理升级、日志
和配置拒绝。
