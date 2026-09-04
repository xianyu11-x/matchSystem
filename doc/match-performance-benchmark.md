# 匹配池规模性能基准

`cmd/match-benchmark` 用现有 `LogicalNode` 运行一轮真实匹配，探索等待池为
1,000 到 100,000 人时的开销。它不执行 UE 编译；该仓库的匹配核心是 Go 模块，
基准可独立运行。

## 规则和数据

基准使用五个业务属性：

- `ticketId`：每个 Ticket 的 `TicketID` 镜像，作为 uint64 属性建立索引；
- `whitelist`：seed 允许的 TicketID 列表，包含 ID 1–10；
- `blacklist`：seed 排除的 TicketID 列表，包含 ID 11–40；
- `level`：1–40 的确定性伪随机整数；
- `score`：1–500 的确定性伪随机整数。

白名单和黑名单现在通过 seed 属性中的真实 TicketID 列表查询 `ticketId` 索引，
不再使用所有命中项相同的 `yes` 标记。Prefilter 表达式为：

```text
(candidate.ticketId ∈ seed.whitelist OR
 (seed.level - 5 <= level <= seed.level + 5 AND
 seed.score - 50 <= score <= seed.score + 50))
AND NOT(candidate.ticketId ∈ seed.blacklist)
```

`exclude` 位于最外层，因此本规则语义是黑名单优先；本基准的白名单 ID 1–10 与
黑名单 ID 11–40 不重叠，所以不会触发名单冲突。`ticketId` 是规则 contract 中
声明的 uint64 multi-value 属性（单值），Ticket 结构体上的 `TicketID` 元数据仍
保留并用于结果校验。

`canJoin` 恒为 true，`party-size >= 30` 时完成 Match，`maxPlayers=30`，评分
函数为恒定 1。候选上限设为当前池规模，使白名单候选不会因候选上限被截断；每个
样本执行一次 `BeginMatchRound` 后连续 10 次 `ProduceMatch`，当前 workload 每次均
提交一个 30 人 Match（每个样本共 10 个 Match），并检查黑名单未进入、10 个白名单
均进入、提交后池中剩余规模为 `N-300`。

## 测量方式

```powershell
go run ./cmd/match-benchmark -samples=20 -warmups=3
```

每个规模先生成并填充一个新 `LogicalNode`。填充时间单独列为 `setup`，不计入一次
匹配热路径。计时包括：

- `round`：`BeginMatchRound`，只重置通用 round 状态并启动 seed runtime stream；
- `produce`：`ProduceMatch`，包含 Prefilter、候选排序、CanJoin/CanComplete 和
  30 人提交；
- `total`：`round + produce`，作为完整的一次匹配尝试。

表格中的时间为每个规模 20 次测量的 p50/p95，单位为毫秒；前 3 个 warmup 样本丢弃。
`heap` 是填充完成并 GC 后的 Go live heap（包含基准输入和节点索引），用于观察
规模增长，不是精确 RSS。

为避免 Windows 单次阶段时钟量化，另有包内 Prefilter 微基准。它用固定随机种子
`100000*7919+17` 生成 100,000 个 Ticket，setup、索引建立和 `BeginTick` 均在计时
外；循环只计时 `CandidatesWithStats` 加 `Remove(seed)`，并打开 `ReportAllocs`：

```powershell
$env:GOMAXPROCS='1'
$env:GOGC='off'
go test ./internal/matchsystem/prefilter -run '^$' -bench '^BenchmarkPrefilterCandidates100k$' -benchtime=1s -benchmem -count=5
```

两套 workload 都校验为 5,464 个非 seed 候选，且每次 Stats 固定为
`Lookup=4, Contains=0, And=1, Or=2, Subtract=1`：

| workload | 5 轮 ns/op 范围 | p50 ns/op | B/op | allocs/op |
|---|---:|---:|---:|---:|
| 当前 `ticketId` uint64 列表（int64 range block 后） | 232,758–284,052 | 249,041 | 175,608 | 134 |
| 同一 workload（block bitmap 前基线） | 407,727–541,278 | 467,071 | 160,760 | 134 |
| 旧版 `yes` string 标记（同一 block 实现） | 222,176–224,438 | 223,709 | 174,904 | 119 |

