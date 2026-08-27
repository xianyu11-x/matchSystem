# Prefilter JSON 与热更新边界

本文区分“当前仓库已经提供的 JSON 编译 API”和“上层可以实现的 generation 发布流程”。
Prefilter 本身不监听文件、不管理 revision，也不在后台切换配置；它提供可重复、可校验、
可比较的 Plan 构建输入。

## 1. 当前 JSON 协议

唯一 Contract 先解析为 `logical-node-contract/v2`：

```go
schema, err := matchsystem.ParseLogicalNodeContract(contractJSON)
if err != nil {
    return err
}
compiler, err := prefilter.NewJSONCompiler(schema)
if err != nil {
    return err
}
plan, err := compiler.Compile(prefilterJSON)
```

Prefilter envelope 是 `prefilter/v2`；其中 `plan` 是 shared expression 的显式 Root：

```json
{
  "schemaVersion": "prefilter/v2",
  "plan": {
    "resultType": "bitmap",
    "expr": {
      "op": "bitmap_if",
      "when": {
        "op": "int64_gte",
        "left": {"op": "int64_ref", "source": "tick_facts", "name": "capacity"},
        "right": {"op": "int64_literal", "value": 1}
      },
      "then": {
        "op": "domain_call",
        "tag": "prefilter",
        "kind": "prefilter.lookup.string",
        "resultType": "bitmap",
        "fields": {"index": "mode_index", "values": ["ranked"]}
      },
      "else": {"op": "bitmap_none"}
    }
  },
  "runtime": {"containsProbeThreshold": 4096}
}
```

Root、`bitmap_if.then/else` 和 `domain_call` 的 `resultType` 必须显式且一致；Bool
condition 与 Bitmap 分支不能互换。

`prefilter/v1` 不是兼容输入，loader 会返回 `UNKNOWN_SCHEMA_VERSION`，不会尝试旧 parser
或旧执行器。

## 2. Parse 与 Compile 分层

`JSONCompiler.Parse` 的实现边界：

```text
bytes
  -> jsonstrict limits / UTF-8 / duplicate / trailing / unknown fields
  -> strict prefilter/v2 envelope
  -> expression.DecodeRootInto(shared Arena, profile, DomainDescriptors)
  -> Config{Arena, Root{ResultBitmap}, runtime options}
```

共享 `DecodeRootInto` 负责 expression JSON op、ResultType、child shape、Domain field 类型
和结构资源限制；它不会解析 index slot、Fact scope 或 phase runtime。`Parse` 返回的 Config
仍需 Compile。

`JSONCompiler.Compile` 使用 JSONCompiler 固化的 Contract，接着调用
`prefilter.Compile(config, schema)`：

```text
Config
  -> Contract.Validate
  -> index name/type/KeyType/limits
  -> shared expression.Compiler
  -> DomainLeaf -> compiledIndexQuery sidecar（动态 operand 是同一 Program 的 InstructionID）
  -> Requirements + canonical + fingerprint
  -> immutable Plan
```

JSON/typed 两条路径必须经过同一个 expression Compiler 和 Prefilter leaf compiler。解析
顺序、source spelling 或 JSON field shape 的差异不能改变 Program semantics。

## 3. Descriptor 与字段协议

Prefilter 暴露 `DomainDescriptors()` 给更大的 JSON envelope 复用：

| kind | fields | 类型 |
| --- | --- | --- |
| `prefilter.lookup.string` | `index`、`values` | string、strings |
| `prefilter.lookup.uint64` | `index`、`values` | string、uint64s |
| `prefilter.lookup.int64_range` | `index`、`min`、`max` | string、int64 |

`DomainDescriptor.Parse` 只将已解码的 `DomainCall` 转成 typed `DomainLeaf`。它不持有
Contract、IndexStore 或 Roaring；index 是否存在和类型是否匹配由后续真实 Contract
compile 决定。

## 4. JSON 限制与错误

`NewJSONCompiler(schema, limits...)` 接受 `expression.JSONLimits`，并把 limit cap 到 Contract
边界。默认边界涵盖 bytes、depth、object/array fields、values、
children、literal values、steps、string bytes、nodes 和 instructions。

错误边界：

- `Phase=json`：JSON 语法、未知/重复字段、null、尾随值、未知 op、显式 resultType、
  Domain field 类型和资源限制；
- `Phase=compile`：未声明 index、index/Attribute 类型冲突、Fact source 越权、无 anchor、
  cycle、动态 query 上限；
