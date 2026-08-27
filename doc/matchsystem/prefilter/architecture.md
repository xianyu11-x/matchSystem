# internal/matchsystem/prefilter 架构说明

Prefilter 是严格的索引驱动候选初筛层。当前工作树实现接受 prefilter/v3 envelope，
自己拥有闭合 Bitmap tree、查询 sidecar、物理索引和 Roaring Bitmap 执行；values/min/max/
when 内嵌的标量文档交给 shared expression-scalar/v3 编译为不透明 ScalarProgram。
归档 pre-v3 文档中的 Builder、Arena、Bitmap Root、Program() 和 DomainDescriptors API
不属于当前实现。

## 1. 分层

~~~text
Contract (logical-node-contract/v3)
       -> NewJSONCompiler / CompileJSON
       -> private bitmap parser/compiler
            ├── private bitmapNode topology
            ├── opaque expression.ScalarProgram operands
            ├── bitmapQuery sidecars + Requirements
            └── immutable Plan + fingerprint(v5)
       -> IndexStore
            ├── Active DocID bitmap
            └── multi_value / int64_range postings
       -> BeginTick -> TickSession.Candidates
            └── DocSet (candidate DocIDs)
~~~

Prefilter 只产生候选 DocID；评分、CanJoin、CanComplete、Match Fact、Ticket 生命周期
和最终提交由根包 LogicalNode 负责。索引缺失时不扫描 Ticket 作为兜底。

## 2. 编译和 JSON 边界

NewJSONCompiler(schema, optional JSONLimits) 复制并校验 Contract，建立有效限制。
Compile 依次执行：

1. jsonstrict 验证字节、UTF-8、重复 key、尾随值、深度和资源边界；
2. 门控 schemaVersion，只接受 prefilter/v3；
3. 解析 bitmap.resultType=bitmap、runtime.containsProbeThreshold 和 bitmap.expr；
4. 将 private Bitmap op 解析为节点，将每个 scalar operand 以 expression-scalar/v3
   编译，并校验 source、类型、Fact scope、节点预算和索引 query 上限；
5. 分析 Bitmap lattice，拒绝无 scope 起点、无 anchor、非法 Exclude、嵌套 Exclude
   或 cycle；
6. 收集实际 Index/Fact/Attribute Requirements，建立 validator、物理 slot 和 Plan；
7. 用 prefilter-fingerprint/v5、Contract、规范化树、有效限制、probe 参数生成 SHA-256
   十六进制指纹。

静态 literal/union operand 会在编译期求值并内联；含依赖的 operand 保留 ScalarProgram，
运行时以当前 seed/Fact 绑定，不保存 Bitmap 或第二个执行树。

## 3. Bitmap lattice 与执行语义

| op | 运行时语义 | 编译约束 |
| --- | --- | --- |
| none | 空 Bitmap | 合法静态空结果 |
| and | 子结果求交 | 无 scope 时选有 anchor 的最小估计子树 |
| or | 子结果求并 | 已覆盖传入 scope 时可短路 |
| exclude | scope AND-NOT child | 必须有输入正向 scope；child scope-free；不能嵌套 |
| if | 求值 Bool 后只执行 then/else | 两个分支都必须在同一 lattice 合法 |
| lookup_string | multi_value/string key 的 OR posting | query values 为 strings scalar |
| lookup_uint64 | multi_value/uint64 key 的 OR posting | query values 为 uint64s scalar |
| lookup_range | int64 闭区间 posting | min/max 为 int64 scalar，运行时 min<=max |

Root 除 static none 外必须 scope-free 且 establishes-scope；因此单独 exclude、无法
建立 anchor 的 and/or 会在编译期拒绝。Static none 是 AND 的吸收元，执行器会在选择
错误 scope-requiring sibling 前直接返回空集。

## 4. 运行时所有权

IndexStore、TickSession 和 DocSet 均不提供并发保护。owner goroutine 必须串行调用
Add/Remove、BeginTick 和 Candidates；TickSession 只借用本轮 Tick Facts，不得跨 mutation
barrier 使用。IndexStore Add 会复制并保存 Contract 校验后的 Ticket snapshot，既供
posting 也供 seed scalar lookup；Remove 同时清理 posting、Active 和 snapshot。

小 scope（默认 4096）逐 DocID contains；大 scope 直接 lookup posting 后与 scope 相交。
Int64 range 只在 BeginTick prepare 时对 distinct values 排序，查询使用二分而不是扫描
数值宽度。Prefilter 不读取完整 Ticket 返回值，也不暴露内部 Roaring 指针。

## 5. 错误与计划身份

Error 统一含 Phase、Path、Code、Err。json 阶段拒绝结构/版本/字段/资源错误；compile
阶段拒绝 Contract、索引类型、query result、scope lattice 和预算错误；evaluate 阶段
拒绝 inactive seed、Fact scope、动态 query 超限、缺失值、range 反转和索引调用错误。
错误不会转成 none、universe 或全池扫描。

Plan 只读保存 topology、sidecar、索引 specs、Fact/Attribute validator、有效 probe
threshold、Requirements 和 fingerprint。运行时 Ticket/Fact 值、候选集合、Provider 和
Scorer 不进入 fingerprint。

实现入口：[doc.go](../../../internal/matchsystem/prefilter/doc.go)、
[compiler.go](../../../internal/matchsystem/prefilter/compiler.go)、
[expression.go](../../../internal/matchsystem/prefilter/expression.go)、
[store.go](../../../internal/matchsystem/prefilter/store.go)。
