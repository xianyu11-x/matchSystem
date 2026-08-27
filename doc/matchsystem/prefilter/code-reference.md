# internal/matchsystem/prefilter 代码索引

范围是当前生产源码。当前版本是 prefilter/v3；归档文档中的 builder.go、typed Arena
Root、Plan.Program 和 DomainDescriptors 等符号已移除，不能作为接入 API。

## 0. 包边界

`doc.go` 的包注释固定职责：严格索引初筛、闭合 Bitmap 语言、编译期物理依赖、
Roaring Bitmap 索引和单 goroutine owner。Prefilter 不扫描 candidate Ticket 作为兜底，
也不负责评分、最终组正确性或 Match 提交。

## 1. JSON 与计划

| 文件/符号 | 说明 |
| --- | --- |
| json.go: JSONSchemaVersion | prefilter/v3 |
| json.go: DefaultJSONLimits | 返回 expression.DefaultJSONLimits |
| json.go: JSONCompiler | 固化一份 Contract 和有效 limits |
| json.go: NewJSONCompiler | 校验/复制 Contract，可选 limits 只能收紧边界 |
| json.go: CompileJSON | 单次创建 compiler 并编译 |
| json.go: JSONCompiler.Compile | 解析 envelope 并返回 immutable Plan |
| plan.go: Fingerprint | Prefilter 计划身份字符串 |
| plan.go: Plan.Fingerprint | 返回指纹；nil plan 返回空串 |
| plan.go: Plan.Requirements | 返回 Index/Fact/Attribute 的防御性副本 |

Plan 没有 Program()、Arena() 或公开 Bitmap tree；nodes、queries、index slots 和
validators 均为私有。

## 2. Bitmap 编译器

compiler.go 的 private bitmapCompiler/bitmapCompileState 负责：

- parseBitmapNode：解析 none、and、or、exclude、if、lookup_string、lookup_uint64、
  lookup_range；
- compileScalar：调用 expression.CompileScalarJSON，收集 scalar cost 和 dependencies；
- compileLookup：绑定 Contract index，构造 bitmapQuery sidecar；
- analyzeBitmap：计算 static none、scope-free、needs-scope、establishes-scope lattice；
- buildRequirements：按名称排序实际使用的索引、Fact 和 Attribute；
- buildPlan：复制 validator/specs、构建 canonical 并计算 prefilter-fingerprint/v5。

编译器的 query sidecar 只保留物理 index 信息和不透明 ScalarProgram。静态 strings/
uint64s/range 会内联并排序去重；动态 operand 在运行时由 bind 求值。

## 3. 私有表达式与 query

expression.go 定义 bitmapKind、bitmapNode、bitmapQuery、bitmapProperties 和
bitmapCost。bitmapLattice 用于编译/运行期 scope 安全：

- bitmapStaticNone：静态空结果；
- bitmapScopeFree：可从空 scope 开始；
- bitmapNeedsScope：必须继承正向 scope；
- bitmapEstablishesScope：可建立 anchor。

query.go 的 bitmapQuery.bind 将当前 seed/tick/seed Facts 绑定为 boundIndexQuery；
它检查动态 key 数是否超过 maxQueryValues、range min 是否不大于 max，并将
expression 错误适配成 Prefilter evaluate 错误。

## 4. Store、索引和执行

NewDocSet 用于创建调用方拥有的空 DocSet；它不读取 Plan 或 Store 状态，
集合运算必须在同一个 owner goroutine 内串行执行。

| 文件/符号 | 说明 |
| --- | --- |
| index.go: RequiredIndex/Requirements | 对外实际依赖快照；runtimeIndex 私有接口 |
| store.go: New | 按 Plan indexSpecs 创建物理索引和 Active bitmap |
| store.go: IndexStore.Add | 校验/复制 Ticket，写入所有 posting、Active 和 snapshot |
| store.go: IndexStore.Remove | 同步移除 posting、Active、snapshot |
| store.go: Len | Active DocID 数 |
| store.go: BeginTick | 校验 Tick Fact、prepare range index、借用 Tick layer |
| store.go: TickSession.Candidates | 求值 root，返回 DocSet |
| store.go: TickSession.CandidatesWithStats | 同时返回 Stats |
| store.go: Stats | Lookup/Contains/And/Or/Subtract 次数 |
| docset.go: DocSet | Add、Remove、Contains、Count、IsEmpty、Clone、Subtract、IDs、ForEach |

multi_value_index.go 维护 string/uint64 posting 与 keysByDoc，支持 validate、add/remove、
estimate、lookup、contains。int64_range_index.go 维护 value posting、valueByDoc 和
sorted distinct values，prepare 后以二分查找闭区间。

## 5. Lookup 与错误

lookup.go 的 prefilterLookup 只允许 seed_attributes、seed_facts、tick_facts，向共享
ScalarProgram 提供 primitive 值；candidate/match source 返回 missing。fact_adapter.go
将 fact.Values 作为 Facts 别名并适配 scope/type 错误。

errors.go 的 Error 是 Phase、Path、Code、Err；json/compile/evaluate 三阶段都使用同一
结构。常见 Code 有 UNKNOWN_SCHEMA_VERSION、QUERY_INDEX_MISMATCH、QUERY_KEY_CONTRACT、
INVALID_BITMAP、EXCLUDE_REQUIRES_SCOPE、QUERY_KEY_LIMIT、INVALID_RANGE、
INACTIVE_SEED、INDEX_LOOKUP、INDEX_CONTAINS。

实现链接：[compiler.go](../../../internal/matchsystem/prefilter/compiler.go)、
[expression.go](../../../internal/matchsystem/prefilter/expression.go)、
[query.go](../../../internal/matchsystem/prefilter/query.go)、
[index.go](../../../internal/matchsystem/prefilter/index.go)、
[store.go](../../../internal/matchsystem/prefilter/store.go)、
[docset.go](../../../internal/matchsystem/prefilter/docset.go)。
