# CandidatePlan JSON 配置与热更新实现方案

> 状态：设计草案，本文中的 JSON Schema、Go API 和类型名称尚未在当前代码中实现。
>
> 前置设计：[CandidatePlan 树形索引初筛层设计](candidate-index-filtering.md)。本文只描述如何把该设计配置化并安全热更新，不改变 CandidatePlan、CandidateIndex 和 GroupEvaluator 的职责边界。

## 1. 目标与非目标

目标是把 CandidatePlan（候选计划）、IndexQuery（索引查询）、索引声明和运行参数从 Go 代码移到 JSON，使同一套通用匹配核心能够：

- 从 JSON 构建并编译完整的树形索引初筛规则。
- 在进程不重启、Ticket 不迁移的前提下更新过滤规则。
- 保证一个 MatchSystem Tick（系统匹配周期）内只看到同一个不可变规则版本。
- 在更新失败时继续使用最后一个有效版本。
- 对只改计划和需要重建索引的更新采用不同发布路径。
- 保持严格索引执行，不把配置错误退化为全池 Ticket 扫描。

本文不包含：

- Router（路由器）的热更新；Ticket 仍由调用方路由到唯一 OwnerNode（所有者节点）。
- GroupEvaluator（组评估器）业务逻辑的动态代码加载。
- 任意脚本、表达式语言或 JSON 内嵌 Go 函数。
- 旧 CandidateFilter、旧 RuleEngine 或其他兼容入口。
- 具体业务字段和业务规则。

CandidatePlan 仍然只产生候选安全超集，最终正确性仍由 GroupEvaluator 保证。

## 2. 核心结论

JSON 不是直接执行的规则。它必须经过严格解码、结构校验、语义编译和资源准备，形成一个不可变的 `PlanGeneration`（计划代），才允许在 Tick 边界发布。

```text
JSON bytes
  -> Strict Decode
  -> Schema Validate
  -> Semantic Compile
  -> Resolve Registry
  -> Prepare Index Generation（必要时）
  -> Prepared Generation
  -> Tick Boundary Atomic Activate
  -> Immutable Active Generation
```

热更新的最小一致性单位是整个 MatchSystem，而不是单个 seed，也不是单个 OwnerNode。一次 Tick 开始时固定读取 `activeGeneration`，直到该 Tick 完成前不再读取全局当前版本。

更新失败的统一语义是：

```text
reject new generation + keep old active generation
```

错误绝不等于 `Empty`，不等于 `Universe`，也不触发扫描回退。

## 3. 配置模型

### 3.1 顶层信封

每份配置使用以下顶层结构：

```json
{
  "schemaVersion": "candidate-plan/v1",
  "configId": "generic-default",
  "revision": 42,
  "indexes": [],
  "facts": [],
  "plan": {},
  "runtime": {}
}
```

字段含义：

| 字段 | 含义 |
| --- | --- |
| `schemaVersion` | JSON 结构和语义版本。未知版本直接拒绝。 |
| `configId` | 配置逻辑身份；revision 在同一 configId 内单调递增。 |
| `revision` | 发布序号，只接受大于当前已接受 revision 的配置。 |
| `indexes` | 当前计划允许依赖的物理索引声明。 |
| `facts` | 当前计划依赖的已注册 Fact（事实）提供者。 |
| `plan` | CandidatePlan 根节点。 |
| `runtime` | 与执行资源有关、允许配置化的有界参数。 |

`PlanFingerprint` 不由配置提供。系统对完成默认值填充和语义规范化后的配置生成指纹，避免空格、对象字段顺序等 JSON 表面差异导致无意义更新。指纹必须覆盖所有会改变候选结果或执行契约的内容，包括规范化 AST、Query、运行参数、索引契约和 Fact 依赖；不能只对原始 JSON 字节做哈希。

### 3.2 封闭的 Tagged Union

所有可变结构都采用 Tagged Union（带类型标签的封闭联合）：

