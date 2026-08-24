# Prefilter JSON 配置与热更新实现方案

> 状态：JSON 解析与热更新仍是设计草案；其依赖的 Prefilter、索引、编译器和 TickSession 已在 `internal/matchsystem/prefilter` 实现，PhysicalNode/LogicalNode 基线已在 `internal/matchsystem` 实现。
>
> 前置设计：[Prefilter 树形索引初筛层设计](index-prefiltering.md)。本文只描述如何把该设计配置化并安全热更新，不改变 Prefilter、IndexStore 和 GroupEvaluator 的职责边界。
>
> 拓扑作用域：引入 [Router 物理节点与逻辑节点设计](router-physical-logical-node.md) 后，MatchService 是匹配服务器，PhysicalNode 是与其一一对应的算法实例。本文的 `MatchSystem` 指一个 `RuleKey` 的本地 RuleRuntime（规则运行时），不是承载多个 RuleID 的整个 PhysicalNode；本文 `NodeID` / `OwnerID` 对应稳定的 `LogicalNodeKey`，`OwnerRef` 只是 `LogicalNodeKey + PhysicalNodeID` 的固定路由引用。Router 只运行在 Ticket 调用节点的 client-side routing（客户端路由）与 MatchService 本地分发中，不协调跨物理 Placement 发布；本地 PlanGeneration 不持有远程索引指针。
>
> 执行作用域：外部 MatchService 调用唯一 `PhysicalNode.Tick`；PhysicalNode 内的 LogicalNodeSelector 选择一个 LogicalNode，并同步调用该节点的一次本地匹配。匹配核心不关心 MatchService 的调度、限速和服务器 IO；LogicalNode 自身不运行独立 Tick。

## 1. 目标与非目标

目标是把 Prefilter（候选计划）、IndexQuery（索引查询）、索引声明和运行参数从 Go 代码移到 JSON，使同一套通用匹配核心能够：

- 从 JSON 构建并编译完整的树形索引初筛规则。
- 在进程不重启、固定 Placement 和 Ticket 路由都不改变的前提下更新过滤规则；进程崩溃后的空状态原地重启由 Router 生命周期负责，旧 Ticket 不恢复。
- 保证一个 LogicalNode 在一次 PhysicalNode Tick 的本地执行内只看到同一个不可变规则版本。
- 在更新失败时继续使用最后一个有效版本。
- 对只改计划和需要重建索引的更新采用不同发布路径。
- 保持严格索引执行，不把配置错误退化为全池 Ticket 扫描。

本文不包含：

- Router（路由器）的热更新；Ticket 仍由调用方路由到唯一 LogicalNode / OwnerRef（逻辑节点 / 所有者引用）。
- GroupEvaluator（组评估器）业务逻辑的动态代码加载。
- 任意脚本、表达式语言或 JSON 内嵌 Go 函数。
- 旧 CandidateFilter、旧 RuleIndexStore 或其他兼容入口。
- 具体业务字段和业务规则。

Prefilter 仍然只产生候选安全超集，最终正确性仍由 GroupEvaluator 保证。

## 2. 核心结论

JSON 不是直接执行的规则。它必须经过严格解码、结构校验、语义编译和资源准备，形成一个不可变的 `PlanGeneration`（计划代），才允许在该 LogicalNode 被 PhysicalNode Tick 选中执行的边界发布。

```text
JSON bytes
  -> Strict Decode
  -> Schema Validate
  -> Semantic Compile
  -> Resolve Registry
  -> Prepare Index Generation（必要时）
  -> Prepared Generation
  -> PhysicalNode Tick Execution Boundary Atomic Activate
  -> Immutable Active Generation
```

热更新的最小一致性单位是一个 RuleKey 的本地 RuleRuntime（本文原称整个 MatchSystem），而不是单个 seed。该 LogicalNode 被本物理节点 Tick 的 LogicalNodeSelector 选中后，在本次执行开始时固定读取 `activeGeneration`，直到本次执行完成前不再读取当前版本。一个 PhysicalNode 内不同 RuleKey 的逻辑节点各自拥有独立的 PlanManager 和 activeGeneration。

