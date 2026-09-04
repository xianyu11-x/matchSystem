# Expression、Prefilter、Evaluation 统一实施记录

> 状态：当前 worktree 的最终架构说明（2026-08-26）。本文不是兼容迁移方案。

本文记录表达式核心、Prefilter、Evaluation 和 LogicalNode 的边界。总目录见
[文档总入口](README.md)，完成审计见 [表达式统一改造完成报告](expression-unification-completion.md)。

## 1. 统一决策

`internal/matchsystem/expression` 是 Prefilter 与 Evaluation 共用的唯一表达式内核，统一负责：

- `Arena`、`NodeID`/`NodeRef`、显式 `Root`；
- 五种 `ResultType`、封闭 `Kind`、`Profile` 和节点 shape；
- 严格 JSON 解码、依赖/canonical、limits、统一 `Compiler` 和不可变 `Program`；
- primitive lookup、typed DomainLeaf instruction，以及 `Evaluate*`/`Evaluate*At`。

两个领域不再各自维护 AST、通用 parser 或通用 compiler。它们只提供自己的
DomainLeaf descriptor、typed leaf compiler/evaluator、sidecar 和运行时编排：

| 包 | 负责 | 不负责 |
| --- | --- | --- |
| `expression` | 表达式结构、类型、合法 Kind、JSON、编译、Program 和 typed evaluation | Roaring、物理索引、Ticket/Match 生命周期 |
| `prefilter` | Bitmap leaf、Contract/index 绑定、query sidecar、anchor/estimate、Roaring | scorer、Join/Complete、Match Fact 更新 |
| `evaluation` | 一个 scorer、Join、OnJoin Match Fact update、Complete、phase/context 校验 | Bitmap、索引物理执行和通用 workflow |
| `LogicalNode` | owner goroutine、固定流程、候选 Top-L、Match Fact 原子发布 | 第三套表达式 IR 或可配置流程 |

统一的是表达式语言、合法性检查、编译产物和 primitive evaluation；领域 runtime 的
状态与物理资源仍由领域包拥有。

## 2. 当前目录与公共入口

~~~text
doc/
  README.md
  expression-core.md
  expression-prefilter-evaluation-split-plan.md
  expression-unification-completion.md
  logical-node-contract-v2.md
  evaluation-layer.md
  prefilter/
    usage-guide.md
    architecture.md
    code-reference.md
  index-prefiltering.md
  json-prefilter-hot-reload.md
  match-system-framework.md
  router-physical-logical-node.md
  fact-lifecycle.md
  seed-order-policy.md
  ticket-lifecycle.md
  logical-node-selector.md
~~~

表达式节点直接使用 shared `expression` 包。Contract、Prefilter、Evaluation 的 typed
入口分别由当前领域包提供；JSON 入口先严格解码目标 envelope，再使用同一套表达式
compiler 和真实 Contract 完成语义编译。

## 3. 唯一 Contract 与版本边界

`logical-node-contract/v2` 是 Prefilter 与 Evaluation 共享的唯一业务 Contract，声明
Ticket 属性、物理索引及 `tick`、`object`、`match` 三类 Fact。
`LogicalNodeSpec.Contract` 是唯一 Contract 入口，LogicalNode 使用同一份已验证快照
编译两个领域计划：

~~~go
prefilterPlan, err := prefilter.Compile(spec.Config.Prefilter, spec.Contract)
evaluationPlan, err := evaluation.Compile(
    spec.Evaluation, spec.Contract, spec.EvaluationOptions,
)
~~~

`prefilter.Config` 和 `evaluation.CompileOptions` 不携带另一份 Contract。稳定编译签名是：

~~~go
prefilter.Compile(config, schema)
evaluation.Compile(config, schema, options)
~~~

Contract 版本和 JSON envelope 版本是不同维度：

| 对象 | 当前版本 | 含义 |
| --- | --- | --- |
| 业务 Contract | `logical-node-contract/v2` | Attributes、Indexes、Facts 的唯一声明 |
| Prefilter envelope | `prefilter/v2` | Bitmap plan 的 wire shape |
| Evaluation envelope | `evaluation/v2` | scorer、Match Fact roots、Join/Complete 的 wire shape |
| Prefilter fingerprint | `prefilter-fingerprint/v4` | Program、sidecar、Requirements 和运行参数的派生标识 |