```json
{
  "type": "filter",
  "query": {
    "type": "multi_key",
    "index": "dimension_a_index",
    "operator": "any",
    "values": {
      "type": "seed_field_strings",
      "field": "dimension_a"
    }
  }
}
```

`type` 只能取核心注册表中已知的固定值。首版不允许插件通过 JSON 注册任意实现，也不允许配置出现函数名、脚本、正则回调或 Ticket 遍历器。

这种限制使配置仍然是数据，而不是远程代码执行入口。

## 4. JSON AST

### 4.1 CandidatePlan 节点

首版允许以下节点：

| `type` | 必需字段 | 语义 |
| --- | --- | --- |
| `filter` | `query` | 执行一个已注册索引上的声明式查询。 |
| `all` | `children` | 子结果执行 Bitmap AND（位图交集）。 |
| `any` | `children` | 子结果执行 Bitmap OR（位图并集）。 |
| `branch` | `when`、`then`、`else` | 只执行谓词选中的一条路径。 |
| `not` | `child` | 在所在执行路径的正向候选域上执行 AndNot（锚定差集）。 |
| `empty` | 无 | 显式空结果。 |

示例：

```json
{
  "type": "all",
  "children": [
    { "type": "filter", "query": {} },
    { "type": "not", "child": { "type": "filter", "query": {} } }
  ]
}
```

`not` 不是全局反转。编译器会把 `All(P, Not(N))` 规范化为 `P AND NOT N`，并证明每条可运行路径在执行 Not 前都已建立正向候选域。以下形式必须拒绝：

```text
Not(Filter(...))                    // 根节点无正向锚点
Any(Not(Filter(...)), Filter(...))  // Not 所在分支无正向锚点
```

空 `all`、空 `any`、缺少任一分支的 `branch` 均为配置错误；空结果必须显式使用 `empty`。

### 4.2 MultiKey Query

`multi_key` 查询 MultiKeyIndex（多值倒排索引）：

```json
{
  "type": "multi_key",
  "index": "dimension_a_index",
  "operator": "any",
  "values": {
    "type": "union_strings",
    "items": [
      { "type": "seed_field_strings", "field": "dimension_a" },
      { "type": "seed_field_strings", "field": "dimension_a_relaxed" },
      { "type": "fact_strings", "fact": "extra_values" }
    ]
  }
}
```

`operator: "any"` 表示同一个 Query 中多个值取 OR。不同 Filter 之间是否 AND 或 OR，由外层 `all` 或 `any` 决定。

字符串值表达式首版只允许：

- `literal_strings`：配置中的常量字符串数组。
- `seed_field_strings`：读取 seed 的一个多值字符串字段。
- `fact_strings`：读取 Tick Fact 的字符串数组。
- `union_strings`：合并多个字符串值表达式并去重。

`MaxDocumentKeyCount` 限制单个 Ticket 在某个索引字段中可写入的 key 数量；`MaxQueryKeyCount` 限制一次绑定完成后的 Query key 数量。两者分别在 Ticket Add 和 seed Query 绑定时检查，不可混用。

### 4.3 Numeric Range Query

`numeric_range` 查询 NumericRangeIndex（数值范围索引）：

```json
{
  "type": "numeric_range",
  "index": "numeric_value_index",
  "min": {
    "type": "sub_int64",
    "left": { "type": "seed_numeric", "field": "numeric_value" },
    "right": {
      "type": "wait_steps_int64",
      "wait": { "type": "seed_wait_ms" },
      "stepMs": 10000,
      "values": [10, 20, 40, 80]
    }
  },
  "max": {
    "type": "add_int64",
    "left": { "type": "seed_numeric", "field": "numeric_value" },
    "right": {
      "type": "wait_steps_int64",
      "wait": { "type": "seed_wait_ms" },
      "stepMs": 10000,
      "values": [10, 20, 40, 80]
    }
  },
  "includeMin": true,
  "includeMax": true
}
```

Int64 表达式首版允许：