在相同 100,000 Ticket workload 下，range block 将当前 ID 列表 Prefilter p50 从
467,071 ns 降至 249,041 ns，约降低 46.7%；allocs/op 不变，B/op 增加 14,848
（约 9.2%），对应查询结果 bitmap 的分配形态变化。额外的持久 block bitmap 内存已
包含在下方 live heap 测量中。旧版 `yes` 标记只作为回归对照，不是当前规则路径。

int64 range index 对排序后的 distinct value 按固定大小的 block 维护聚合 roaring
bitmap：首尾不完整 block 逐 posting 合并，中间 block 直接 Or 聚合 bitmap；estimate
对中间 block 使用聚合 cardinality。Add/Remove 会同步现有 block，distinct value
变更在 Tick barrier 的 Prepare 中重建排序目录和 block。查询返回新 bitmap，不把内部
posting 或 block 的所有权暴露给调用者。

块大小用固定 100,000 Ticket、500 个 distinct score value、5 次 `benchmem` 重复比较：

```powershell
$env:GOMAXPROCS='1'
$env:GOGC='off'
go test ./internal/matchsystem/prefilter -run '^$' -bench 'BenchmarkInt64RangeBlockSizes100k' -benchtime=1s -benchmem -count=5
```

下表是各 5 次结果的 p50；`score-medium`（200–300）接近实际规则的中等范围，
因此选择 16 作为默认 block size，在 lookup 和 estimate 之间取得平衡：

| block 内 distinct value 数 | score-medium lookup ns/op | score-medium estimate ns/op | score-wide lookup ns/op |
|---:|---:|---:|---:|
| 8 | 69,107 | 102.2 | 128,806 |
| 16（当前） | 65,436 | 224.3 | 126,301 |
| 32 | 89,606 | 307.0 | 126,301 |

该微基准只比较 block lookup/estimate，不包含 FastOr、scratch pool 或 result cache；
16 的选择对大范围查询改善最明显，窄范围的绝对差异处于纳秒级测量噪声内。
`blockSize=16` 只是当前 100,000 Ticket、500 个 distinct score value workload 下的选择，
不是所有数据分布或查询宽度的普适最优值。索引会复用部分目录/bitmap backing，churn
后可能保留历史高水位容量；这是用空间换查询时间的当前限制。

Object slot 的冷/稳态微基准使用 `go test -benchmem`：

```powershell
go test ./internal/matchsystem/fact -run '^$' -bench 'ObjectSlot' -benchmem -count=5
```

`ColdRefresh` 包含新 slot 初始化和首次 Writer 写入；`SteadyCacheHit` 测量同一
generation 的缓存命中；`SteadyRefreshReuse` 测量 generation 改变后的原地刷新和
已分配 list buffer 复用。后两者用于确认稳定路径无每次操作分配，不能与完整池 setup
时间混为一谈。

本次机器上连续 5 轮 `-benchmem` 的范围为：`ColdRefresh` 191.4–210.7 ns/op、568 B/op、
5 allocs/op；`SteadyCacheHit` 8.519–12.74 ns/op、0 B/op、0 allocs/op；
`SteadyRefreshReuse` 52.37–58.41 ns/op、0 B/op、0 allocs/op。冷路径的分配是 slot 首次
建立/首次写入成本，稳态缓存命中和跨 generation 的已分配 buffer 复用均无每次操作分配。

Seed runtime 的流式消费另有 100,000 Ticket、`limit=1` 微基准，用于确认 round 不再
扫描或物化整个等待池：

```powershell
go test ./internal/matchsystem -run '^$' -bench 'BenchmarkSeedOrderRuntimeNextRound100kLimit1' -benchtime=1s -benchmem -count=3
```

本次实现对应的微基准测量的是 `BeginRound(1)+Next()`，不再分配返回 slice；结果应
以运行命令输出为准。四种策略的 stream 都只移动一个 TicketID，`oldest`/priority
把 entry 暂存到有界 held，`random` 从 dense active 数组无放回移入 held；下一轮
只恢复仍 active 的 entry。

## ProduceMatch 阶段打点

