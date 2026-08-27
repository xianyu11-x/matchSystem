# Expression Core

`internal/matchsystem/expression` 是 Prefilter 与 Evaluation 共用的唯一表达式语言、
校验器和编译器。它拥有合法节点集合、结果类型、Arena、JSON 解码、source/capability
检查、canonical、依赖收集和不可变 `Program`。Prefilter/Evaluation 只提供领域叶子描述/编译器和阶段运行时。

## 1. 五种结果类型

每个根和每个节点都必须有明确的 `ResultType`：

| `ResultType` | 产生的值 | 典型使用方 |
| --- | --- | --- |
| `ResultBitmap` | 候选 DocID 的逻辑 Bitmap 结果 | Prefilter |
| `ResultBool` | 布尔值 | Evaluation Join/Complete、BitmapIf 条件 |
| `ResultInt64` | 单个 `int64` | Fact 更新、Range 查询的边界 |
| `ResultStrings` | 排序去重后的 string 集合 | string index 查询、Fact |
| `ResultUint64s` | 排序去重后的 uint64 集合 | uint64 index 查询、Fact |

Typed API 使用 `arena.Root(node, result)` 或 `NodeRef.Root(result)`。JSON API 使用：

```json
{
  "resultType": "bool|int64|strings|uint64s|bitmap",
  "expr": {"op": "..."}
}
```

缺少 `resultType`、根结果与 `expr` 产出不一致，或父节点要求的子结果不一致，都会在
JSON/compile 边界失败。`ResultBitmap` 不是 Bool 的别名；它有自己的 Bitmap 结构节点和
执行属性。

## 2. Arena、NodeRef 与 Root

`Arena` 是 append-only 的节点所有者，`NodeID == 0` 永远无效。构造节点的入口集中在
`Arena`：

```go
arena := expression.NewArena()
left := arena.Int64Lookup(expression.SourceMatchFacts, "count")
right := arena.Int64Literal(4)
condition := arena.GreaterOrEqualInt64(left, right)
root := arena.Root(condition, expression.ResultBool)
```

`Arena.Ref(id)` 返回带所有权的 `NodeRef`。`Root` 只包含 `Node` 和显式 `Result`，
不携带 Arena；跨包边界需要验证所有权时，使用 `NodeRef.Root(result)` 与
`Compiler.CompileRef(ref, result)`。`Compile(arena, root)` 仅能在给定 Arena snapshot 中解释 numeric
`NodeID`，不能证明该 ID 最初来自哪座 Arena。Compiler 在编译前取得稳定 snapshot，
生成的 Program 不保留可变 Arena 引用。`Snapshot`/`Freeze` 和
`Node`/`Nodes` 都返回防御性副本。

### 2.1 结构构造器

| 结果 | 构造器 | 节点 |
| --- | --- | --- |
| Bool | `BoolLiteral`、`BoolAnd`、`BoolOr`、`BoolNot` | 布尔常量/逻辑 |
| Int64 | `Int64Literal`、`Int64Lookup`、`Int64Step`、`Int64Clamp`、`Int64Add/Sub/Min/Max` | 数值表达式 |
| Strings | `StringsLiteral`、`StringsLookup`、`StringsUnion` | string 集合 |
| Uint64s | `Uint64sLiteral`、`Uint64sLookup`、`Uint64sUnion` | uint64 集合 |
| Bool | `EqualInt64`、`LessInt64`、`GreaterOrEqualInt64` 等 | int64 比较 |
| Bool | `EqualStrings`、`ContainsString`、`IntersectsStrings` 等 | string 集合谓词 |
| Bool | `EqualUint64s`、`ContainsUint64`、`IntersectsUint64s` 等 | uint64 集合谓词 |
| Bitmap | `BitmapAnd`、`BitmapOr`、`BitmapExclude`、`BitmapIf`、`BitmapNone` | Bitmap 结构 |
| domain result | `DomainLeaf(DomainLeaf{...})` | 领域叶子扩展点 |

`CompareOp`/`SetOp` 只在 builder 辅助方法中使用；节点中保存的是闭合的 `Kind`，不会把
任意 operator 或接口值带入 Compiler。

## 3. 合法 Kind 是闭合集合

`Kind` 是 expression 包定义的封闭枚举。Compiler 先依据根结果选择
`BuiltinKinds(result)`，再依据 phase profile 可选地收紧集合；未知 Kind、错误 Result 或
不在 profile 中的 Kind 一律拒绝。