- `literal_int64`
- `seed_numeric`
- `seed_wait_ms`，运行时计算 `max(0, now - seed.CreatedAt)`
- `fact_int64`
- `wait_steps_int64`
- `add_int64`
- `sub_int64`

所有算术都使用 checked arithmetic（检查溢出的算术）。溢出、缺失必需字段或绑定后 `min > max` 是该 seed 的运行期查询错误，不能解释为空结果。

`wait_steps_int64` 的 `values[i]` 在等待时间达到 `i * stepMs` 后生效，超过最后一步时固定使用最后一个值。`stepMs` 必须大于零，`values` 不得为空，元素数量不得超过索引契约上限。

查询值随等待时间变化但执行结构不变时使用 `wait_steps_int64`；等待时间导致整条执行路径变化时使用 `branch`。

### 4.4 Branch Predicate

Branch Predicate（分支谓词）只允许读取 seed、`now` 和不可变 Tick Fact，不允许读取 candidate、group、Bitmap 或索引基数。

```json
{
  "type": "branch",
  "when": {
    "type": "gte_int64",
    "left": { "type": "seed_wait_ms" },
    "right": { "type": "literal_int64", "value": 30000 }
  },
  "then": { "type": "filter", "query": {} },
  "else": { "type": "filter", "query": {} }
}
```

首版谓词采用封闭集合，例如 `eq_strings`、`gte_int64`、`lt_int64`、`and`、`or`。每种谓词在注册表中声明输入类型和最大复杂度，不能接受自定义函数。

## 5. 索引和 Fact 声明

### 5.1 索引声明

```json
{
  "name": "dimension_a_index",
  "type": "multi_key",
  "field": "dimension_a",
  "maxDocumentKeyCount": 64,
  "maxQueryKeyCount": 64
}
```

```json
{
  "name": "numeric_value_index",
  "type": "numeric_range",
  "field": "numeric_value"
}
```

JSON 只选择核心中已注册的索引类型并提供有界参数。索引实现本身仍由 Go 注册表提供：

```go
// 设计草案
type IndexTypeRegistry interface {
    Resolve(typeName string) (IndexFactory, bool)
}
```

索引名在一份配置内必须唯一。Query 的 `index` 按名称绑定到索引声明；Query 类型、操作符和值表达式输出类型必须与索引契约完全一致。

### 5.2 Fact 声明

```json
{
  "name": "extra_values",
  "provider": "registered_string_values",
  "valueType": "strings",
  "maxValueCount": 32
}
```

`provider` 只能引用启动时注册的 FactProvider（事实提供器）。配置不能定义 provider 代码。编译器验证：

- provider 存在；
- provider 输出类型等于声明类型；
- Query 或 Predicate 的读取类型与 Fact 一致；
- 动态值数量上限足以满足引用它的索引契约。

FactProvider 每个 Tick 只计算一次并产生只读快照。

## 6. 完整 JSON 示例

以下配置只使用抽象字段。它同时展示多值组合、等待放宽、动态数值范围、Branch 和锚定 Not。