基准调用显式的 `LogicalNode.ProduceMatchWithMetrics`，它返回一次调用的聚合
`ProduceMatchMetrics`；正常生产调用仍使用 `ProduceMatch`，默认不读时钟、不创建
metrics 快照，也不输出逐玩家日志。阶段包括：

- `SeedPreparation`：本轮 seed 预留和 cursor 推进；`SessionPreparation`：Tick
  Fact、Fact Frame 和 Prefilter TickSession 初始化；`AttemptPreparation`：seed
  Object Fact slot 首次刷新与 Match Fact Initialize。
- `Prefilter`：索引查询、bitmap 组合和 seed 移除；同时返回 lookup/contains/AND/
  OR/AND-NOT 计数。
- `CandidateRanking`：候选 Object Fact 访问、评分、bounded Top-L selection 和最终排序；
  当 effective candidate limit >= scoring limit 时使用 append+sort；否则使用 bounded
  heap 保留 Top-L。
  `CandidateMaterialization`、`CandidateScoring`、`CandidateSort` 是其中的细分阶段，
  `ObjectFactRefresh`/`ObjectFactProvider` 是物化中的 slot/provider 子测量，均不是
  与 `CandidateRanking` 相加的独立阶段。
- `CanJoin`、`MatchFactUpdate`、`CanComplete`：分别聚合谓词、OnJoin Fact 更新和
  完成判定调用耗时；`MatchBuild` 与 `Commit` 覆盖结果构造和 ticketStore 提交。

阶段表输出每个阶段的 p50/p95 时延，以及按每个样本计算的阶段占该样本
`ProduceMatch` 时延的 p50/p95 百分比；另有聚合调用次数表，包含 Object Fact 的
refresh/provider/cache-hit/capacity-growth/error 计数，避免用高基数玩家 ID 作为日志
或指标标签。`CandidateRanking` 包含其细分阶段，所以阶段占比不能简单求和。

## 实际运行结果

运行命令：`go run ./cmd/match-benchmark -sizes=10000,20000,50000,100000 -samples=20 -warmups=3 -produces-per-round=10 -attempt-limit-per-round=500`

运行日期：2026-09-02（Asia/Shanghai）。下表来自当前工作区一次完整运行的
20 个测量样本（前 3 个 warmup 已丢弃）；Windows 计时器粒度会令部分短阶段显示为 0。

环境：Windows 11 专业版 x64（build 26100），Intel Core i7-13700（16 核/24 线程），
64 GiB 内存；Go `go1.24.6 windows/amd64`，`GOMAXPROCS=24`。这是单进程、单次串行
匹配测量，不包含网络、序列化或并发竞争。

| 池规模 | Prefilter 候选数 | 成功 Match | 剩余 | setup p50/p95 ms | round p50/p95 ms | 10 次 produce 总和 p50/p95 ms | round+10 produces p50/p95 ms | live heap MiB |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 10,000 | 542 | 10 | 9,700 | 34.755/38.567 | 0.000/0.000 | 6.026/7.570 | 6.026/7.570 | 26.9 |
| 20,000 | 788 | 10 | 19,700 | 69.442/88.707 | 0.000/0.000 | 8.547/10.545 | 8.547/10.545 | 53.3 |
| 50,000 | 2,826 | 10 | 49,700 | 235.062/253.378 | 0.000/0.000 | 12.389/13.346 | 12.389/13.346 | 129.9 |
| 100,000 | 5,465 | 10 | 99,700 | 507.019/522.237 | 0.000/0.000 | 17.158/20.082 | 17.158/20.082 | 259.4 |

表格中的 `Prefilter 候选数` 是本次 seed 的候选总数，包含 1 个 seed，因此 100,000
行的 5,465 对应阶段统计中的 5,464 个非 seed 候选加 seed 本身；阶段表和聚合表明确
按非 seed 候选统计时使用 5,464。

100,000 人池连续 10 次 ProduceMatch 的阶段明细如下；时延和占比均为每个样本中
10 次调用的聚合 p50/p95。`CandidateRanking` 已包含其下的物化、Object Fact
refresh/provider、评分和排序，不能与这些子项相加：