| 家族 | 合法 Kind |
| --- | --- |
| Bool 结构 | `KindBoolLiteral`、`KindBoolAnd`、`KindBoolOr`、`KindBoolNot` |
| Int64 值 | `KindInt64Literal`、`KindInt64Lookup`、`KindInt64Step`、`KindInt64Clamp`、`KindInt64Add`、`KindInt64Sub`、`KindInt64Min`、`KindInt64Max` |
| 集合值 | `KindStringsLiteral/Lookup/Union`、`KindUint64sLiteral/Lookup/Union` |
| Int64 谓词 | `KindInt64Equal`、`NotEqual`、`Less`、`LessOrEqual`、`Greater`、`GreaterOrEqual` |
| String 谓词 | `KindStringsEqual`、`NotEqual`、`Empty`、`Contains`、`ContainsAny`、`ContainsAll`、`Intersects` |
| Uint64 谓词 | `KindUint64sEqual`、`NotEqual`、`Empty`、`Contains`、`ContainsAny`、`ContainsAll`、`Intersects` |
| Bitmap 结构 | `KindBitmapAnd`、`KindBitmapOr`、`KindBitmapExclude`、`KindBitmapIf`、`KindBitmapNone` |
| 领域扩展 | `KindDomainLeaf`，但必须同时注册具体 `DomainLeafKindSpec` 和 typed leaf compiler |

`BuiltinKindSet(ResultBitmap)` 包含 Bitmap 结构、Bool 条件及其全部 typed descendants；
例如 `BitmapIf` 的 `when` 只能是 `ResultBool`。`BuiltinKinds(ResultBool)`、
`BuiltinKinds(ResultInt64)`、`BuiltinKinds(ResultStrings)` 和 `BuiltinKinds(ResultUint64s)`
分别返回对应闭包。阶段不应手写另一份 Kind 表。

## 4. StrictProfile 与 phase profile

```go
profile := expression.StrictProfile(expression.ResultBitmap)
profile.AllowedSources = expression.CapabilitySeedAttributes |
    expression.CapabilitySeedFacts |
    expression.CapabilityTickFacts
profile.Attributes = schema.AttributeSpecs()
profile.Facts = schema.FactSpecs()
profile.DomainLeafKinds = prefilterDomainKinds
profile.LeafCompilers.Bitmap = expression.BitmapLeafFunc(compileBitmapLeaf)
```

`StrictProfile(result)` 预填允许的根结果和内建 Kind 闭包，但不预填 source、Fact、
Attribute、限制或 domain leaf。`BuiltinKinds(result)` 适合多个根共用一个 profile；调用方
应合并去重后再设置 `AllowedRoots`/`AllowedKinds`。`CompileProfile` 还包含：

- `AllowedSources`：Source capability 位集；
- `Attributes`、`Facts`：封闭的名称和类型 namespace；
- `FactAllowed(source, name)`：scope 到 source 的额外约束；
- `Limits`：深度、children、literal、step、node、instruction 上限；
- `DomainLeafKinds` 与 `LeafCompilers`：本阶段允许的领域叶子。

Evaluation 为 Join/Complete/Match Fact update 创建 phase profile；Prefilter 为 Bitmap 根
创建 profile。这些 profile 只描述“允许什么”，实际结构和类型检查仍集中在同一 Compiler。

## 5. Compiler 与 Program

```go
program, err := expression.NewCompiler(profile).Compile(arena, root)
// 或：
program, err := expression.Compile(arena, root, profile)
// 需要所有权验证的包边界：
program, err := expression.NewCompiler(profile).CompileRef(arena.Ref(root.Node), root.Result)
```

Compiler 的一次编译包括：

1. 检查根 Node、显式 Result 和 `AllowedRoots`；`CompileRef` 额外检查 Arena ownership。
2. 对 Arena snapshot 做 DFS，拒绝非法 ID、cycle、未允许 Kind、节点 Result 不匹配和
   错误 child shape。
3. 检查 source/capability、Contract 中的 Attribute/Fact 名称及类型。
4. 校验空组合、Step 单调性、动态 clamp、literal/children/depth/node 限制。
5. 对 DomainLeaf 校验字段 schema，并调用其 typed compiler，得到 opaque `LeafHandle`、
   叶子属性和可选 canonical token。