```json
{
  "schemaVersion": "candidate-plan/v1",
  "configId": "generic-default",
  "revision": 42,
  "indexes": [
    {
      "name": "dimension_a_index",
      "type": "multi_key",
      "field": "dimension_a",
      "maxDocumentKeyCount": 64,
      "maxQueryKeyCount": 64
    },
    {
      "name": "dimension_b_index",
      "type": "multi_key",
      "field": "dimension_b",
      "maxDocumentKeyCount": 32,
      "maxQueryKeyCount": 64
    },
    {
      "name": "numeric_value_index",
      "type": "numeric_range",
      "field": "numeric_value"
    }
  ],
  "facts": [
    {
      "name": "extra_values",
      "provider": "registered_string_values",
      "valueType": "strings",
      "maxValueCount": 16
    }
  ],
  "plan": {
    "type": "all",
    "children": [
      {
        "type": "filter",
        "query": {
          "type": "multi_key",
          "index": "dimension_a_index",
          "operator": "any",
          "values": {
            "type": "union_strings",
            "items": [
              { "type": "seed_field_strings", "field": "dimension_a" },
              { "type": "seed_field_strings", "field": "dimension_a_relaxed" },
              { "type": "fact_strings", "fact": "extra_values" }
            ]
          }
        }
      },
      {
        "type": "branch",
        "when": {
          "type": "gte_int64",
          "left": { "type": "seed_wait_ms" },
          "right": { "type": "literal_int64", "value": 30000 }
        },
        "then": {
          "type": "any",
          "children": [
            {
              "type": "filter",
              "query": {
                "type": "multi_key",
                "index": "dimension_b_index",
                "operator": "any",
                "values": { "type": "seed_field_strings", "field": "dimension_b" }
              }
            },
            {
              "type": "filter",
              "query": {
                "type": "multi_key",
                "index": "dimension_b_index",
                "operator": "any",
                "values": { "type": "literal_strings", "values": ["fallback"] }
              }
            }
          ]
        },
        "else": {
          "type": "filter",
          "query": {
            "type": "multi_key",
            "index": "dimension_b_index",
            "operator": "any",
            "values": { "type": "seed_field_strings", "field": "dimension_b" }
          }
        }
      },
      {
        "type": "filter",
        "query": {
          "type": "numeric_range",
          "index": "numeric_value_index",
          "min": {
            "type": "sub_int64",
            "left": { "type": "seed_numeric", "field": "numeric_value" },
            "right": {
              "type": "wait_steps_int64",
              "wait": { "type": "seed_wait_ms" },
              "stepMs": 10000,
              "values": [10, 20, 40, 80]
            }
          },
          "max": {
            "type": "add_int64",
            "left": { "type": "seed_numeric", "field": "numeric_value" },
            "right": {
              "type": "wait_steps_int64",
              "wait": { "type": "seed_wait_ms" },
              "stepMs": 10000,
              "values": [10, 20, 40, 80]
            }
          },
          "includeMin": true,
          "includeMax": true
        }
      },
      {
        "type": "not",
        "child": {
          "type": "filter",
          "query": {
            "type": "multi_key",
            "index": "dimension_a_index",
            "operator": "any",
            "values": { "type": "literal_strings", "values": ["excluded"] }
          }
        }
      }
    ]
  },
  "runtime": {
    "candidateLimitPerSeed": 128,
    "bitmapProbeThreshold": 4096,
    "maxBoundQueryKeys": 64
  }
}
```

该配置的集合语义可简化为：

```text
A
AND Branch(wait, B_relaxed, B_strict)
AND NumericRange(seed.numeric_value ± wait_step)
AND NOT A_excluded
```

最外层前三个 Filter/Branch 提供正向候选域，因此最后一个 Not 是锚定差集。它不会构造全池 Universe 的补集。

## 7. 严格解码和校验

配置进入编译器前必须经过四层检查。

### 7.1 字节和 JSON 语法限制

- 限制配置字节数，例如首版 1 MiB。
- 必须是单个 JSON 对象，尾部不得存在第二个 JSON 值。
- 拒绝重复对象 key，避免不同解析器采用“第一个生效”或“最后一个生效”造成歧义。
- 必需对象、数组和标量均拒绝 `null`，不能用 `null` 隐式表达默认值或 Empty。
- 拒绝无效 UTF-8、非整数 revision 和超出 int64/uint64 的数值。
- 不允许注释、NaN、Infinity 或实现相关扩展。

Go 标准库 `encoding/json` 的普通结构体解码不能单独发现所有重复 key。实现时应先用 token visitor（令牌访问器）遍历对象并检测重复 key，再用 `json.Decoder` 和 `DisallowUnknownFields` 解码强类型结构。

### 7.2 JSON Schema 结构校验

为 `candidate-plan/v1` 固定一份随二进制发布的 JSON Schema。所有对象都使用等价于 `additionalProperties: false` 的约束，并限制：

