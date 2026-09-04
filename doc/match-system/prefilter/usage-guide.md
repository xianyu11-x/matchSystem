# internal/matchsystem/prefilter 使用指南

## 1. 准备 Contract 并编译计划

Prefilter 使用同一份 contract.Contract；索引 Name 同时是 Attribute 名和查询 index。示例：

~~~go
schema := contract.Contract{
    Attributes: []contract.AttributeSpec{
        {Name: "partition", Type: fact.TypeStrings, MaxValues: 2},
        {Name: "rating", Type: fact.TypeInt64},
    },
    Indexes: []contract.IndexSpec{
        {Type: contract.IndexTypeMultiValue, Name: "partition",
         KeyType: contract.KeyTypeString, MaxDocumentValues: 2, MaxQueryValues: 4},
        {Type: contract.IndexTypeInt64Range, Name: "rating"},
    },
}
if err := schema.Validate(); err != nil { return err }

planJSON := readPlanFile("prefilter.json")
plan, err := prefilter.CompileJSON(planJSON, schema)
if err != nil { return err }
fmt.Println(plan.Fingerprint())
~~~

对应的 prefilter.json 内容如下：

~~~json
{
  "schemaVersion":"prefilter/v3",
  "bitmap":{"resultType":"bitmap","expr":{
    "op":"and","children":[
      {"op":"lookup_string","index":"partition","values":{
        "schemaVersion":"expression-scalar/v3","resultType":"strings",
        "expr":{"op":"strings_ref","source":"seed_attributes","name":"partition"}
      }},
      {"op":"lookup_range","index":"rating",
        "min":{"schemaVersion":"expression-scalar/v3","resultType":"int64",
          "expr":{"op":"int64_literal","value":1000}},
        "max":{"schemaVersion":"expression-scalar/v3","resultType":"int64",
          "expr":{"op":"int64_literal","value":2000}}}
    ]
  }},
  "runtime":{"containsProbeThreshold":4096}
}
~~~

Go 中也可以将上面的 JSON 放入 []byte 合法字符串或嵌入文件读取。values、
min、max 都是完整 expression-scalar/v3 文档。lookup_string 只能匹配 string multi_value，
lookup_uint64 只能匹配 uint64 multi_value，lookup_range 只能匹配 int64_range。

## 2. 建立 Store 并维护 Ticket

~~~go
store, err := prefilter.New(plan)
if err != nil { return err }

blue := &common.Ticket{
    TicketID: 1,
    CreatedAt: 10,
    StringLists: map[string][]string{"partition": {"blue"}},
    Int64Values: map[string]int64{"rating": 1500},
}
if err := store.Add(1, blue); err != nil { return err }

green := &common.Ticket{
    TicketID: 2,
    StringLists: map[string][]string{"partition": {"green"}},
    Int64Values: map[string]int64{"rating": 1500},
}
if err := store.Add(2, green); err != nil { return err }
fmt.Println(store.Len())
store.Remove(2)
~~~

Add 要求非零且唯一 DocID、非 nil Ticket；会先校验 Contract Attribute，再复制 Ticket，
然后检查索引文档值上限并一次写入所有 posting、Active bitmap 和 seed snapshot。调用方
之后修改 blue 不会改变索引或表达式读取。Remove 同步删除全部物理状态；未知 DocID
返回 false。

## 3. 开始 Tick 并查询候选

~~~go
session, err := store.BeginTick(prefilter.Facts{})
if err != nil { return err }

seed := &common.Ticket{
    TicketID: 1,
    StringLists: map[string][]string{"partition": {"blue"}},
}
candidates, err := session.Candidates(1, seed, prefilter.Facts{})
if err != nil { return err }
candidates.Remove(1) // Prefilter 不自动排除 seed，通常由上层移除
ids := candidates.IDs()
~~~