| 阶段 | 时延（ms；<1 ms 显示 µs） | 占 ProduceMatch |
|---|---:|---:|
| SeedPreparation | 0.000/0.000 | 0.0%/0.0% |
| SessionPreparation | 0.000/0.000 | 0.0%/0.0% |
| AttemptPreparation | 0.000/0.000 | 0.0%/0.0% |
| Prefilter | 6.404/9.562 | 35.2%/49.4% |
| CandidateRanking（含子项） | 5.772/7.434 | 31.9%/48.2% |
| CandidateMaterialization | 0.998/2.669 | 5.0%/13.8% |
| ObjectFactRefresh（物化子项） | 0.000/0.000 | 0.0%/0.0% |
| ObjectFactProvider（物化子项） | 0.000/0.000 | 0.0%/0.0% |
| CandidateScoring | 0.000/0.526 | 0.0%/3.2% |
| CandidateSort | 0.521/1.337 | 2.7%/7.9% |
| CanJoin | 2.007/3.530 | 12.3%/20.0% |
| MatchFactUpdate | 1.504/3.689 | 7.5%/20.6% |
| CanComplete | 0.000/0.999 | 0.0%/5.3% |
| MatchBuild | 0.000/0.000 | 0.0%/0.0% |
| Commit | 1.001/2.808 | 5.8%/15.9% |

连续 10 次 ProduceMatch 的 CandidateMaterialization 共有 5,290 次调用（5,000 次候选
访问加 290 次 CanJoin 重访）；本规则没有 Object Facts，Ticket 的 slot 为 nil，
因此 `Frame.Object` 直接返回空 borrowed Facts，不产生 Object refresh/provider/cache/
growth/error 事件。

对应的 10 次调用聚合计数为：10 个 seed，10 次 Prefilter，52,074 个非 seed Prefilter
候选，5,000 次候选访问，5,290 次候选 ObjectFact seam 访问，obj-refresh/provider/
cache/growth/errors 全为 0，5,000 次评分，290 次 CanJoin，290 次 Match Fact 更新，
300 次 CanComplete，以及 10 次 Commit。

结论：在该环境下，TicketID 黑白名单索引版本的 100,000 人池一轮连续 10 次
ProduceMatch 的 produce 总和 p50/p95 为 `17.158/20.082 ms`；每个样本成功 10 次、
失败 0 次、耗尽 0 次，消费 10 个有效 seed，剩余 99,700 个 Ticket。`BeginMatchRound`
只重置各 LogicalNode 的通用 round 状态并启动策略 stream，不再分配完整 TicketID
snapshot，因此四档 round 均低于 Windows 阶段计时粒度。`setup` 是将池填充进索引的
成本，100,000 人约 `504.747/537.711 ms`，不属于一次已入池匹配热路径。本数据分布下
动态等级/分数范围命中的候选共约 5,465 人（含 seed；非 seed 候选约 5,464 人），因此
produce 主要随 Prefilter 候选数和每轮 10 次调用增长。

### 同一 MatchRound 连续 10 次 ProduceMatch

为观察一轮内的连续调用，使用：

```powershell
go run ./cmd/match-benchmark -sizes=100000 -samples=20 -warmups=3 -produces-per-round=10 -attempt-limit-per-round=500
```

每个样本只调用一次 `BeginMatchRound`，然后固定调用 10 次
`ProduceMatchWithMetrics`。规则配置为 `attemptLimitPerProduceMatch=1`、
`attemptLimitPerMatchRound=500`：前者限制每次调用，后者是整轮最多消费的有效 seed
数；`-produces-per-round` 只控制调用次数，两者独立。runtime 在 Begin 时只重置策略
自己的 cursor/heap-held/dense-held 状态，不复制池内 TicketID；每次 Produce 按策略
流式取得下一个 TicketID。某个 seed 被 Commit/Remove 后，后续调用仍可取得后面的
seed；已经返回的 seed 不会在同一轮重复，失败 seed 下一轮可恢复。主表中的 `produce`
是 10 次调用时长之和，`total` 是一次 `BeginMatchRound` 加这 10 次调用。

本次运行（2026-09-02，前 3 个 warmup 丢弃）的完整汇总为：