- AST 最大深度；
- 总节点数；
- 单个 `all`/`any` 的子节点数；
- Branch 数量；
- 字符串长度、常量数组长度和 WaitSteps 步数；
- 索引、Fact 和动态 QueryKey 数量。

JSON Schema 用于结构和基础范围检查；它不代替 CandidatePlan 编译器的路径语义检查。

### 7.3 注册表解析

索引类型、Query 类型、表达式类型、谓词类型和 FactProvider 都必须从启动时构造的只读注册表解析。未知 `type` 或 `provider` 直接报告带 JSON Path 的错误。

首版注册表在进程启动后冻结，热更新不能修改可执行代码集合。

### 7.4 语义编译

编译器执行：

1. AST 无环、非空组合节点和深度检查。
2. 字段类型、Query 类型、操作符和表达式返回类型检查。
3. 索引名唯一性与引用完整性检查。
4. Fact 依赖、动态 QueryKey 上限和数值表达式边界检查。
5. Branch 每条路径的正向锚点验证。
6. Not 的路径数据流验证，禁止无锚点反转。
7. 生成规范化 AST、`IndexContract` 和可重排执行操作图。
8. 生成基于规范化语义的 `PlanFingerprint`。

典型错误必须包含阶段、配置版本和路径：

```text
compile configId=generic-default revision=43
path=$.plan.children[2].query.min.right
code=WAIT_STEPS_EMPTY
message=wait_steps_int64.values must contain at least one value
```

```text
compile configId=generic-default revision=43
path=$.plan.children[3]
code=UNANCHORED_NOT
message=not has no positive candidate domain on this execution path
```

## 8. 编译产物与运行对象

建议把配置 DTO（数据传输对象）、编译态和运行态彻底分离：

```go
// 设计草案
type PlanGeneration struct {
    ConfigID       string
    SchemaVersion  string
    Revision       uint64
    GenerationID   uint64
    Fingerprint    PlanFingerprint
    IndexContract  IndexContract
    IndexGenerationID uint64
    FactContractRevision uint64
    Program        *CandidateProgram
    Runtime        RuntimeLimits
    NodeIndexes    map[NodeID]*IndexGeneration
}
```

`PlanGeneration` 发布后完全不可变。CandidateProgram 保存已解析的操作码或执行节点，不在 seed 执行期间查询 JSON map，也不进行字符串类型分派。Generation 必须把 Program、IndexGeneration、Fact 契约和 schemaVersion 成组绑定，不能只替换 Program 指针。

`IndexContract` 至少包含：

- 索引逻辑名、实现类型、Ticket 字段及字段类型；
- 文档 key 和 Query key 上限；
- NumericRange 的数值类型和边界语义；
- 依赖的 Fact 名称和类型；
- 编译器与运行时 ABI（应用二进制接口）版本。

运行时只接受由同一编译器版本产生且契约已满足的 Program。

## 9. 热更新状态机

### 9.1 状态

每个 configId 至少公开以下状态：

```text
Idle
  -> Decoding
  -> Compiling
  -> PreparingIndexes（可跳过）
  -> Ready
  -> Active

任一准备阶段 -> Rejected（Active 保持不变）
```

同时保存：

- `activeRevision`、`activeFingerprint`
- `pendingRevision`、准备阶段和进度
- `lastRejectedRevision`、错误码、JSON Path 和时间

### 9.2 Revision 和幂等

规则如下：

- `revision <= activeRevision`：拒绝为 stale revision（过期版本）。
- revision 更大但 fingerprint 与 active 相同：记录为 no-op（无操作），推进已接受 revision，不重建索引、不切换 Program。
- 同一 configId 同时只准备最新 revision；新 revision 到来后取消可取消的旧任务，不能取消时丢弃旧任务结果。
- 准备结果发布前再次比较 revision，避免慢任务覆盖新版本。

回滚不降低 revision。应提交一个更高 revision，其内容等于目标历史版本；这样审计顺序和并发控制保持单调。