更新失败的统一语义是：

```text
reject new generation + keep old active generation
```

错误绝不等于 `None`，不等于 `Universe`，也不触发扫描回退。

## 3. 配置模型

### 3.1 顶层信封

每份配置使用以下顶层结构：

```json
{
  "schemaVersion": "prefilter/v1",
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
| `facts` | 当前计划依赖的 Fact 契约；值可由 Tick FactProvider 或 SeedFactProvider 提供。 |
| `plan` | Prefilter 根节点。 |
| `runtime` | 与执行资源有关、允许配置化的有界参数。 |

`Fingerprint` 不由配置提供。系统对完成默认值填充和语义规范化后的配置生成指纹，避免空格、对象字段顺序等 JSON 表面差异导致无意义更新。指纹必须覆盖所有会改变候选结果或执行契约的内容，包括规范化 过滤表达式、Query、运行参数、索引契约和 Fact 依赖；不能只对原始 JSON 字节做哈希。

### 3.2 封闭的 Tagged Union

所有可变结构都采用 Tagged Union（带类型标签的封闭联合）：

```json
{
  "type": "lookup",
  "query": {
    "type": "multi_value",
    "index": "dimension_a_index",
    "values": {
      "type": "seed_strings",
      "field": "dimension_a"
    }
  }
}
```

`type` 只能取核心注册表中已知的固定值。首版不允许插件通过 JSON 注册任意实现，也不允许配置出现函数名、脚本、正则回调或 Ticket 遍历器。

这种限制使配置仍然是数据，而不是远程代码执行入口。

## 4. JSON 过滤表达式

### 4.1 Prefilter 节点

首版允许以下节点：

| `type` | 必需字段 | 语义 |
| --- | --- | --- |
| `lookup` | `query` | 执行一个已注册索引上的声明式查询。 |
| `and` | `children` | 子结果执行 Bitmap AND（位图交集）。 |
| `or` | `children` | 子结果执行 Bitmap OR（位图并集）。 |
| `if` | `when`、`then`、`else` | 只执行谓词选中的一条路径。 |
| `exclude` | `child` | 在所在执行路径的正向候选域上执行 AndNot（锚定差集）。 |
| `none` | 无 | 显式空结果。 |

示例：

```json
{
  "type": "and",
  "children": [
    { "type": "lookup", "query": {} },
    { "type": "exclude", "child": { "type": "lookup", "query": {} } }
  ]
}
```

`exclude` 不是全局反转。编译器会把 `And(P, Exclude(N))` 规范化为 `P AND NOT N`，并证明每条可运行路径在执行 Exclude 前都已建立正向候选域。以下形式必须拒绝：

```text
Exclude(Lookup(...))                    // 根节点无正向锚点
Or(Exclude(Lookup(...)), Lookup(...))  // Exclude 所在分支无正向锚点
```

空 `and`、空 `or`、缺少任一分支的 `if` 均为配置错误；空结果必须显式使用 `none`。

### 4.2 MultiValue Query

`multi_value` 查询 MultiValueIndex（多值倒排索引）：

```json
{
  "type": "multi_value",
  "index": "dimension_a_index",
  "values": {
    "type": "union_strings",
    "items": [
      { "type": "seed_strings", "field": "dimension_a" },
      { "type": "seed_strings", "field": "dimension_a_relaxed" },
      { "type": "fact_strings", "fact": "extra_values" }
    ]
  }
}
```

同一个 StringQuery 或 Uint64Query 中的多个值固定取 OR；不同 Lookup 之间的组合方式由外层 `and` 或 `or` 决定。

字符串值表达式首版只允许：

- `literal_strings`：配置中的常量字符串数组。
- `seed_strings`：读取 seed 的一个多值字符串字段。
- `fact_strings`：读取 Tick Fact 的字符串数组。
- `union_strings`：合并多个字符串值表达式并去重。

`MaxDocumentValues` 限制单个 Ticket 在某个索引字段中可写入的 key 数量；`MaxQueryValues` 限制一次绑定完成后的 Query key 数量。两者分别在 Ticket Add 和 seed Query 绑定时检查，不可混用。

### 4.3 Int64Values Range Query

`int64_range` 查询 Int64RangeIndex（数值范围索引）：

```json
{
  "type": "int64_range",
  "index": "numeric_value_index",
  "min": {
    "type": "sub_int64",
    "left": { "type": "seed_int64", "field": "numeric_value" },
    "right": {
      "type": "step_int64",
      "input": { "type": "fact_int64", "fact": "wait_millis" },
      "steps": [
        { "at": 0, "value": 10 },
        { "at": 10000, "value": 20 },
        { "at": 20000, "value": 40 },
        { "at": 30000, "value": 80 }
      ]
    }
  },
  "max": {
    "type": "add_int64",
    "left": { "type": "seed_int64", "field": "numeric_value" },
    "right": {
      "type": "step_int64",
      "input": { "type": "fact_int64", "fact": "wait_millis" },
      "steps": [
        { "at": 0, "value": 10 },
        { "at": 10000, "value": 20 },
        { "at": 20000, "value": 40 },
        { "at": 30000, "value": 80 }
      ]
    }
  },
  "includeMin": true,
  "includeMax": true
}
```

Int64 表达式首版允许：

- `literal_int64`
- `seed_int64`
- `fact_int64`
- `step_int64`
- `add_int64`
- `sub_int64`

所有算术都使用 checked arithmetic（检查溢出的算术）。溢出、缺失必需字段或绑定后 `min > max` 是该 seed 的运行期查询错误，不能解释为空结果。

`step_int64` 接受任意 int64 表达式作为 `input`；`steps` 按 `at` 严格递增，选择最后一个满足 `at <= input` 的 `value`，低于首个阈值时使用首项。`steps` 不得为空，元素数量不得超过配置契约上限。

查询值随等待时间变化但执行结构不变时，让 SeedFactProvider 生成普通 `wait_millis` Fact 并使用 `step_int64`；等待时间导致整条执行路径变化时使用 `if`。

### 4.4 If Condition

If Condition（分支谓词）只允许读取 seed、`now` 和不可变 Tick Fact，不允许读取 candidate、group、Bitmap 或索引基数。

```json
{
  "type": "if",
  "when": {
    "type": "gte_int64",
    "left": { "type": "fact_int64", "fact": "wait_millis" },
    "right": { "type": "literal_int64", "value": 30000 }
  },
  "then": { "type": "lookup", "query": {} },
  "else": { "type": "lookup", "query": {} }
}
```

首版谓词采用封闭集合，例如 `eq_strings`、`gte_int64`、`lt_int64`、`and`、`or`。每种谓词在注册表中声明输入类型和最大复杂度，不能接受自定义函数。

## 5. 索引和 Fact 声明

### 5.1 索引声明

```json
{
  "name": "dimension_a_index",
  "type": "multi_value",
  "field": "dimension_a",
  "maxDocumentValues": 64,
  "maxQueryValues": 64
}
```

```json
{
  "name": "numeric_value_index",
  "type": "int64_range",
  "field": "numeric_value"
}
```

JSON 只选择核心中已注册的索引类型并提供有界参数。索引实现本身仍由 Go 注册表提供：

```go
// 设计草案
type IndexTypeRegistry interface {
    Resolve(typeName string) (IndexSpec, bool)
}
```

索引名在一份配置内必须唯一。Query 的 `index` 按名称绑定到索引声明；Query 类型和值表达式输出类型必须与索引需求完全一致。

### 5.2 Fact 声明

```json
{
  "name": "extra_values",
  "provider": "registered_string_values",
  "type": "strings",
  "maxValues": 32
}
```

`provider` 只能引用启动时注册的 Tick FactProvider 或 SeedFactProvider。配置不能定义 provider 代码。编译器验证：

- provider 存在；
- provider 输出类型等于声明类型；
- Query 或 Condition 的读取类型与 Fact 一致；
- 动态值数量上限足以满足引用它的索引契约。

FactProvider 每个 Tick 只计算一次并产生只读快照；SeedFactProvider 在每个实际求值 seed 进入 Prefilter 前计算一次。两层 Fact 不合并且禁止同名。

## 6. 完整 JSON 示例

以下配置只使用抽象字段。它同时展示多值组合、等待放宽、动态数值范围、If 和锚定 Exclude。

```json
{
  "schemaVersion": "prefilter/v1",
  "configId": "generic-default",
  "revision": 42,
  "indexes": [
    {
      "name": "dimension_a_index",
      "type": "multi_value",
      "field": "dimension_a",
      "maxDocumentValues": 64,
      "maxQueryValues": 64
    },
    {
      "name": "dimension_b_index",
      "type": "multi_value",
      "field": "dimension_b",
      "maxDocumentValues": 32,
      "maxQueryValues": 64
    },
    {
      "name": "numeric_value_index",
      "type": "int64_range",
      "field": "numeric_value"
    }
  ],
  "facts": [
    {
      "name": "extra_values",
      "provider": "registered_string_values",
      "type": "strings",
      "maxValues": 16
    },
    {
      "name": "wait_millis",
      "provider": "registered_seed_values",
      "type": "int64"
    }
  ],
  "plan": {
    "type": "and",
    "children": [
      {
        "type": "lookup",
        "query": {
          "type": "multi_value",
          "index": "dimension_a_index",
          "values": {
            "type": "union_strings",
            "items": [
              { "type": "seed_strings", "field": "dimension_a" },
              { "type": "seed_strings", "field": "dimension_a_relaxed" },
              { "type": "fact_strings", "fact": "extra_values" }
            ]
          }
        }
      },
      {
        "type": "if",
        "when": {
          "type": "gte_int64",
          "left": { "type": "fact_int64", "fact": "wait_millis" },
          "right": { "type": "literal_int64", "value": 30000 }
        },
        "then": {
          "type": "or",
          "children": [
            {
              "type": "lookup",
              "query": {
                "type": "multi_value",
                "index": "dimension_b_index",
                "values": { "type": "seed_strings", "field": "dimension_b" }
              }
            },
            {
              "type": "lookup",
              "query": {
                "type": "multi_value",
                "index": "dimension_b_index",
                "values": { "type": "literal_strings", "values": ["fallback"] }
              }
            }
          ]
        },
        "else": {
          "type": "lookup",
          "query": {
            "type": "multi_value",
            "index": "dimension_b_index",
            "values": { "type": "seed_strings", "field": "dimension_b" }
          }
        }
      },
      {
        "type": "lookup",
        "query": {
          "type": "int64_range",
          "index": "numeric_value_index",
          "min": {
            "type": "sub_int64",
            "left": { "type": "seed_int64", "field": "numeric_value" },
            "right": {
              "type": "step_int64",
              "input": { "type": "fact_int64", "fact": "wait_millis" },
              "steps": [
                { "at": 0, "value": 10 },
                { "at": 10000, "value": 20 },
                { "at": 20000, "value": 40 },
                { "at": 30000, "value": 80 }
              ]
            }
          },
          "max": {
            "type": "add_int64",
            "left": { "type": "seed_int64", "field": "numeric_value" },
            "right": {
              "type": "step_int64",
              "input": { "type": "fact_int64", "fact": "wait_millis" },
              "steps": [
                { "at": 0, "value": 10 },
                { "at": 10000, "value": 20 },
                { "at": 20000, "value": 40 },
                { "at": 30000, "value": 80 }
              ]
            }
          },
          "includeMin": true,
          "includeMax": true
        }
      },
      {
        "type": "exclude",
        "child": {
          "type": "lookup",
          "query": {
            "type": "multi_value",
            "index": "dimension_a_index",
            "values": { "type": "literal_strings", "values": ["excluded"] }
          }
        }
      }
    ]
  },
  "runtime": {
    "candidateLimitPerSeed": 128,
    "containsProbeThreshold": 4096,
    "maxBoundQueryKeys": 64
  }
}
```

该配置的集合语义可简化为：

```text
A
AND If(wait, B_relaxed, B_strict)
AND Int64Range(seed.numeric_value ± wait_step)
AND NOT A_excluded
```

最外层前三个 Lookup/If 提供正向候选域，因此最后一个 Exclude 是锚定差集。它不会构造全池 Universe 的补集。

## 7. 严格解码和校验

配置进入编译器前必须经过四层检查。

### 7.1 字节和 JSON 语法限制

- 限制配置字节数，例如首版 1 MiB。
- 必须是单个 JSON 对象，尾部不得存在第二个 JSON 值。
- 拒绝重复对象 key，避免不同解析器采用“第一个生效”或“最后一个生效”造成歧义。
- 必需对象、数组和标量均拒绝 `null`，不能用 `null` 隐式表达默认值或 None。
- 拒绝无效 UTF-8、非整数 revision 和超出 int64/uint64 的数值。
- 不允许注释、NaN、Infinity 或实现相关扩展。

Go 标准库 `encoding/json` 的普通结构体解码不能单独发现所有重复 key。实现时应先用 token visitor（令牌访问器）遍历对象并检测重复 key，再用 `json.Decoder` 和 `DisallowUnknownFields` 解码强类型结构。

### 7.2 JSON Schema 结构校验

为 `prefilter/v1` 固定一份随二进制发布的 JSON Schema。所有对象都使用等价于 `additionalProperties: false` 的约束，并限制：

- 过滤表达式最大深度；
- 总节点数；
- 单个 `and`/`or` 的子节点数；
- If 数量；
- 字符串长度、常量数组长度和 StepInt64 步数；
- 索引、Fact 和动态 QueryKey 数量。

JSON Schema 用于结构和基础范围检查；它不代替 Prefilter 编译器的路径语义检查。

### 7.3 注册表解析

索引类型、Query 类型、表达式类型、谓词类型和两类 FactProvider 都必须从启动时构造的只读注册表解析。未知 `type` 或 `provider` 直接报告带 JSON Path 的错误。

首版注册表在进程启动后冻结，热更新不能修改可执行代码集合。

### 7.4 语义编译

编译器执行：

1. 过滤表达式无环、非空组合节点和深度检查。
2. 字段类型、Query 类型和表达式返回类型检查。
3. 索引名唯一性与引用完整性检查。
4. Fact 依赖、动态 QueryKey 上限和数值表达式边界检查。
5. If 每条路径的正向锚点验证。
6. Exclude 的路径数据流验证，禁止无锚点反转。
7. 生成规范化 过滤表达式、`Requirements` 和可重排执行操作图。
8. 生成基于规范化语义的 `Fingerprint`。

典型错误必须包含阶段、配置版本和路径：

```text
compile configId=generic-default revision=43
path=$.plan.children[2].query.min.right
code=EMPTY_STEPS
message=step_int64.steps must contain at least one value
```

```text
compile configId=generic-default revision=43
path=$.plan.children[3]
code=EXCLUDE_REQUIRES_SCOPE
message=exclude has no positive candidate domain on this execution path
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
    Fingerprint    Fingerprint
    Requirements  Requirements
    IndexGenerationID uint64
    FactRequirementsRevision uint64
    Program        *PrefilterProgram
    Runtime        RuntimeLimits
    Indexes        *IndexGeneration
}
```

`Indexes` 只引用当前 LogicalNode 的本地索引代。`NodeID` / `OwnerID` 对应 Router 拓扑中的稳定 `LogicalNodeKey`，但不需要再作为 PlanGeneration 内的 map key；跨物理 Placement 只能交换配置制品、revision、fingerprint 和准备状态，不能把远程索引指针放入同一个 PlanGeneration。

`PlanGeneration` 发布后完全不可变。PrefilterProgram 保存已解析的操作码或执行节点，不在 seed 执行期间查询 JSON map，也不进行字符串类型分派。Generation 必须把 Program、IndexGeneration、Fact 契约和 schemaVersion 成组绑定，不能只替换 Program 指针。

`Requirements` 至少包含：

- 索引逻辑名、实现类型、Ticket 字段及字段类型；
- 文档 key 和 Query key 上限；
- Int64Range 的数值类型和边界语义；
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

回滚不降低 revision。应提交一个更高 revision，其内容等于目标历史版本；这样审计顺序保持单调。

## 10. 两类更新路径

### 10.1 Plan-only 更新

如果新旧 `Requirements` 兼容，即新 Program 所需的每个索引和 Fact 都已由当前 Generation 满足，则无需重建物理索引：

```text
decode -> compile -> requirements compatibility -> Ready -> next selected PhysicalNode Tick activate
```

常见情况包括：

- 调整 If 阈值；
- 调整 StepInt64；
- 改变 And/Or 的组合结构；
- 修改常量 Query 值；
- 调整 CandidateLimitPerSeed 等安全范围内的运行参数。

编译器仍需完整验证所有路径，不能把“小改动”直接 patch 到当前 Program。

这里的兼容是有方向的：`active IndexGeneration` 必须满足 `new Program.requirements`。实现应逐项比较索引类型、字段、字段类型、缺失值语义、多值展开方式、边界语义、编码版本和上限，不能只比较索引名称。新计划不再引用的索引可以暂时保留，待旧 Generation 无引用后回收。

### 10.2 Index-requirements 更新

新增索引、删除被依赖索引、改变索引字段、索引类型或 key 上限时，需要准备新的 `IndexGeneration`（索引代）。活动索引不得原地改变结构。首版采用同步停顿式重建：整个过程由 owner goroutine 执行，期间不处理 Add、Remove、Get 或 Tick。

推荐流程：

1. MatchService 把更新作为一条命令交给唯一 owner goroutine。
2. owner goroutine 完整解码、校验和编译配置；失败时保留活动 generation。
3. 若 Requirements 改变，owner goroutine 从当前 TicketStore 同步构建新的 indexes；重建期间不执行其他节点命令。
4. 校验新 generation 的 Ticket 数、Active Bitmap 和各索引不变量。
5. 校验成功后，在同一命令边界整体替换 RuleRuntime 的 active generation。
6. 完成后才处理下一条 Add、Remove、Get 或 Tick 命令。

这里允许索引维护任务遍历基线 Ticket 来构建新索引，因为它不在候选查询路径上。任何查询都不能以该维护遍历作为缺失索引时的回退。

如果 Context 取消、资源上限触发或准备失败，则丢弃未发布 generation，活动 generation 不变。首版不使用后台 worker、shadow build、mutation sequence 或 delta log；重建延迟是 owner goroutine 的显式停顿成本。

## 11. Owner goroutine 命令边界整体切换

LogicalNode 的执行入口应固定当前代：

```go
// 设计草案
func (n *LogicalNode) TryMatchOnce(now int64) MatchResult {
    n.activatePreparedBeforeExecution()
    generation := n.activeGeneration

    facts := generation.factsForEvaluation(now)
    return n.executeOne(generation, facts, now)
}
```

关键不变量：

- `generation` 在整次本地匹配执行内固定。
- 同一次执行只使用一个 revision 和 fingerprint。
- `generation.Program.RequiredRequirements` 必须等于 generation 记录的绑定契约；本地 IndexGeneration 必须满足该绑定契约，Fact 契约版本也必须匹配。
- 未选中的 If 不访问索引。
- JSON、编译器和索引准备只在 owner goroutine 的 Apply 命令中运行。
- 发布只在两条节点命令之间通过普通指针赋值整体完成。

LogicalNode 不创建独立执行线程、后台事件循环或 worker。JSON 解析、编译、索引构建、活动状态切换和回收决定都由同一个 owner goroutine 顺序执行。外层不能把 Apply 与 Add、Remove 或 Tick 分派到不同 goroutine。

由于切换只发生在命令边界，旧 generation 在赋值完成后不会再被执行路径引用，可以立即释放；为了诊断和快速回滚，也可以保留有限数量的只读历史 generation，但必须设置内存上限。

## 12. 跨物理 Placement 的独立发布边界

每个 MatchService 只接收自己唯一 PhysicalNode 的配置；PhysicalNode 只准备和发布自己本地 LogicalNode 的 PlanGeneration：

```text
placement-1: decode -> compile -> prepare -> next selected PhysicalNode Tick activate
placement-2: decode -> compile -> prepare -> next selected PhysicalNode Tick activate
```

每个 Placement 独立准备和激活，Ticket 调用节点的 ClientRouteTable 不保存活动 PlanGeneration，也不协调不同 MatchService 的同步整体切换。

因此多个 PhysicalNode 上相同 RuleID 的 Placement 可以短暂运行不同 revision。若业务不能接受该窗口，部署者必须在 Router 之外安排更新顺序，例如先从 ClientRouteTable 禁用目标 Placement，分别完成本地更新后再重新启用。配置变更不会复制或迁移已有 Ticket；等待中的 Ticket 在所在 LogicalNode 下一次被 PhysicalNode Tick 选中时使用新的 active generation。

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
    Fingerprint Fingerprint
    Status      ApplyStatus
}

func (r *RuleRuntime) ApplyPrefilterConfigJSON(
    ctx context.Context,
    raw []byte,
) (ApplyResult, error)

func (r *RuleRuntime) PrefilterConfigStatus(
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

- `ApplyPrefilterConfigJSON`：测试、外部配置调用方和嵌入式调用的唯一核心入口。
- `FilePollingSource`：轮询本地文件的简单适配器。

文件发布方应使用“写临时文件、flush、原子 rename”协议。配置输入层需要在文件稳定后完整读取，再由 owner goroutine 顺序调用 Apply；遇到文件缺失、半写入或非法 JSON 时保留旧版本。文件读取只是输入方式，revision、指纹、编译和整体发布仍由 PlanManager 统一处理。

## 14. 错误模型

### 14.1 构建期错误

构建期错误包括：

- JSON 语法、重复 key、未知字段或未知 schemaVersion；
- 超过大小、深度、节点数或动态值数量限制；
- 未注册索引、Fact、表达式或谓词；
- Query 与索引类型不匹配；
- 过滤表达式循环、空组合节点、无锚点 Exclude；
- If 读取 candidate、group 或候选集合；
- Requirements 不可满足或影子索引构建失败。

这些错误均拒绝新 revision，并保留当前 active generation。

### 14.2 运行期错误

合法配置仍可能因单个 seed 数据不满足契约而发生运行期错误，例如：

- seed 缺少必需字段；
- 绑定值超过 MaxQueryValues；
- int64 算术溢出；
- 动态范围 Min 大于 Max；
- Tick FactProvider 或 SeedFactProvider 返回错误或超限值。

运行期错误必须携带 configId、revision、node、seed 和表达式路径，并按既定策略跳过该 seed 或终止本 Tick。首版建议隔离到 seed，同时提高错误指标；无论选择哪种策略，都不能把错误解释成 None、Universe 或扫描回退。

## 15. 可观测性和审计

至少暴露：

- active/pending/rejected revision 和 fingerprint；
- decode、validate、compile、同步 index rebuild、activate 各阶段耗时；
- 每个 LogicalNode / Placement 的索引构建进度、文档数、posting 数和估算内存；
- owner goroutine 因配置更新暂停处理命令的时长；
- no-op、stale、reject、activate 和 rollback 计数；
- 带 JSON Path 的最近拒绝原因；
- 每个 Tick 实际使用的 revision。

日志不能输出完整 Ticket 或任意动态字段值。审计记录至少保存 configId、revision、fingerprint、来源、接收时间、激活时间和结果。

## 16. 资源与安全限制

建议把以下限制放入进程启动配置，由服务拥有者控制，而不是允许规则 JSON 自行提高：

| 限制 | 建议首值 |
| --- | ---: |
| JSON 最大字节数 | 1 MiB |
| 过滤表达式最大深度 | 32 |
| 过滤表达式最大节点数 | 4096 |
| 单组合节点最大 children | 256 |
| StepInt64 最大步数 | 256 |
| 单配置最大索引数 | 128 |
| 单配置最大 Fact 数 | 128 |
| 单次 Apply 准备的 generation | 每个 configId 1 个 |

这些数值是实现初值，最终通过压测校准。JSON 中的 runtime 参数只能在服务端硬上限以内降低或选择值，不能突破硬上限。

编译和索引构建接受 Context 取消和超时。Apply 开始时先核对 configId 与 revision；由于 owner goroutine 不会并发处理第二条 Apply，不需要任务 token 或过期后台任务仲裁。

## 17. 实现分层和落地顺序

建议按以下包内职责拆分；实际文件名可随现有工程结构调整：

```text
prefilterconfig/
  dto.go               JSON DTO 和 tagged union 原始结构
  decode.go            重复 key、unknown field、大小与深度限制
  schema.go            schemaVersion 和结构校验
  normalize.go         默认值填充与规范化