| 池规模 | round p50/p95 ms | 10 次 produce 总和 p50/p95 ms | round+10 produces p50/p95 ms | 成功 | 失败 | 耗尽 | 消费 seed | 剩余 | live heap MiB |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 10,000 | 0.000/0.000 | 6.026/7.570 | 6.026/7.570 | 10/10 | 0/0 | 0/0 | 10/10 | 9,700/9,700 | 26.9 |
| 20,000 | 0.000/0.000 | 8.547/10.545 | 8.547/10.545 | 10/10 | 0/0 | 0/0 | 10/10 | 19,700/19,700 | 53.3 |
| 50,000 | 0.000/0.000 | 12.389/13.346 | 12.389/13.346 | 10/10 | 0/0 | 0/0 | 10/10 | 49,700/49,700 | 129.9 |
| 100,000 | 0.000/0.000 | 17.158/20.082 | 17.158/20.082 | 10/10 | 0/0 | 0/0 | 10/10 | 99,700/99,700 | 259.4 |

100,000 人池 10 次调用的逐次 p50/p95（ms）为：第 1 次 `1.998/2.508`、第 2 次
`1.645/2.480`、第 3 次 `1.842/2.615`、第 4 次 `1.509/1.892`、第 5 次
`2.000/2.833`、第 6 次 `2.000/2.516`、第 7 次 `1.590/3.001`、第 8 次
`2.003/2.511`、第 9 次 `1.993/2.506`、第 10 次 `1.543/2.503`；累计到第 10 次为
`17.158/20.082`。每个样本的结果计数为成功 10、失败 0、耗尽 0、消费有效 seed 10、
剩余 99,700。

同一命令扩展到四档池规模后，round p50/p95 均为 `0.000/0.000 ms`（Windows
阶段计时粒度下不可分辨），而且不随池规模增长；这符合 Begin 只重置策略运行态的
设计。`attemptLimitPerMatchRound=500` 与池规模无关，只要本轮实际消费不超过 500
个有效 seed 即可。这里即使把它设为 10，流式 runtime 也会在前序 seed 被 Commit/Remove
后继续取得后面的有效 seed；在当前 workload 下固定 10 次 Produce 仍可成功 10 次。
10 表示整轮最多消费的有效 seed 数，不是 Produce 调用次数；只有失败或失效 seed 消费完
这 10 次有效 attempt 后，后续调用才会耗尽。基准使用 500 是为其他失败场景保留余量，
并不依赖池规模。

在当前实现中，arrival 用带 TicketID 索引的 list 和 cursor，oldest/priority 用可定位
heap entry 并把本轮已返回 entry 放到 held，random 用 dense 数组 swap-remove 并把已
返回 ID 放到 held。上述结构均不在 Begin 按池规模复制 slice；`nextSeed` 仍对每个
TicketID 做一次 store lookup 作为失效防御，但 live seed 才计入 round budget。

内存复核显示，benchmark 规则没有 Object-scoped Fact 时不会为每个 Ticket 创建
`ObjectSlot` 或其 list buffer；`storedTicket` 只保留 nil 的可选 slot 指针。本次完整
运行的 100,000 人 live heap 为 259.4 MiB。ObjectSlot 冷/稳态微基准和 heap profile
属于单独的历史测量，不能从本次 round/produce 表直接推导；当前规则包含每 Ticket
唯一 `ticketId` 的 Prefilter 索引，Ticket 所有权复制仍是主要内存来源。

阶段数据来自启用 `ProduceMatchWithMetrics` 的诊断路径，包含时钟采样本身的微小
开销，不应与默认关闭 metrics 的生产路径作严格的绝对值对比。Windows 上亚毫秒
阶段受系统计时粒度影响，显示为 0.000 并不表示调用次数为零；应结合上面的调用
次数和整体 `produce` 时延解读。

上述时间是本机一次运行的参考值；CPU 频率、后台负载、Go GC 和样本数都会影响结果，
不应当视为跨机器 SLA。`CandidateScoring` 现在使用 borrowed Ticket/Facts，不再为每个
候选 clone；Object slot 的稳定 refresh/cache 路径由下面的 microbenchmark 单独验证。

### 8 人 Match：同一 MatchRound 连续 20 次 ProduceMatch（新增场景）