## 10. 两类更新路径

### 10.1 Plan-only 更新

如果新旧 `IndexContract` 兼容，即新 Program 所需的每个索引和 Fact 都已由当前 Generation 满足，则无需重建物理索引：

```text
decode -> compile -> contract compatibility -> Ready -> next Tick activate
```

常见情况包括：

- 调整 Branch 阈值；
- 调整 WaitSteps；
- 改变 All/Any 的组合结构；
- 修改常量 Query 值；
- 调整 CandidateLimitPerSeed 等安全范围内的运行参数。

编译器仍需完整验证所有路径，不能把“小改动”直接 patch 到当前 Program。

这里的兼容是有方向的：`active IndexGeneration` 必须满足 `new Program.requiredContract`。实现应逐项比较索引类型、字段、字段类型、缺失值语义、多值展开方式、边界语义、编码版本和上限，不能只比较索引名称。新计划不再引用的索引可以暂时保留，待旧 Generation 无引用后回收。

### 10.2 Index-contract 更新

新增索引、删除被依赖索引、改变索引字段、索引类型或 key 上限时，需要准备新的 `IndexGeneration`（索引代）。活动索引不得原地改变结构。

推荐流程：

1. 核心 actor（串行状态执行器）记录每个 OwnerNode 的 `baseSequence`，并取得不可变 Ticket/Active 快照引用。
2. 后台 worker 根据快照构建 shadow indexes（影子索引），不影响当前匹配。
3. 构建期间，actor 正常处理 Add/Remove，并把 `baseSequence` 之后的变更写入有界 delta log（增量日志）。
4. shadow 构建完成后重放 delta，直到追平当前 sequence。
5. 在 actor 上做最终短暂同步，验证 Ticket 数、Active Bitmap 和各索引不变量。
6. 所有 OwnerNode 都准备成功后，形成一个完整 Prepared Generation。
7. 在下一个 MatchSystem Tick 开始前一次性切换全局 active generation。

这里允许索引维护任务遍历基线 Ticket 来构建新索引，因为它不在候选查询路径上。任何查询都不能以该维护遍历作为缺失索引时的回退。

如果 delta log 达到容量上限、构建取消或任一节点失败，则丢弃 shadow generation，活动 generation 不变。实现可以在后台分段重放 delta，但最终发布检查必须由 actor 串行完成。

## 11. Tick 边界原子切换

MatchSystem 的执行入口应固定当前代：

```go
// 设计草案
func (s *MatchSystem) Tick(now int64) {
    s.activatePreparedBeforeTick()
    generation := s.activeGeneration

    facts := generation.snapshotFacts(now)
    for _, node := range s.ownerNodes {
        node.tick(generation, facts, now)
    }
}
```

关键不变量：

- `generation` 在整个 Tick 内固定。
- 同一个 Tick 中所有 OwnerNode 使用相同 revision 和 fingerprint。
- `generation.Program.RequiredIndexContract` 必须等于 generation 记录的绑定契约；每个 IndexGeneration 必须满足该绑定契约，Fact 契约版本也必须匹配。
- 未选中的 Branch 不访问索引。
- JSON、编译器和索引准备任务不能直接修改 active generation。
- 发布只通过 actor 的单一命令入口完成。

在单 goroutine 核心模型下，JSON 解析、编译和影子索引构建可以在 worker goroutine 上运行，但 worker 只能返回不可变 Prepared Generation；活动状态的切换、delta 最终追平和回收决定由核心 actor 完成。

旧 generation 在没有 Tick 引用后才能回收。首版若 Tick 严格串行，则切换后的上一个 generation 可在该 Tick 结束后释放；为了诊断和快速回滚，也可以保留有限数量的只读历史 generation，但必须设置内存上限。

## 12. 多 OwnerNode 的一致发布

同一配置应用到多个 OwnerNode 时必须 all-or-none（全有或全无）：

```text
compile once
  -> prepare node-1 indexes
  -> prepare node-2 indexes
  -> ...
  -> all ready
  -> publish one system generation
```