BeginTick 刷新 int64 range 的 distinct value 目录；返回的 session 借用可信 Tick Values。
Candidates 要求 seedDocID 仍 active、seed TicketID 与存储快照一致，并消费可信 seed
Facts。Prefilter 的 seed_attributes 实际读取 Add 时保存的 snapshot，而不是调用方后来
传入的 mutable Ticket。Fact schema/type/scope/值上限由 Provider 契约测试负责，生产执行
不重复校验。

## 4. 统计执行路径

~~~go
set, stats, err := session.CandidatesWithStats(seedDocID, seed, seedFacts)
if err != nil { return err }
fmt.Printf("n=%d lookup=%d contains=%d and=%d or=%d subtract=%d\n",
    set.Count(), stats.LookupCalls, stats.ContainsCalls, stats.AndCalls,
    stats.OrCalls, stats.SubtractCalls)
~~~

小 scope（不超过 containsProbeThreshold，默认 4096）逐 DocID 调用 contains；大 scope
使用 posting lookup 后与当前 scope 相交。And 无输入 scope 时会估算有 anchor 的子树并
先求最小候选；Or 可在覆盖输入 scope 时短路；Exclude 需要正向 scope；If 只求值并执行
被选中的分支。

## 5. 动态 query 与静态优化

若要使用下面的 seed_facts 示例，Contract 还必须声明
extraPartitions 为 strings、scope 为 object 的 Fact（并设置正的 MaxValues）；它不能
只声明为 Attribute 或省略。示例展示的是查询形状，不会改变 Prefilter 只读取
seed_attributes、seed_facts、tick_facts 的权限。

~~~json
{
  "schemaVersion":"prefilter/v3",
  "bitmap":{"resultType":"bitmap","expr":{
    "op":"lookup_string","index":"partition","values":{
      "schemaVersion":"expression-scalar/v3","resultType":"strings",
      "expr":{"op":"strings_union","items":[
        {"op":"strings_ref","source":"seed_attributes","name":"partition"},
        {"op":"strings_ref","source":"seed_facts","name":"extraPartitions"}
      ]}
    }
  }}
}
~~~

Prefilter scalar profile 只允许 seed_attributes、seed_facts、tick_facts；Object Fact 的
scope 必须可从 seed_facts 读取。静态 literal/union 会编译期排序去重并内联；含 source
依赖的 operand 会在每次 Candidates 绑定当前 Facts，并再次检查 MaxQueryValues。运行时
query 产生超过上限或 range min > max 时返回 evaluate error。

## 6. DocSet 和 owner 约定

DocSet 是调用方拥有的可变 uint32 集合，可用 Count、IsEmpty、Contains、Clone、Subtract、
IDs、ForEach、Add、Remove。IDs/ForEach 按升序；它不提供 Ticket 读取，也不应把内部集合
指针跨 owner 保存。

IndexStore、TickSession 和 Plan 没有并发保护；必须由同一个 LogicalNode owner goroutine
串行执行 Add/Remove、BeginTick、Candidates，并在下一次 mutation 前结束 TickSession。
Prefilter 不负责 scorer、CanJoin、CanComplete、Match Fact 或 fallback 全池扫描。

## 7. 错误处理

用 errors.As(err, *prefilter.Error) 读取 Phase、Path、Code、Err。典型错误：

- compile：MISSING_INDEX、QUERY_INDEX_MISMATCH、QUERY_KEY_CONTRACT、INVALID_BITMAP、
  UNKNOWN_OP、EXCLUDE 无 scope；
- evaluate：INVALID_TICK_SESSION、NIL_TICKET、INACTIVE_SEED、SEED_TICKET_MISMATCH、
  QUERY_KEY_LIMIT、INVALID_RANGE、INDEX_LOOKUP；
- json：UNKNOWN_SCHEMA_VERSION、UNKNOWN_FIELD、NULL_NOT_ALLOWED、DEPTH_LIMIT 等。

错误不会被转换为空集或全量候选。实现参考：[compiler.go](../../../internal/matchsystem/prefilter/compiler.go)、
[store.go](../../../internal/matchsystem/prefilter/store.go) 和
[cmd/app/main.go](../../../cmd/app/main.go)。