6. 生成只含 InstructionID、primitive 数据和 opaque leaf handle 的不可变 Program，收集
   Dependencies 与 canonical。

`Program` 提供 `Root`、`ResultType`、`Instructions`、`InstructionCount`、typed instruction
视图、`Dependencies` 和 `Canonical`。Bitmap 根额外提供 `BitmapInstructions` 与
`BitmapProperties`；这是执行器所需的结构视图，不是 Bitmap 值。

## 6. Domain leaf descriptor/compiler

expression 不知道索引、scorer、Ticket 或业务查询。领域包只提交 typed schema：

```go
profile.DomainLeafKinds = []expression.DomainLeafKindSpec{
    {
        Kind:   "prefilter.lookup.string",
        Result: expression.ResultBitmap,
        Fields: []expression.LeafFieldSpec{
            {Name: "index", Type: expression.LeafFieldString, Required: true},
            {Name: "values", Type: expression.LeafFieldUint64,
                AllowedTypes: []expression.LeafFieldType{
                    expression.LeafFieldUint64, expression.LeafFieldStrings,
                }, Required: true},
        },
    },
}
profile.LeafCompilers.Bitmap = expression.BitmapLeafFunc(
    func(leaf expression.CompiledDomainLeaf) (expression.BitmapLeafInstruction, error) {
        // 领域包把 leaf 解析为自己的 sidecar，返回 opaque handle。
        return expression.BitmapLeafInstruction{
            Handle: 1,
            Properties: expression.LeafProperties{
                State: expression.BitmapScopeFree | expression.BitmapEstablishesScope,
            },
            Canonical: "domain-specific-canonical",
        }, nil
    },
)
```

`LeafField` 只有 bool/int64/uint64/string/集合这几个闭合输入类型，可通过 `NodeRef` 绑定
Arena 中的动态值。`LeafFieldSpec` 规定字段名、类型、必填、cardinality、AllowedTypes
和 MaxValues。typed Builder 与 JSON descriptor 必须接受同一套字段规则。

Bitmap leaf compiler 返回 `BitmapLeafInstruction{Handle, Properties, Canonical}`。其中
`LeafProperties.State` 可表达 `BitmapStaticNone`、`BitmapScopeFree`、
`BitmapNeedsScope`、`BitmapEstablishesScope`；Compiler 会计算整个 Bitmap 树的属性并让
Prefilter 执行器据此校验 anchor/Exclude。Handle 只在所属领域的 sidecar 中解释，不能
携带 Bitmap 对象进 expression。

当 `LeafField.Node` 表示动态 operand 时，Compiler 会先生成 `CompiledLeafOperand`，其中
的 `Instruction` 指向同一个 enclosing `Program`，并把它和 operand 的 Result、cardinality
property、canonical 一起交给 typed leaf compiler。领域包只保存这个 typed InstructionID
或静态值；运行时通过同一个 Program 的 `Evaluate*At` 读取，不创建嵌套 Program，不复制
value evaluator，也不遍历通用 Arena。

## 7. JSON：DecodeRootInto 与 ParseRoot

### 7.1 DecodeRootInto

`DecodeRootInto(arena, data, DecodeOptions)` 是共享语法入口：

```go
profile.JSONLimits = expression.DefaultJSONLimits()
root, err := expression.DecodeRootInto(arena, raw, expression.DecodeOptions{
    Profile: profile,
    Domains: prefilter.DomainDescriptors(),
})
```

它严格检查 JSON、重复/未知字段、尾随值、资源限制、显式 resultType、每个 op 的字段、
子节点结果和 domain descriptor。它只把 JSON 追加为 typed Arena 节点；Lookup 名称的
真实 Contract、Fact scope 和 phase capability 由后续 `Compiler.Compile` 决定。解码失败
后已追加的 Arena 节点不会回滚，调用方需要丢弃这座 Arena 才能获得事务式语义。

### 7.2 ParseRoot

`ParseRoot(data, DecodeOptions)` 是单根 convenience boundary：它创建 Arena，执行
`DecodeRootInto`，再用同一 profile 编译并返回 `ParsedRoot{Root, Program}`。需要一次解码
多个根、共享 Arena 或把真实 Contract 留到 phase compile 时，使用 `DecodeRootInto`；需要
立即得到 Program 时使用 `ParseRoot`。Prefilter/Evaluation 的 envelope parser 都复用
前者，以便把多个根放在一座 Arena 并统一调用真实 Contract 编译。

