# 匹配系统整体设计框架

> 状态：目标架构框架，尚未全部在当前代码中实现。  
> 范围：只定义系统分层、核心对象和主流程；各层的算法、配置与接口细节由独立设计文档负责。

## 1. 设计目标

匹配系统作为匹配服务内部的通用能力，负责接收 Ticket、在隔离的匹配节点内寻找合法组，并以单组结果接入外部 RateLimiter（限速器）。核心不解释地区、模式、玩法版本等业务字段，也不内置具体匹配规则。

系统遵循以下边界：

- 一个 Ticket 在等待期间只归属于一个 `OwnerNode`，普通匹配不跨节点执行。
- 路由只决定 Ticket 的归属节点，不承担候选过滤和组合法性判断。
- 索引初筛只产生候选安全超集，最终合法性由评估层保证。
- 一次对外产出调用最多返回一个组，但内部可以连续尝试多个 seed。
- 同一轮的 seed 游标跨调用保留，后续调用不会重新从最高优先级 Ticket 开始。
- 组被确认后才从 TicketStore、Active 集合和各项索引中移除；未确认的组可以释放。

## 2. 总体结构

```text
MatchService（匹配服务编排）
  |
  +-- Add Ticket
  |     -> Router（路由层）
  |     -> OwnerNode（唯一归属节点）
  |     -> TicketStore + Active + Indexes
  |
  `-- NextOffer（单组产出）
        -> NodeScheduler / SeedCursor
        -> CandidatePlan + CandidateIndex（索引层）
        -> bounded Top-L + Ticket materialization
        -> GroupBuilder + GroupEvaluator（评估层）
        -> GroupOffer（组提案 / reservation）
        -> external RateLimiter
             |- Commit -> 移除组成员及其索引
             `- Abort  -> 释放组成员，后续轮次可再次参与
```

`MatchService` 是三层能力之外的运行编排边界。它管理节点调度、匹配轮次、单组产出和外部确认，但不把这些职责下沉到 Router、CandidatePlan 或 GroupEvaluator。

## 3. 三层核心能力

| 层 | 输入 | 核心职责 | 输出 |
| --- | --- | --- | --- |
| Router（路由层） | 新增 Ticket 与节点集合 | 选择唯一 `OwnerNode`，建立 Ticket 生命周期内的节点归属 | 目标 `OwnerNode` |
| Candidate Index（索引层） | seed、节点快照、编译后的 `CandidatePlan` | 执行树形索引查询和集合运算，限制候选规模并产生安全超集 | 有界候选集合 |
| Evaluation（评估层） | seed 与已物化候选 | 挑选候选、构建 group，并执行 Join、Start、ForceStart 等最终合法性判断 | 合法 group 或无结果 |

索引层的详细设计见：[CandidatePlan 树形索引初筛层设计](./candidate-index-filtering.md)。

## 4. Ticket 生命周期

```text
创建 Ticket
  -> Router 选择唯一 OwnerNode
  -> 节点保存 Ticket 并写入 Active 与索引
  -> SeedScheduler 将 Ticket 纳入匹配轮次
  -> CandidatePlan 按 seed 生成候选安全超集
  -> Evaluation 构建并确认合法组
  -> 预留组并产出一个 GroupOffer
  -> 外部限速器决策
       |- Commit：匹配成立，删除成员、归属关系和索引数据
       `- Abort：匹配未成立，解除预留，不回退当前 seed 游标
```

Ticket 在 `Commit` 前仍属于等待池，只是处于 reserved（已预留）状态，不得被其他组重复使用。`Abort` 不改变 Ticket 的创建时间和等待时间，也不立即重新尝试同一个 seed。

## 5. 匹配轮次与单组产出

一个匹配轮次固定本轮的 seed 顺序并保存游标。每次单组产出调用从当前游标继续，依次尝试 seed，直到发生以下任一结果：

- 找到一个合法组并返回 `GroupOffer`；
- 本次 seed 尝试预算耗尽；
- 当前轮 seed 已全部尝试；
- 当前等待池为空。

找到组后立即结束本次调用，因此外部限速器始终面对单组结果。调用方完成 `Commit` 或 `Abort` 后，下一次调用从游标后续位置继续。新 Ticket 不插入游标之前；已删除的 stale seed 在读取时直接跳过。

多节点场景由 `NodeScheduler` 保存节点游标，并在各 `OwnerNode` 之间轮转；每个节点独立保存自己的 seed 轮次和索引状态。

## 6. 核心对象关系

| 对象 | 框架职责 |
| --- | --- |
| `MatchService` | 对外接收 Ticket、驱动单组产出，并衔接限速器和提交结果 |
| `Router` | 为 Ticket 返回唯一节点归属 |
| `OwnerNode` | 持有本节点 TicketStore、Active、索引、计划与匹配轮次状态 |
| `CandidatePlan` | 描述树形初筛的执行结构 |
| `CandidateIndex` | 维护 posting，并根据 `IndexQuery` 产生 Bitmap（位图）结果 |
| `SeedScheduler` / `SeedCursor` | 生成一轮 seed 顺序并跨调用保存扫描位置 |
| `GroupBuilder` / `GroupEvaluator` | 构建 group 并完成最终正确性判断 |
| `GroupOffer` | 表示已经构建但等待外部确认的单组结果 |

## 7. 文档导航

- [CandidatePlan 树形索引初筛层设计](./candidate-index-filtering.md)：索引层的 AST、Query、Index、编译验证、Bitmap 执行、动态范围、Top-L 和测试验收。

## 8. 当前实现边界

本文描述目标架构，不表示当前仓库已经实现全部组件。当前代码仍以单个 `MatchPool`、线性 `CandidateFilter`、`RuleSet` 和 Greedy（贪心）建组为主；Router、树形 `CandidatePlan`、Bitmap 索引执行、持久化 seed 轮次以及 `GroupOffer` 的 Commit/Abort 流程均属于后续实现范围。