`prefilter/v1` 和 `evaluation/v1` 只作为负向输入说明：loader 返回
`UNKNOWN_SCHEMA_VERSION`，不会调用旧 parser、转换旧 IR 或进入运行时。旧 Contract、
canonical、fingerprint 和缓存不能与当前 generation 混用，升级时必须重新编译。

## 4. Shared Expression Core

### 4.1 五种显式 ResultType

每个 Node 和 Root 都必须声明结果类型。当前封闭类型为：

| 类型 | 含义 | 顶层用途 |
| --- | --- | --- |
| `ResultBitmap` | 候选 DocID 的 Bitmap 结构结果 | Prefilter |
| `ResultBool` | 判断是否成立的 bool 结果 | Evaluation Join/Complete、BitmapIf 条件 |
| `ResultInt64` | 单值 int64 | Match Fact update、数值比较和范围边界 |
| `ResultStrings` | 去重 string 集合 | Match Fact update、字符串查询和谓词 |
| `ResultUint64s` | 去重 uint64 集合 | Match Fact update、uint64 查询和谓词 |

typed Root 使用 `expression.Root{Node, Result}`；JSON Root 使用：

~~~json
{"resultType":"bool","expr":{"op":"..."}}
~~~

缺少 resultType、Root/Node 类型不匹配、非法 child shape、非法 source，或把 Bitmap 当
Bool，均在 JSON/compile 边界失败。Join/Complete 的根固定为 `ResultBool`；
Initialize/OnJoin 的根结果必须匹配目标 match-scope Fact 的类型。

### 4.2 一个 Arena、Compiler、Program 语义

`Arena` 是 append-only Node 的所有者，`Root` 携带 Arena 中的节点和显式结果类型。
统一 `expression.Compiler` 在稳定 snapshot 上集中检查：

- Root 结果、封闭 Kind 集合和父子结果类型；
- child shape、非法 NodeID、foreign Arena、cycle、深度/节点/指令 limits；
- source/capability、Contract namespace、Fact scope/type；
- DomainLeaf descriptor、动态 operand、依赖和 canonical。

编译结果是不可变 `expression.Program`。Program 不保存可变 Arena、Roaring、IndexStore、
Match 当前值或 Ticket，只保存指令、primitive 数据和 opaque leaf handle，并提供
`EvaluateBool`、`EvaluateInt64`、`EvaluateStrings`、
`EvaluateUint64s` 以及 `Evaluate*At`。

`StrictProfile`、`BuiltinKindSet`/`BuiltinKinds` 和 `ProfileForRoots` 是合法内建
Kind 的中央来源。Prefilter/Evaluation 只注入自己的 source/capability、Contract namespace、
phase 和 DomainLeaf 注册，不复制内建 Kind 表。

## 5. DomainLeaf 与单 Program 规则

DomainLeaf 是领域扩展点，不是第二种表达式语言。动态字段通过 Arena 中的 value Node
表示，descriptor 用 `DynamicResult` 声明动态结果类型。shared Compiler 将动态字段
编译成所属 Program 的 typed `CompiledLeafOperand`，包括字段名、结果类型、
`InstructionID`、operand properties 和 canonical。

~~~text
one expression.Program
  ├─ primitive/value instructions
  └─ typed DomainLeaf instruction
       └─ sidecar: InstructionID -> domain physical handle
~~~

sidecar 只保存 InstructionID、静态值或领域 opaque handle，并通过所属 Program 的
`Evaluate*At` 读取动态值。领域包只提供 descriptor、typed leaf compiler/evaluator、
opaque handle 和 sidecar：

- 不创建 operand 子 Program；
- 不创建第二份通用 evaluator；
- 不遍历通用 Arena/AST；
- 不绕过中央 Kind、ResultType、source、scope 和 limits 检查。

## 6. Prefilter 适配

Prefilter 的 typed 入口使用 shared Arena 和显式 Bitmap Root：

~~~go
arena := expression.NewArena()
builder := prefilter.NewBuilder(arena)
values := arena.StringsLookup(expression.SourceSeedAttributes, "mode")
leaf := builder.LookupString("mode_index", values)
config := prefilter.Config{Arena: arena, Root: builder.Root(leaf)}
plan, err := prefilter.Compile(config, schema)
~~~

当前 Bitmap DomainLeaf 是：

- `prefilter.lookup.string`，动态值 `ResultStrings`；
- `prefilter.lookup.uint64`，动态值 `ResultUint64s`；
- `prefilter.lookup.int64_range`，两个 `ResultInt64` 边界。