本节是独立的 8 人 Match workload，新增数据不替换上面的默认 30 人 Match 历史数据。命令显式指定 `-match-size=8`；它同时配置 `canComplete` 的 `party-size` 阈值和 runtime 的 `maxPlayers`。首个 seed 仍带有 whitelist `TicketID 1-10` 与 blacklist `TicketID 11-40`，8 人场景只要求能放入的 whitelist 前缀 `1-8`，并继续校验 blacklist 不得进入 Match。

```powershell
go run ./cmd/match-benchmark -sizes=10000,20000,50000,100000 -samples=20 -warmups=3 -produces-per-round=20 -attempt-limit-per-round=500 -match-size=8
```

每个样本只执行一次 `BeginMatchRound`，随后连续执行 20 次 `ProduceMatch`。`-attempt-limit-per-round=500` 是本轮最多消费的有效 seed 数，和 20 次调用次数、8 人 Match 大小彼此独立；本 workload 每次调用都成功，故每个样本提交 20 个 Match、消费 20 个 seed，剩余为 `N-160`。这是在同一 round 流式 runtime 下验证 stale/Commit 后仍能继续取后续 seed 的场景。

运行日期为 2026-09-03（Asia/Shanghai），Windows amd64、Go `go1.24.6`，20 个测量样本（前 3 个 warmup 丢弃）：

| 池规模 | Prefilter 候选数 | Match 大小 | 剩余 | setup p50/p95 ms | round p50/p95 ms | 20 次 produce 总和 p50/p95 ms | round+20 produces p50/p95 ms | live heap MiB |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 10,000 | 542 | 8 | 9,840 | 31.884/36.670 | 0.000/0.000 | 4.357/7.010 | 4.357/7.010 | 26.9 |
| 20,000 | 788 | 8 | 19,840 | 64.859/91.545 | 0.000/0.000 | 7.043/10.124 | 7.043/10.124 | 53.4 |
| 50,000 | 2,826 | 8 | 49,840 | 202.019/236.067 | 0.000/0.000 | 9.108/10.510 | 9.108/10.510 | 129.9 |
| 100,000 | 5,465 | 8 | 99,840 | 389.555/412.449 | 0.000/0.000 | 11.767/15.287 | 11.767/15.287 | 259.4 |

20 次调用序列在四个池规模上均为 `success=20, failed=0, exhausted=0, consumed seeds=20`；因此没有因前面 Match 提交而提前耗尽 round stream。100,000 池 20 次调用累计 produce p50/p95 为 `11.767/15.287 ms`，每个样本消费 20 个有效 seed，剩余 99,840 个 Ticket。

100,000 池的 20 次调用聚合阶段（p50/p95 ms）中，Prefilter 为 `5.753/7.502`，
CandidateRanking 为 `2.007/3.993`；其余阶段以本次命令的阶段输出为准。聚合 counters
为 `seeds=20/20`、`prefilter-candidates=101,593/101,593`、`candidate-visited=10,000/10,000`、
`materialized=10,140/10,140`、`scored=10,000/10,000`、`canJoin=140/140`、`joined=140/140`、
`fact-updates=160/160`、`canComplete=160/160`、`commits=20/20`。

`round` 在 Windows 的阶段计时粒度下显示为 `0.000 ms`，不表示没有执行；本场景的完整 round+produce 成本由 `total` 表示。setup 与 live heap 仍随池规模增长，因为它们包含 ticketStore、位图及规则索引填充，不属于 Begin/Produce 热路径。

### 8 人 Match：L=50/S=500 独立 bounded heap 场景

本节是独立的候选保留上限场景，不覆盖上面的默认 L>=S 数据。`-candidate-limit=50`
将每个 seed 的 Top-L 保留上限设为 50，`-candidate-scoring-limit=500` 仍允许最多评分
500 个候选；由于 `50 < 500`，CandidateRanking 使用 bounded heap。header 会打印
`ranking=candidateLimitPerSeed=50 candidateScoringLimitPerSeed=500` 作为分支配置证据。

```powershell
go run ./cmd/match-benchmark -sizes=10000,20000,50000,100000 -samples=20 -warmups=3 -produces-per-round=20 -attempt-limit-per-round=500 -match-size=8 -candidate-limit=50 -candidate-scoring-limit=500
```