- `Phase=evaluate`：绑定 Seed/Tick 值缺失、Min>Max、单次 query 产生过多 key、Store
  生命周期错误。

错误携带 `Path`，JSON 计划中的 expression 路径以 `$.plan` 为前缀；不要用错误字符串做
版本兼容判断。

## 5. Typed/JSON 等价与 fingerprint

等价 typed 计划：

```go
arena := expression.NewArena()
builder := prefilter.NewBuilder(arena)
values := arena.StringsLiteral("ranked")
leaf := builder.LookupString("mode_index", values)
typedPlan, err := prefilter.Compile(prefilter.Config{
    Arena: arena, Root: builder.Root(leaf),
}, schema)
```

同一 Contract 下，等价 JSON 计划应满足：

```text
Program.Canonical 相同
Requirements 相同
Prefilter fingerprint 相同
运行时候选 Bitmap 相同
```

fingerprint schema 是 `prefilter-fingerprint/v4`，输入包括 Program canonical、实际
query sidecar、依赖的 Attribute/Fact、Index requirements 和 contains probe threshold。
And/Or/集合 canonical 的顺序归一化，但改变 Contract type/scope/limits、leaf kind、动态
source、index 上限或 runtime threshold 都会改变 fingerprint。旧代 fingerprint 不能跨代
做 no-op 或加载旧 IndexGeneration。

## 6. 热更新建议边界

上层可在 LogicalNode owner goroutine 中实现如下不可变 generation：

```text
raw Contract + raw prefilter/v2
       │
       ▼
Parse/Compile (off the execution path only if ownership allows)
       │
       ▼
Plan{Program, sidecars, Requirements, Fingerprint}
       │
       ├─ Requirements compatible -> reuse physical IndexStore
       └─ Requirements changed    -> build a new IndexStore generation
       │
       ▼
publish whole generation at a command boundary
```

推荐状态：`Idle -> Decoding -> Compiling -> PreparingIndexes -> Ready -> Active`；任一阶段
失败保留当前 Active。一次 `ProduceMatch` 固定一个 generation，不混用两个 Plan 或两个
Contract。发布只替换不可变 generation 指针，旧 generation 等 owner 完成后回收。

revision 规则建议如下：

- `revision <= activeRevision` 拒绝为 stale；
- 更大 revision 但 fingerprint 相同是 no-op，不重建索引；
- revision 更大且 Requirements 改变，先同步准备新 IndexStore，再整体发布；
- 回滚使用更高 revision 重新提交旧内容，不降低 revision；
- 任何 decode/compile/index rebuild 错误都不得覆盖 Active。

这些是上层发布器的编排约束，不是当前 `prefilter` 包已经导出的 manager API。

## 7. Requirements compatibility

不能只比较 fingerprint 或 index name 判断是否可以复用物理索引。至少逐项比较：

- Index type、field、KeyType、MaxDocumentValues、MaxQueryValues；
- Attribute/Fact type、MaxValues、Fact scope 和 source 绑定；
- dynamic query 的 cardinality、边界和 missing-value 语义；
- expression/compiler/canonical ABI 版本；
- contains probe threshold（如果它参与运行行为/指纹）。

新 Plan 不再引用的 index 可以在 generation 结束后回收；被新 Plan 引用但物理契约不满足
的 index 必须构建新代。查询路径不得以 TicketStore 扫描替代缺失 index。

## 8. 可观测性与安全

上层发布器应记录 configId、revision、fingerprint、compile/prepare/activate 阶段耗时、
Requirements 变更、拒绝 Path/Code 和每次 Tick 使用的 generation。不要记录完整 Ticket、
动态 Fact 值或完整 query value。

配置输入应限制 bytes/depth/nodes/children/values，文件发布使用临时文件 flush 后原子
rename；读取/监听属于外部适配层，不要把 IO 放进 expression 或 Prefilter Compiler。

## 9. 与 Evaluation 热更新的关系

Prefilter 的 Plan 和 Evaluation 的 Plan 可以一起挂在 LogicalNode generation，但它们仍各自
调用 shared expression Compiler：

```text
one logical-node Contract snapshot
  ├─ Prefilter ResultBitmap Program + index sidecars
  └─ Evaluation bool/value Programs + scorer registry
```

一次匹配执行必须使用同一 Contract snapshot；Prefilter 负责候选域，Evaluation 负责最终
Join/Complete 和 Match Fact 原子更新。Roaring、scorer callback、事务提交不进入 shared
expression runtime。
