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
规模均实际提交一个 30 人 Match，并检查黑名单未进入、10 个白名单均进入、提交后
池中剩余规模为 `N-30`。

## 测量方式

```powershell
go run ./cmd/match-benchmark -samples=20 -warmups=3
```

每个规模先生成并填充一个新 `LogicalNode`。填充时间单独列为 `setup`，不计入一次
匹配热路径。计时包括：

- `round`：`BeginMatchRound`，包含 arrival seed snapshot 构建；
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
go test ./internal/matchsystem/prefilter -run '^$' -bench 'BenchmarkPrefilterCandidates100k' -benchtime=3s -benchmem -count=10
```

两套 workload 都校验为 5,464 个非 seed 候选，且每次 Stats 固定为
`Lookup=4, Contains=0, And=1, Or=2, Subtract=1`：

| workload | 10 轮 ns/op 范围 | 10 轮 p50 ns/op | B/op | allocs/op |
|---|---:|---:|---:|---:|
| 当前 `ticketId` uint64 列表 | 453,351–521,536 | 470,109 | 160,760 | 134 |
| 旧版 `yes` string 标记 | 449,410–473,026 | 461,689 | 160,056 | 119 |

当前规则相对旧版的批量 Prefilter p50 增加 8,420 ns（约 1.82%），每次增加 704 B
和 15 次分配。两者差距接近 2%，说明当前 ID 列表查询的额外成本较小；单次
`time.Now` 阶段表中的 1 ms 主要受 Windows 亚毫秒计时粒度量化影响，不能直接解释
成同等幅度的性能回归。

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

Seed runtime 的有界构建另有 100,000 Ticket、`limit=1` 微基准，用于确认 round 不再
扫描或物化整个等待池：

```powershell
go test ./internal/matchsystem -run '^$' -bench 'BenchmarkSeedOrderRuntimeBuildRound100kLimit1' -benchtime=1s -benchmem -count=3
```

本机最后 3 轮结果的中位数约为：`arrival` 15.6 ns/op、`oldest` 246.5 ns/op、
`int64_priority` 270.1 ns/op、`random` 76.6 ns/op；四者均为 8 B/op、1 alloc/op
（仅为返回一个 TicketID slice）。`arrival` 的成本与池规模无关，`oldest`/priority
只弹出并恢复 Top-N，`random` 只做 limit 长度的部分洗牌。

## ProduceMatch 阶段打点

基准调用显式的 `LogicalNode.ProduceMatchWithMetrics`，它返回一次调用的聚合
`ProduceMatchMetrics`；正常生产调用仍使用 `ProduceMatch`，默认不读时钟、不创建
metrics 快照，也不输出逐玩家日志。阶段包括：

- `SeedPreparation`：本轮 seed 预留和 cursor 推进；`SessionPreparation`：Tick
  Fact、Fact Frame 和 Prefilter TickSession 初始化；`AttemptPreparation`：seed
  Object Fact slot 首次刷新与 Match Fact Initialize。
- `Prefilter`：索引查询、bitmap 组合和 seed 移除；同时返回 lookup/contains/AND/
  OR/AND-NOT 计数。
- `CandidateRanking`：候选 Object Fact 访问、评分、bounded Top-L heap 和最终排序；
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

运行命令：`go run ./cmd/match-benchmark -samples=20 -warmups=3`

运行日期：2026-09-02（Asia/Shanghai）。下表来自当前工作区一次完整运行的
20 个测量样本（前 3 个 warmup 已丢弃）；Windows 计时器粒度会令部分短阶段显示为 0。

环境：Windows 11 专业版 x64（build 26100），Intel Core i7-13700（16 核/24 线程），
64 GiB 内存；Go `go1.24.6 windows/amd64`，`GOMAXPROCS=24`。这是单进程、单次串行
匹配测量，不包含网络、序列化或并发竞争。

| 池规模 | Prefilter 候选数 | Match | setup p50/p95 ms | round p50/p95 ms | produce p50/p95 ms | 完整一次 total p50/p95 ms | live heap MiB |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1,000 | 55 | 30 | 2.631/3.506 | 0.000/0.000 | 0.000/0.997 | 0.000/0.997 | 3.2 |
| 5,000 | 270 | 30 | 12.535/13.060 | 0.000/0.000 | 1.000/1.518 | 1.000/1.518 | 13.8 |
| 10,000 | 542 | 30 | 24.849/26.902 | 0.000/0.000 | 0.978/1.232 | 0.978/1.232 | 27.1 |
| 25,000 | 1,422 | 30 | 59.494/61.562 | 0.000/0.000 | 1.000/1.512 | 1.000/1.512 | 65.6 |
| 50,000 | 2,826 | 30 | 129.138/136.828 | 0.000/0.000 | 1.001/1.198 | 1.001/1.198 | 130.5 |
| 75,000 | 3,748 | 30 | 210.579/223.159 | 0.000/0.000 | 1.011/1.513 | 1.011/1.513 | 200.7 |
| 100,000 | 5,465 | 30 | 274.466/291.954 | 0.000/0.000 | 1.008/1.561 | 1.008/1.561 | 260.6 |

表格中的 `Prefilter 候选数` 是本次 seed 的候选总数，包含 1 个 seed，因此 100,000
行的 5,465 对应阶段统计中的 5,464 个非 seed 候选加 seed 本身；阶段表和聚合表明确
按非 seed 候选统计时使用 5,464。

100,000 人池的阶段明细如下；时延和占比均为 p50/p95。`CandidateRanking` 已包含
其下的物化、Object Fact refresh/provider、评分和排序，不能与这些子项相加：

| 阶段 | 时延（ms；<1 ms 显示 µs） | 占 ProduceMatch |
|---|---:|---:|
| SeedPreparation | 0.000/0.000 | 0.0%/0.0% |
| SessionPreparation | 0.000/0.508 | 0.0%/33.6% |
| AttemptPreparation | 0.000/0.000 | 0.0%/0.0% |
| Prefilter | 0.000/1.001 | 0.0%/100.0% |
| CandidateRanking（含子项） | 0.941/1.010 | 63.1%/100.0% |
| CandidateMaterialization | 0.000/0.000 | 0.0%/0.0% |
| ObjectFactRefresh（物化子项） | 0.000/0.000 | 0.0%/0.0% |
| ObjectFactProvider（物化子项） | 0.000/0.000 | 0.0%/0.0% |
| CandidateScoring | 0.000/0.000 | 0.0%/0.0% |
| CandidateSort | 0.0 µs/0.0 µs | 0.0%/0.0% |
| CanJoin | 0.000/0.506 | 0.0%/33.3% |
| MatchFactUpdate | 0.0 µs/0.0 µs | 0.0%/0.0% |
| CanComplete | 0.0 µs/503.9 µs | 0.0%/17.6% |
| MatchBuild | 0.000/0.000 | 0.0%/0.0% |
| Commit | 0.0 µs/0.0 µs | 0.0%/0.0% |

CandidateMaterialization 的 529 次调用是统一的 `Frame.Object` seam 访问（500 次候选
访问加 29 次 CanJoin 重访）；本规则没有 Object Facts，Ticket 的 slot 为 nil，
因此 `Frame.Object` 直接返回空 borrowed Facts，不产生 Object refresh/provider/cache/
growth/error 事件。

对应的聚合调用次数为：1 个 seed，1 次 Prefilter，5,464 个非 seed Prefilter 候选，500 次
候选访问，529 次候选 ObjectFact seam 访问，obj-refresh/provider/cache/growth/errors
全为 0，500 次评分，29 次 CanJoin，29 次 Match Fact 更新，30 次 CanComplete，以及
1 次 Commit。

结论：在该环境下，TicketID 黑白名单索引版本的 100,000 人池一次完整匹配尝试 p50
为约 1.008 ms、p95 为约 1.561 ms；其中 `BeginMatchRound` 已降至计时器粒度以下，
`ProduceMatch` 约 1.008/1.561 ms。`setup` 是将池填充进索引的成本，100,000 人约
274.466/291.954 ms，不属于一次已入池匹配热路径。优化后 `arrival` runtime 在 Add 时
维护到达序列，`BuildRound(limit=1)` 在找到首个 active TicketID 后早停，不再为 round
全量构造候选或 DocID snapshot，因此 `round` 不再随 N 线性增长；本数据分布下动态等级/分数
范围命中的候选共约 5,465 人（含 seed；非 seed 候选约 5,464 人），因此 `produce`
主要随 Prefilter 候选数增长。

### 同一 MatchRound 连续 10 次 ProduceMatch

为观察一轮内的连续调用，使用：

```powershell
go run ./cmd/match-benchmark -sizes=100000 -samples=20 -warmups=3 -produces-per-round=10 -attempt-limit-per-round=100000
```

每个样本只调用一次 `BeginMatchRound`，然后固定调用 10 次
`ProduceMatchWithMetrics`。该选项同时把规则配置为
`attemptLimitPerProduceMatch=1`、`attemptLimitPerMatchRound=100000`，所以 round 的
seed snapshot 上限来自独立的 `-attempt-limit-per-round` 参数，并取本场景 100,000
池的最大值；不是把调用次数写死为 10，也不是预计成功的 Match 数。即使某次 Produce
没有返回 Match，后续调用仍会执行。`-produces-per-round` 只控制调用次数，两个参数
可以独立调整。这里不能把 round limit 设为 10：首个成功 Match 可能正好移除
snapshot 前 10 个 arrival seed，剩余 9 次调用将立即耗尽，无法观察同一轮继续处理
后续 seed 的真实成本。主表中的
`produce` 是 10 次调用时长之和，`total` 是一次 `BeginMatchRound` 加这 10 次调用。

本次运行（2026-09-02，前 3 个 warmup 丢弃）结果为：

| 池规模 | Prefilter 候选数 | 成功 Match | 剩余 | setup p50/p95 ms | round p50/p95 ms | 10 次 produce 总和 p50/p95 ms | round+10 produces p50/p95 ms |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100,000 | 5,465 | 30 人 Match × 10 | 99,700 | 383.800/419.165 | 4.180/4.885 | 15.117/16.061 | 19.117/20.279 |

序列 p50/p95（ms）为：第 1 次 `1.985/2.390`、第 2 次 `1.311/2.000`、第 3 次
`1.462/2.413`、第 4 次 `1.512/2.513`、第 5 次 `1.518/2.006`、第 6 次
`1.515/2.259`、第 7 次 `1.516/2.006`、第 8 次 `1.505/1.997`、第 9 次
`1.504/2.008`、第 10 次 `1.595/2.023`；累计到第 10 次为 `15.117/16.061`。
每个样本的结果计数为成功 10、失败 0、耗尽 0、消费 seed 10、剩余 99,700。

这里必须把 `-attempt-limit-per-round` 设为 100,000，而不能设为 10。10 是
Produce 调用次数，不是本轮允许尝试的 seed 上限；如果 round snapshot 只有前 10 个
arrival TicketID，首个成功 Match 可能一次提交掉这 10 个 ID，后续调用就会全部耗尽。
本次配置让 `BuildRound(limit=100000)` 从 100,000 人池生成完整 snapshot，实际长度为
`min(limit,pool)=100000`，从而 10 次 Produce 能继续取得后续 seed；round 的
`4.180/4.885 ms` 包含这次完整 snapshot 构建，但不再包含旧的第二份 `order` 拷贝、
全量 duplicate/unknown TicketID 校验及 100,000 次 store lookup。

在相同参数下，删除全量校验前记录的 round 为 `28.340/38.715 ms`，当前为
`4.180/4.885 ms`；两次运行受 Windows 后台负载影响，绝对值仅作参考，但下降来自
移除第二份 TicketID slice、全量 duplicate/unknown 校验和逐项 store lookup。当前 100,000
Ticket round 仍保留
runtime 返回的一个 `[]TicketID`（约 800 KB、1 次 slice 分配）；seed runtime 的
`BuildRound(limit=1)` 微基准仍为四种策略各 `8 B/op、1 alloc/op`，`nextSeed` 的
逐项 TicketID lookup 仍保留用于跳过已 Commit snapshot 项。

内存复核显示，benchmark 规则没有 Object-scoped Fact 时不会为每个 Ticket 创建
`ObjectSlot` 或其 list buffer；`storedTicket` 只保留 nil 的可选 slot 指针。本次完整
运行的 100,000 人 live heap 为 260.6 MiB。ObjectSlot 冷/稳态微基准和 heap profile
属于单独的历史测量，不能从本次 round/produce 表直接推导；当前规则包含每 Ticket
唯一 `ticketId` 的 Prefilter 索引，Ticket 所有权复制仍是主要内存来源。

阶段数据来自启用 `ProduceMatchWithMetrics` 的诊断路径，包含时钟采样本身的微小
开销，不应与默认关闭 metrics 的生产路径作严格的绝对值对比。Windows 上亚毫秒
阶段受系统计时粒度影响，显示为 0.000 并不表示调用次数为零；应结合上面的调用
次数和整体 `produce` 时延解读。

上述时间是本机一次运行的参考值；CPU 频率、后台负载、Go GC 和样本数都会影响结果，
不应当视为跨机器 SLA。`CandidateScoring` 现在使用 borrowed Ticket/Facts，不再为每个
候选 clone；Object slot 的稳定 refresh/cache 路径由下面的 microbenchmark 单独验证。

## 代码验证

```powershell
gofmt -w internal/matchsystem/seed_order.go internal/matchsystem/seed_order_test.go internal/matchsystem/logical_node_round_test.go
go test ./internal/matchsystem/... -count=1
go test ./... -count=1
go test ./internal/matchsystem -run '^$' -bench 'BenchmarkSeedOrderRuntimeBuildRound100kLimit1' -benchtime=1s -benchmem -count=3
go run ./cmd/match-benchmark -samples=20 -warmups=3
go run ./cmd/match-benchmark -sizes=100000 -samples=20 -warmups=3
go run ./cmd/match-benchmark -sizes=100000 -samples=20 -warmups=3 -produces-per-round=10 -attempt-limit-per-round=100000
git diff --check
```