每个样本仍只执行一次 BeginMatchRound 和 20 次 ProduceMatch；四档均为
`success=20, failed=0, exhausted=0, consumed seeds=20`，剩余分别为 9,840、19,840、
49,840、99,840。

| 池规模 | Prefilter 候选数 | Match 大小 | 剩余 | setup p50/p95 ms | round p50/p95 ms | 20 次 produce 总和 p50/p95 ms | round+20 produces p50/p95 ms | live heap MiB |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 10,000 | 542 | 8 | 9,840 | 30.323/32.785 | 0.000/0.000 | 4.220/5.701 | 4.220/5.701 | 26.9 |
| 20,000 | 788 | 8 | 19,840 | 62.096/68.587 | 0.000/0.000 | 6.847/8.528 | 6.847/8.528 | 53.4 |
| 50,000 | 2,826 | 8 | 49,840 | 206.947/224.390 | 0.000/0.000 | 7.522/9.825 | 7.522/9.825 | 129.9 |
| 100,000 | 5,465 | 8 | 99,840 | 431.713/570.638 | 0.000/0.000 | 14.714/18.520 | 14.714/18.520 | 259.4 |

100,000 池 20 次 ProduceMatch 的阶段聚合（p50/p95 ms）为：SeedPreparation
`0.000/0.000`、SessionPreparation `2.906/3.146`、AttemptPreparation `0.000/0.518`、
Prefilter `7.384/8.533`、CandidateRanking `2.998/5.131`、CandidateMaterialization
`1.000/2.002`、CandidateScoring `0.000/1.000`、CandidateSort `0.000/1.000`、
CanJoin `1.004/2.515`、MatchFactUpdate `0.000/2.004`、CanComplete `0.000/1.002`、
MatchBuild `0.000/0.000`、Commit `0.000/1.515`。聚合 counters 为
`seeds=20/20`、`prefilter-candidates=101,593/101,593`、`candidate-visited=10,000/10,000`、
`materialized=10,140/10,140`、`scored=10,000/10,000`、`canJoin=140/140`、
`joined=140/140`、`fact-updates=160/160`、`canComplete=160/160`、`commits=20/20`。

CandidateRanking 独立 bench 使用 5 次 `benchmem`、`GOMAXPROCS=1,GOGC=off`：

```powershell
$env:GOMAXPROCS='1'
$env:GOGC='off'
go test ./internal/matchsystem -run '^$' -bench '^BenchmarkCandidateRanking(BoundedHeap|HeapCapacity)$' -benchmem -benchtime=1s -count=5
```

| ranking 配置 | 选择路径 | p50 ns/op | B/op | allocs/op |
|---|---|---:|---:|---:|
| L=50/S=500 | bounded heap | 78,695 | 1,440 | 56 |
| L=100,000/S=500（L>=S 对照） | append+sort | 41,465 | 4,320 | 6 |

L=50/S=500 的 Top-L heap 减少了保留结果的 backing 大小，但 heap 操作本身带来更多
分配和排序选择开销；完整场景的 100,000 池 produce p50/p95 为 `14.714/18.520 ms`，
高于同批 L>=S 场景的 `11.767/15.287 ms`。两组完整 benchmark 均受 Windows 阶段
计时粒度和运行负载影响，适合比较路径趋势，不作为跨机器 SLA。

## 代码验证

```powershell
gofmt -w internal/matchsystem/seed_order.go internal/matchsystem/seed_order_test.go internal/matchsystem/logical_node_round_test.go
go test ./internal/matchsystem/... -count=1
go test ./... -count=1
go test ./internal/matchsystem -run '^$' -bench 'BenchmarkSeedOrderRuntimeNextRound100kLimit1' -benchtime=1s -benchmem -count=3
go run ./cmd/match-benchmark -sizes=10000,20000,50000,100000 -samples=20 -warmups=3 -produces-per-round=10 -attempt-limit-per-round=500
go run ./cmd/match-benchmark -sizes=10000,20000,50000,100000 -samples=20 -warmups=3 -produces-per-round=20 -attempt-limit-per-round=500 -match-size=8
git diff --check
```
