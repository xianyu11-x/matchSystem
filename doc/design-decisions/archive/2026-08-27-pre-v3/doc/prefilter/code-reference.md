# Prefilter 代码索引

范围：`internal/matchsystem/prefilter` 当前生产代码。表达式结构和通用编译逻辑见
[Expression Core](../expression-core.md)；本页只记录 Prefilter 的叶子、物理索引、sidecar
和执行编排。测试/benchmark 文件会随源码演进，不在本页复制已删除的旧 API。

## 1. `doc.go`

Package comment 固定包边界：索引初筛、共享 expression Bitmap Root、Fact layer 校验、
Roaring 执行和 single-owner goroutine。Prefilter 不扫描 Ticket，不负责最终评分/Join。

## 2. `builder.go`

### 公开类型与常量

| 符号 | 作用 |
| --- | --- |
| `LookupStringLeafKind` | `prefilter.lookup.string` |
| `LookupUint64LeafKind` | `prefilter.lookup.uint64` |
| `LookupRangeLeafKind` | `prefilter.lookup.int64_range` |
| `Builder` | 只创建 Prefilter DomainLeaf 的 Arena facade |

### 方法

| 方法 | 说明 |
| --- | --- |
| `NewBuilder(arenas ...*expression.Arena)` | 使用传入 Arena 或创建新 Arena |
| `(*Builder).Arena()` | 返回 Builder 所属 Arena |
| `(*Builder).Root(node)` | 返回 `Root{Node: node, Result: ResultBitmap}` |
| `(*Builder).LookupString(index, values)` | 创建 string multi-value Bitmap leaf；values 必须为 `ResultStrings` |
| `(*Builder).LookupUint64(index, values)` | 创建 uint64 multi-value Bitmap leaf；values 必须为 `ResultUint64s` |
| `(*Builder).LookupRange(index, min, max)` | 创建 int64 range Bitmap leaf；边界必须为 `ResultInt64` |

Builder 不拥有 And/Or/Exclude/If、literal 或动态 value AST；这些方法全部在
`expression.Arena`。

## 3. `compiler.go`

### 配置与结果

```go
type Config struct {
    Arena    *expression.Arena
    Root     expression.Root
    Profile  expression.CompileProfile
    ContainsProbeThreshold uint64
}
```

`Root.Result` 必须为 `expression.ResultBitmap`。`Plan` 内部持有 immutable shared Program、
Bitmap instruction view、query sidecars、index slots、Fact/Attribute validators、
Requirements 和 fingerprint。

| 方法 | 说明 |
| --- | --- |
| `Compile(config, schema)` | 验证唯一 Contract，构造 Prefilter profile，调用 shared Compiler 并组装 Plan |
| `(*Plan).Fingerprint()` | 返回 `Fingerprint` |
| `(*Plan).Requirements()` | 返回防御性复制的实际索引/Fact/Attribute 依赖 |
| `(*Plan).Program()` | 返回 shared `*expression.Program` |

内部编译步骤：

- `newCompileState`：复制 Contract，建立 index name/slot 和 required maps；
- `makePrefilterProfile`：从 `expression.StrictProfile(ResultBitmap)` 补入 source、
  Contract namespace、limits、三种 DomainLeafKindSpec；
- `compileBitmapLeaf`：解析 leaf fields，校验 index type/KeyType，创建 sidecar 并返回
  opaque `BitmapLeafInstruction`；
- 动态 operand：由 shared expression Compiler 直接编译进所属 Program，sidecar 只保留
  typed `InstructionID`；Prefilter 不创建 operand 子 Program，也不遍历通用 AST；
- `requirements`/`assemblePlan`：排序依赖、生成 Program Bitmap view、canonical、
  `prefilter-fingerprint/v4`。

`Compile` 不创建额外查询树，不把 Arena 留在 Program，也不把 Roaring 物化到 expression。

## 4. `json.go`

| 符号 | 作用 |
| --- | --- |
| `JSONSchemaVersion` | `prefilter/v2` envelope 版本 |
| `DefaultJSONLimits()` | shared expression JSON defaults |
| `JSONCompiler` | 固化 Contract、profile、limits 和 domain descriptors |
| `NewJSONCompiler(schema, limits...)` | 校验/复制唯一 Contract；limits 可选 |
| `(*JSONCompiler).Parse(data)` | 严格 envelope + shared `DecodeRootInto`，返回 typed Config |
| `(*JSONCompiler).Compile(data)` | Parse 后调用 `prefilter.Compile` |
| `DomainDescriptors()` | 返回三种 Prefilter JSON DomainDescriptor 的防御性快照 |

计划 JSON 的结构是：

```json
{
  "schemaVersion": "prefilter/v2",
  "plan": {"resultType": "bitmap", "expr": {"op": "..."}},
  "runtime": {"containsProbeThreshold": 4096}
}
```

`DomainDescriptors` 只负责 `tag/kind/resultType/fields` 到 typed `DomainLeaf` 的语法
转换；索引存在性、类型、scope、MaxQueryValues 和 sidecar 仍由 `Compile` 以真实
Contract 校验。错误经过 Prefilter `Error{Phase,Path,Code,Err}` 适配。`prefilter/v1`
会被明确返回 `UNKNOWN_SCHEMA_VERSION`，不再作为兼容输入。