表达式 operator 的 JSON 名称是稳定的闭合集合，例如：

```json
{
  "resultType": "bool",
  "expr": {
    "op": "int64_gte",
    "left": {"op": "int64_ref", "source": "match_facts", "name": "count"},
    "right": {"op": "int64_literal", "value": 4}
  }
}
```

Bitmap domain leaf 使用：

```json
{
  "resultType": "bitmap",
  "expr": {
    "op": "domain_call",
    "tag": "prefilter",
    "kind": "prefilter.lookup.string",
    "resultType": "bitmap",
    "fields": {"index": "mode_index", "values": ["ranked"]}
  }
}
```

`DomainDescriptor{Tag, Kind, Result, Fields, Parse}` 将 wire field 转为 `DomainCall`，
再由领域 parser 生成 DomainLeaf。表达式包只负责结构/类型/限制；实际 index 名解析、
query sidecar 和运行时 handle 仍由 Prefilter 完成。

## 8. Runtime 边界

expression Program 的 primitive 运行时接口为 `EvaluateBool`、`EvaluateInt64`、
`EvaluateStrings`、`EvaluateUint64s` 和 `EvaluateBoolAt` 等；它只接收 `Lookup` 与可选的
typed `LeafEvaluators`。缺失值、非法 instruction、cycle 和叶子 evaluator 错误会返回
结构化 expression error，不会 panic 或自动切换 source。
领域叶子 evaluator 也通过 phase-scoped `Lookup` 读取数据；运行时会再次执行 source capability
检查，不能因为 Context 带有额外数据而绕过编译期的 source 限制。

Bitmap Program 不执行 Roaring：Prefilter 消费 `BitmapInstructions`，通过 instruction 的
opaque handle 找到 query sidecar，再在自己的 `IndexStore`/`TickSession` 中做 estimate、
lookup、contains、AND、OR、AND-NOT。Evaluation 消费 Bool/Int64/集合 Program，并在自己
的 runtime context 中执行 scorer、Join、Complete 和 Match Fact 原子更新。

因此可统一的是“节点语言、合法性、编译产物和 primitive lookup”；Roaring、索引 posting、
scorer callback、TicketStore、事务/提交和 Match 生命周期必须留在领域/编排包中。

## 9. 错误与 canonical

expression 错误包含 `Phase`、`Path`、`Code` 和底层 `Err`。JSON 形状问题通常是 `json`，
编译/Contract/capability 问题是 `compile`，Program 运行期问题是 `evaluate`。上层可在
边界处保留原 Path 并加上 `$.plan`、`join` 或 `matchFacts.<phase>.<name>` 前缀。

Program canonical 对节点、集合、动态值和 DomainLeaf canonical token 使用确定性顺序；
Dependencies 同样是排序后的只读快照。Prefilter fingerprint 还会加入实际 Requirements
和运行参数，Expression Program 自身不包含 Roaring Bitmap 或 Match Fact 当前值。
## Dynamic Domain Operand JSON

When a `LeafFieldSpec` declares `DynamicResult`, that field may use the same
strict Root envelope as any other expression:

```json
{
  "resultType": "strings",
  "expr": {
    "op": "strings_union",
    "items": [
      {"op": "strings_ref", "source": "seed_attributes", "name": "mode"},
      {"op": "strings_ref", "source": "seed_facts", "name": "labels"}
    ]
  }
}
```

The envelope `resultType` must equal the descriptor's `DynamicResult`. A bare
legacy AST object is rejected. The nested expression is appended to the same
Arena and compiled by the same Compiler into the enclosing Program; no domain
adapter creates a second parser, Compiler, or Program.
### Domain Descriptor Identity

The JSON registry requires `DomainDescriptor.Kind` to be globally unique.
`Tag` is a wire namespace and cannot select a second schema/parser for the same
kind, because `CompileProfile` and `DomainLeaf` identify the compiled leaf by
kind alone. Registering the same kind under two tags is rejected as
`DUPLICATE_DOMAIN_KIND`.

`Arena.Snapshot` and `Arena.Freeze` retain the source Arena's stable ownership
identity. Dynamic `NodeRef` values therefore remain valid when compiling a
snapshot, while references from an unrelated Arena are still rejected as
`FOREIGN_ARENA`.