任一节点失败时，不得让其余节点先使用新版本。Prepared Generation 中应包含所有目标节点的 IndexGeneration 引用，发布命令只接受完整集合。

Router 不参与本次事务。配置变更不会重新路由、复制或迁移已存在 Ticket。

## 13. 配置输入接口

核心只接受字节和来源元数据，不把文件监听、远程配置协议与编译器耦合：

```go
// 设计草案
type ConfigUpdate struct {
    Source      string
    ReceivedAt int64
    Bytes       []byte
}

type ApplyResult struct {
    ConfigID    string
    Revision    uint64
    Fingerprint PlanFingerprint
    Status      ApplyStatus
}

func (s *MatchSystem) ApplyCandidateConfigJSON(
    ctx context.Context,
    raw []byte,
) (ApplyResult, error)

func (s *MatchSystem) CandidateConfigStatus(
    configID string,
) PlanStatus
```

可选的外部适配层：

```go
// 设计草案
type ConfigSource interface {
    Watch(ctx context.Context, emit func(ConfigUpdate) error) error
}
```

首版建议同时提供：

- `ApplyCandidateConfigJSON`：测试、控制面和嵌入式调用的唯一核心入口。
- `FilePollingSource`：轮询本地文件的简单适配器。

文件发布方应使用“写临时文件、flush、原子 rename”协议。监听器需要在文件稳定后完整读取，再调用 Apply；遇到文件缺失、半写入或非法 JSON 时保留旧版本。文件监听只是输入方式，revision、指纹、编译和原子发布仍由 PlanManager 统一处理。

## 14. 错误模型

### 14.1 构建期错误

构建期错误包括：

- JSON 语法、重复 key、未知字段或未知 schemaVersion；
- 超过大小、深度、节点数或动态值数量限制；
- 未注册索引、Fact、表达式或谓词；
- Query 与索引类型不匹配；
- AST 循环、空组合节点、无锚点 Not；
- Branch 读取 candidate、group 或候选集合；
- IndexContract 不可满足或影子索引构建失败。

这些错误均拒绝新 revision，并保留当前 active generation。

### 14.2 运行期错误

合法配置仍可能因单个 seed 数据不满足契约而发生运行期错误，例如：

- seed 缺少必需字段；
- 绑定值超过 MaxQueryKeyCount；
- int64 算术溢出；
- 动态范围 Min 大于 Max；
- FactProvider 本 Tick 返回错误或超限值。

运行期错误必须携带 configId、revision、node、seed 和表达式路径，并按既定策略跳过该 seed 或终止本 Tick。首版建议隔离到 seed，同时提高错误指标；无论选择哪种策略，都不能把错误解释成 Empty、Universe 或扫描回退。

## 15. 可观测性和审计

至少暴露：

- active/pending/rejected revision 和 fingerprint；
- decode、validate、compile、shadow build、delta replay、activate 各阶段耗时；
- 每个 OwnerNode 的索引构建进度、文档数、posting 数和估算内存；
- delta log 长度、追平延迟和溢出次数；
- no-op、stale、reject、activate 和 rollback 计数；
- 带 JSON Path 的最近拒绝原因；
- 每个 Tick 实际使用的 revision。

日志不能输出完整 Ticket 或任意动态字段值。审计记录至少保存 configId、revision、fingerprint、来源、接收时间、激活时间和结果。

## 16. 资源与安全限制

建议把以下限制放入进程启动配置，由服务拥有者控制，而不是允许规则 JSON 自行提高：

| 限制 | 建议首值 |
| --- | ---: |
| JSON 最大字节数 | 1 MiB |
| AST 最大深度 | 32 |
| AST 最大节点数 | 4096 |
| 单组合节点最大 children | 256 |
| WaitSteps 最大步数 | 256 |
| 单配置最大索引数 | 128 |
| 单配置最大 Fact 数 | 128 |
| 同时准备的 generation | 每个 configId 1 个 |