## 5. `query.go`

`compiledIndexQuery` 是一个 DomainLeaf 的物理 sidecar：

- `compiledQueryString`、`compiledQueryUint64`、`compiledQueryRange` 是闭合物理 query kind；
- `slot` 是 index slice slot，`index` 是 canonical 名称，`maxKeys` 是查询上限；
- `values`/`min`/`max` 是可选的 shared Program `InstructionID`；静态集合/范围直接内联；
- `bind(ctx,path)` 把 Seed/Seed Facts/Tick Facts 绑定为 concrete query，校验动态上限和
  range `min <= max`；
- `estimate`、`lookup`、`contains` 委托给 runtime index；
- `canonicalString` 返回稳定物理 query token。

sidecar 不保存 Bitmap；`LeafHandle` 只作为 Plan 内部 sidecar 的 O(1) 索引。

## 6. `lookup.go`

`prefilterLookup` 实现 shared `expression.Lookup`：

| Source | 数据 |
| --- | --- |
| `SourceSeedAttributes` | `common.Ticket.StringLists/Uint64Lists/Int64Values` |
| `SourceSeedFacts` | 当前 Seed 的 object `Facts` |
| `SourceTickFacts` | 当前 TickSession 借用的 Tick `Facts` |

Candidate/Match source 一律返回 missing。它只返回 primitive 值和存在标记，不泄漏 Ticket、
Fact map 或 Mutable Store；集合值由 shared Program 做排序去重。

## 7. `index.go`

| 类型 | 作用 |
| --- | --- |
| `RequiredIndex` | 对外的实际 index requirement snapshot |
| `Requirements` | `Indexes`、`Facts`、`Attributes` 三类依赖 |
| `runtimeIndex` | 内部 `validate/add/remove/prepare/estimate/lookup/contains` 接口 |
| `indexSpec` | Contract IndexSpec 的 immutable physical copy |

`newIndex` 只分派 `multi_value` 与 `int64_range` 两种 Contract index。未知类型属于内部
不变量错误，不对外提供动态插件接口。

## 8. 物理索引文件

### `multi_value_index.go`

string 和 uint64 两种实现都维护 posting、每 DocID 的 key 反向记录和 key 上限：

- `validate(ticket)` 检查字段类型与 MaxDocumentValues；
- `add/remove(docID,ticket)` 维护 posting 与反向记录；
- `estimate(query)` 汇总 query keys 的 posting 基数；
- `lookup(query)` OR 组合多个 key 的 posting；
- `contains(query,docID)` 做单 DocID probe。

### `int64_range_index.go`

维护 value posting、`valueByDoc`、distinct `sortedValues` 和 dirty 标志；`prepare` 按需
排序，range query 用二分确定闭区间，不按数值宽度逐整数遍历。

## 9. `store.go` 与 `docset.go`

`IndexStore` 创建所有物理 index 和 Active Roaring Bitmap：

| 方法 | 语义 |
| --- | --- |
| `New(plan)` | 根据 Plan indexSpecs 初始化 Store |
| `Add(docID, ticket)` | 校验并原子式写入所有 index/Active |
| `Remove(docID)` | 同步删除所有 posting/Active |
| `Len()` | Active DocID 数 |
| `BeginTick(tickFacts)` | 校验 Tick Facts、prepare index、返回 TickSession |

`TickSession.Candidates`/`CandidatesWithStats` 校验 seed、Facts scope 后调用
`evalBitmap`。执行器只消费 `Program.BitmapInstructions` 与 sidecar，不访问 source Arena。
`Stats` 暴露 Lookup/Contains/And/Or/Subtract 调用计数。

`DocSet` 是调用方拥有的可变 uint32 集合：`Count`、`IsEmpty`、`Contains`、`Clone`、
`Subtract`、`IDs`、`ForEach`。它不提供 Ticket 读取或 Match 提交。

## 10. `fact_adapter.go` 与 `errors.go`

`Facts` alias 到中立 `fact.Values`；`validateFactTypes`、`validateFactScopes` 和
`adaptFactError` 复用共享 Fact 校验。错误类型统一为：

```go
type Error struct {
    Phase string // json | compile | evaluate
    Path  string
    Code  string
    Err   error
}
```

Prefilter 不吞掉结构化错误，也不把缺失值解释成空 Bitmap。

## 11. 测试索引

当前测试按职责分组：

- `v3_test.go`、`public_v3_test.go`、`compiler_v3_test.go`：显式 Bitmap Root、Builder、
  JSON/typed canonical、Contract/Requirements 和 fingerprint golden；
- `expressions_v3_test.go`、`p1_test.go`：shared value Program、DomainLeaf schema、
  foreign Arena、leaf handle/properties；
- `store_v3_test.go`、`migration_coverage_test.go`、`oracle_test.go`：Bitmap 执行、
  anchor/Exclude、动态 query、Fact scope 和索引一致性；
- `prefilter_bench_test.go`、`v3_bench_test.go`：posting、contains probe 和 Bitmap 性能。

测试的生产 API 入口始终是 `expression.Arena`、`prefilter.Builder`、`prefilter.Compile`
和 `prefilter.NewJSONCompiler`。