Bitmap And/Or/Exclude/If/None 由 shared Arena 构造，Root 必须是 `ResultBitmap`。
Prefilter profile 只允许 Seed Attributes、Seed Object Facts 和 Tick Facts；Match Facts、
Candidate source 及不匹配的 index/fact scope 均在 compile 时拒绝。

编译和执行边界：

~~~text
Contract.Validate
  -> Prefilter CompileProfile + DomainLeaf registration
  -> expression.Compiler.Compile(Arena, Root)
  -> typed query sidecars + Requirements
  -> canonical + prefilter-fingerprint/v4
  -> immutable Prefilter Plan
  -> IndexStore/TickSession anchor/estimate/contains/lookup/Roaring
~~~

Prefilter 不扫描全池 Ticket，不执行 scorer、Join 或 Complete；Roaring、index posting、
IndexStore/TickSession 和物理 handle 均由 Prefilter runtime 拥有。

## 7. Evaluation 三块能力与 phase

每个 LogicalNode 独立选择一个命名 `CandidateScorer`。它是 Go callback，只返回有限
`float64`；LogicalNode 在 Prefilter 候选安全超集上用它做 bounded Top-L。Scorer 不
读取 Match Fact，不调用 Provider，也不改变流程。

表达式部分由三个阶段能力组成，另有 Initialize/OnJoin 作为 Match Fact value roots：

| 能力 | 配置入口 | 根结果 | 作用 |
| --- | --- | --- | --- |
| Initialize | `MatchFacts.Initialize` | Fact 对应的 Strings/Int64/Uint64s | 生成全部 match-scope Match Fact |
| Join | `Config.Join` | `ResultBool` | 判断当前候选能否加入 Match |
| OnJoin | `MatchFacts.OnJoin` | Fact 对应的 Strings/Int64/Uint64s | 加入后更新 Match Fact |
| Complete | `Config.Complete` | `ResultBool` | 判断 Match 是否成立 |

阶段 profile 封闭的读取权限：

| 阶段 | 可读内容 | 结果 |
| --- | --- | --- |
| Initialize | Seed 属性、Seed Object Fact、Tick Fact | Match Fact value root |
| Join | Seed 属性/Fact、Tick Fact、当前 Candidate 属性/Fact、当前 Match Fact | Bool |
| OnJoin | 与 Join 相同的当前快照 | Match Fact value root |
| Complete | Match Fact、Tick Fact | Bool |

Candidate scorer 的 context 只包含 Seed/Candidate Ticket 属性、Seed Facts、Candidate Facts
和 Tick Facts，不包含 Match Fact。当前 Candidate 是唯一暴露的候选成员视图。

新候选通过 Join 后，OnJoin 的所有 roots 基于同一份加入前 Match Fact snapshot 计算；
全部 update 成功并通过 Fact 类型、scope、MaxValues 校验后，LogicalNode 才原子发布新的
Match Fact。后续候选只能读取聚合后的 Match Fact、自己的属性/Fact 和固定 Seed/Tick
输入；不能读取已有 Match 成员的属性或 Object Fact，也没有遍历 Match 成员的 API。

固定运行时序：

~~~text
Seed
  -> Tick/Object Fact snapshot
  -> Initialize 全部 Match Fact
  -> seed-only Complete
  -> Prefilter 候选超集
  -> CandidateScorer bounded Top-L
  -> Join Bool
       false -> 跳过
       true  -> OnJoin update -> 原子合并 Match Fact -> Complete Bool
  -> 成立则提交，否则继续候选；错误不触发后续副作用
~~~

`PhaseInitialize`、`PhaseJoin`、`PhaseUpdate`、`PhaseComplete` 的 DomainLeaf
allow-list 必须非空且合法。未知 kind/result、越权 source、跨 scope Fact、缺失
compiler/evaluator 和动态 operand 类型不匹配均 fail closed；Evaluation 的
`DomainRegistry` 是当前唯一领域注册集合。

## 8. 动态 JSON operand 的最新 Root envelope

动态 DomainLeaf 字段不是裸 AST，也不是另一种 JSON 语法。只有 descriptor 声明
`DynamicResult` 的字段才允许把字段值写成完整 Root envelope：

~~~json
{
  "schemaVersion": "prefilter/v2",
  "plan": {
    "resultType": "bitmap",
    "expr": {
      "op": "domain_call",
      "tag": "prefilter",
      "kind": "prefilter.lookup.string",
      "resultType": "bitmap",
      "fields": {
        "index": "modes",
        "values": {
          "resultType": "strings",
          "expr": {
            "op": "strings_union",
            "items": [
              {"op": "strings_ref", "source": "seed_attributes", "name": "mode"},
              {"op": "strings_ref", "source": "seed_facts", "name": "labels"}
            ]
          }
        }
      }
    }
  }
}
~~~