这些数值是实现初值，最终通过压测校准。JSON 中的 runtime 参数只能在服务端硬上限以内降低或选择值，不能突破硬上限。

编译和索引构建接受 Context 取消和超时。任何耗时任务完成时都要再次核对 configId、revision 和任务 token，防止已经过期的结果被发布。

## 17. 实现分层和落地顺序

建议按以下包内职责拆分；实际文件名可随现有工程结构调整：

```text
candidateconfig/
  dto.go               JSON DTO 和 tagged union 原始结构
  decode.go            重复 key、unknown field、大小与深度限制
  schema.go            schemaVersion 和结构校验
  normalize.go         默认值填充与规范化

candidateplan/
  compile.go           DTO -> typed AST -> executable program
  contract.go          IndexContract 与兼容性比较
  fingerprint.go       规范化语义指纹

matchsystem/
  generation.go        immutable PlanGeneration
  plan_manager.go      revision、prepare、publish、status
  index_rebuild.go     snapshot + shadow build + delta replay
  config_source.go     可选输入适配接口
```

落地顺序：

1. 固化当前代码内 CandidatePlan 的 typed AST、编译器和 IndexContract，不先引入热更新。
2. 增加 JSON DTO、严格解码和 DTO 到 typed AST 的转换；JSON 与代码构造必须复用同一个语义编译器。
3. 增加不可变 PlanGeneration，并让 Tick 显式持有一个 generation。
4. 实现 Plan-only 的 Apply、revision、fingerprint、状态和 Tick 边界切换。
5. 实现 shadow index、mutation sequence、delta log 和索引契约更新。
6. 实现多 OwnerNode 的全有或全无发布。
7. 增加 FilePollingSource 或外部控制面适配器。
8. 完成故障注入、并发一致性和百万 Ticket 压测后再开放生产热更新。

第 2 步不能另写一套 JSON 专用执行器；否则代码构造和 JSON 构造会出现不同语义。

## 18. 测试与验收

### 18.1 解码和编译

- 未知字段、重复 key、尾随 JSON、未知 type 和未知 schemaVersion 均被拒绝。
- AST 深度、节点数、字符串长度和数组长度限制生效。
- JSON 构造与等价 typed AST 产生相同 IndexContract、PlanFingerprint 和候选 Bitmap。
- 每条 Branch 路径和每个 Not 都完成正向锚点验证。
- 未选中的 Branch 不读取索引或 Fact。

### 18.2 热更新一致性

- Plan-only 更新不重建索引。
- 一个 Tick 只能观察到旧版本或新版本，不能混用。
- 多 OwnerNode 同时切换，任一节点准备失败时全部保持旧版本。
- stale revision 和过期后台任务不能覆盖新版本。
- 相同 fingerprint 是 no-op。
- 非法 JSON、编译失败和超时均不影响当前匹配。
- 回滚通过更高 revision 完成，并可追溯到目标 fingerprint。

### 18.3 索引重建

- 在 shadow build 期间持续 Add/Remove Ticket，delta replay 后与串行 oracle（基准实现）一致。
- delta log 溢出时拒绝更新，不损坏活动索引。
- 发布前逐项验证 Active Bitmap、Ticket 数和索引 posting 不变量。
- 初筛查询期间绝不读取基线 Ticket 作为缺失索引回退。

### 18.4 规模和故障注入

- 在 10 万、50 万和 100 万 Ticket 下记录编译、重建、追平和切换耗时。
- 记录活动代与影子代同时存在时的峰值内存。
- 注入 worker 取消、Fact 失败、索引构建失败、进程关闭和连续快速 revision。
- 验证发布切换为有界常数操作，长时间工作均发生在 Tick 外。

验收的最终不变量是：

```text
配置可热更新
+ Tick 内版本一致
+ 更新失败保留旧版本
+ 索引契约变化使用影子索引
+ 查询路径零全池扫描回退
+ GroupEvaluator 继续负责最终正确性
```