prefilter/
  compiler.go           DTO -> typed 过滤表达式-> executable program
  requirements.go          Requirements 与兼容性比较
  fingerprint.go       规范化语义指纹

matchsystem/
  generation.go        immutable PlanGeneration
  plan_manager.go      revision、prepare、publish、status
  index_rebuild.go     owner goroutine 内同步重建和校验
  config_source.go     可选输入适配接口
```

落地顺序：

1. 固化当前代码内 Prefilter 的 typed 过滤表达式、编译器和 Requirements，不先引入热更新。
2. 增加 JSON DTO、严格解码和 DTO 到 typed 过滤表达式的转换；JSON 与代码构造必须复用同一个语义编译器。
3. 增加不可变 PlanGeneration，并让一次本地匹配执行显式持有一个 generation。
4. 实现 Plan-only 的 Apply、revision、fingerprint、状态和执行边界切换。
5. 实现 owner goroutine 内同步索引重建和索引契约更新。
6. 增加 FilePollingSource 或其他外部配置输入适配器。
7. 完成故障注入、命令顺序一致性和百万 Ticket 停顿时间压测后再开放生产热更新。

第 2 步不能另写一套 JSON 专用执行器；否则代码构造和 JSON 构造会出现不同语义。

## 18. 测试与验收

### 18.1 解码和编译

- 未知字段、重复 key、尾随 JSON、未知 type 和未知 schemaVersion 均被拒绝。
- 过滤表达式深度、节点数、字符串长度和数组长度限制生效。
- JSON 构造与等价 typed 过滤表达式产生相同 Requirements、Fingerprint 和候选 Bitmap。
- 每条 If 路径和每个 Exclude 都完成正向锚点验证。
- 未选中的 If 不读取索引或 Fact。

### 18.2 热更新一致性

- Plan-only 更新不重建索引。
- 一次本地匹配执行只能观察到旧版本或新版本，不能混用。
- 一个 Placement 更新失败不影响其他 Placement 的活动 generation。
- 每次本地匹配执行只观察旧代或新代；Router 不参与跨 Placement 版本门控。
- stale revision 不能覆盖新版本。
- 相同 fingerprint 是 no-op。
- 非法 JSON、编译失败和超时均不影响当前匹配。
- 回滚通过更高 revision 完成，并可追溯到目标 fingerprint。

### 18.3 索引重建

- 同步重建期间不执行 Add、Remove、Get 或 Tick。
- 重建失败或取消时保留完整旧 generation。
- 发布前逐项验证 Active Bitmap、Ticket 数和索引 posting 不变量。
- 初筛查询期间绝不读取基线 Ticket 作为缺失索引回退。

### 18.4 规模和故障注入

- 在 10 万、50 万和 100 万 Ticket 下记录编译、同步重建和切换耗时。
- 记录活动代与待发布代同时存在时的峰值内存。
- 注入 Context 取消、Fact 失败、索引构建失败、进程关闭和顺序快速 revision。
- 验证整个 Apply 在 owner goroutine 中执行，期间没有其他核心命令插入。

验收的最终不变量是：

```text
配置可热更新
+ 单次匹配执行内版本一致
+ 更新失败保留旧版本
+ 索引契约变化使用同步重建的新索引代
+ 查询路径零全池扫描回退
+ GroupEvaluator 继续负责最终正确性
```