外层 `domain_call` 自身仍必须声明 `resultType:"bitmap"`；动态字段的 nested
Root 必须声明与 descriptor.DynamicResult 相同的结果类型（上例为 `strings`），
其 `expr` 才是所属 Arena/Program 的子节点。动态字段不能声明 Bitmap result。
不支持动态的静态字段继续使用原有 primitive/array JSON spelling。

int64 range 的两个动态边界使用相同协议：

~~~json
{
  "op": "domain_call",
  "tag": "prefilter",
  "kind": "prefilter.lookup.int64_range",
  "resultType": "bitmap",
  "fields": {
    "index": "rating_index",
    "min": {
      "resultType": "int64",
      "expr": {
        "op": "int64_sub",
        "left": {"op": "int64_literal", "value": 100},
        "right": {"op": "int64_ref", "source": "seed_facts", "name": "delta"}
      }
    },
    "max": {
      "resultType": "int64",
      "expr": {"op": "int64_literal", "value": 200}
    }
  }
}
~~~

DecodeRootInto 把 nested Root envelope 解码到同一座 Arena；Compiler 随后把 nested
expression 编成同一个 enclosing Program 的 typed InstructionID。nested expression 计入
同一套 depth、children、nodes、instructions 和 source/capability limits；不允许用动态
字段绕过约束。运行时由 sidecar 通过 `Evaluate*At` 读取该 InstructionID。

错误示例：

~~~json
{"values":{"op":"strings_ref","source":"seed_attributes","name":"mode"}}
~~~

上例缺少 Root envelope，应在 JSON boundary 失败；`resultType:"int64"` 或
`resultType:"bitmap"` 也会因 dynamic result mismatch 失败；Candidate source、
未知 Fact 和超出 limits 的 nested expression 分别在 capability/Contract/limits boundary
失败。

## 9. JSON 统一入口

Prefilter envelope 必须为 `prefilter/v2`，Evaluation envelope 必须为
`evaluation/v2`。所有表达式字段（包括动态 DomainLeaf operand）使用显式
`{resultType, expr}`。JSON 只负责严格结构解码，真实 Contract 语义仍由 shared
expression Compiler 完成。

~~~go
// Prefilter
compiler, err := prefilter.NewJSONCompiler(schema)
if err != nil {
    return err
}
prefilterPlan, err := compiler.Compile(rawPrefilterJSON)

// Evaluation
options := matchsystem.EvaluationCompileOptions{Scorers: scorers}
config, err := matchsystem.ParseEvaluationJSONWithDefaults(rawEvaluationJSON, options)
if err != nil {
    return err
}
evaluationPlan, err := matchsystem.CompileEvaluation(config, schema, options)
// 一步入口：matchsystem.CompileEvaluationJSON(rawEvaluationJSON, schema, options)
~~~

解析阶段复用 `expression.DecodeRootInto`；Compile 阶段再校验真实 Contract、index
binding、Fact scope、phase capability、DomainLeaf 和 scorer。JSON limits 使用共享
`expression.JSONLimits`，不另造一套 Contract、AST 或 compiler。

## 10. 清理边界与验收

当前文档和 API 不再提供旧 builder/plan alias、重复 Contract parser、重复 expression
adapter、旧 JSON 正向加载或 phase 兼容入口。旧版本文字只能出现在明确的 unsupported
version 说明或负向测试名称中，不能作为可加载示例。

验收重点：

- 五种 ResultType、中央 Kind/Profile、父子 shape、source/capability、scope、cycle 和 limits；
- typed/JSON roots 共享 Program canonical、Requirements 和 Prefilter fingerprint；
- nested JSON operand 的 Root envelope 严格校验，InstructionID 属于所属 Program，sidecar 不拥有子 Program；
- Prefilter anchor/Exclude/If、index binding、Fact scope 和无全池扫描 fallback；
- 每 LogicalNode 一个 scorer；Join 输入权限；OnJoin 整批原子 Match Fact update；
- Complete 只读 Match/Tick Fact 且为 Bool root；错误 fail closed 且无后续副作用。

本次文档同步只更新 README/doc，不修改 Go、不提交。独立检查应包括：

~~~text
local Markdown links
Markdown fences
legacy API / v1 positive references
git diff --check
~~~
