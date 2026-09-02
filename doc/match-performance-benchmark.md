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

运行日期：2026-09-01（Asia/Shanghai）。下表来自当前工作区一次完整运行的
20 个测量样本（前 3 个 warmup 已丢弃）；Windows 计时器粒度会令部分短阶段显示为 0。

环境：Windows 11 专业版 x64（build 26100），Intel Core i7-13700（16 核/24 线程），
64 GiB 内存；Go `go1.24.6 windows/amd64`，`GOMAXPROCS=24`。这是单进程、单次串行
匹配测量，不包含网络、序列化或并发竞争。

| 池规模 | Prefilter 候选数 | Match | setup p50/p95 ms | round p50/p95 ms | produce p50/p95 ms | 完整一次 total p50/p95 ms | live heap MiB |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1,000 | 55 | 30 | 2.580/3.066 | 0.000/0.514 | 0.513/0.517 | 0.514/0.519 | 3.1 |
| 5,000 | 270 | 30 | 13.019/14.670 | 0.000/0.525 | 0.521/1.028 | 0.970/1.028 | 13.4 |
| 10,000 | 542 | 30 | 24.684/29.851 | 0.000/1.000 | 1.000/1.508 | 1.003/1.797 | 26.3 |
| 25,000 | 1,422 | 30 | 61.261/66.436 | 0.999/1.496 | 1.004/2.000 | 1.999/2.511 | 63.7 |
| 50,000 | 2,826 | 30 | 125.000/142.439 | 2.001/2.511 | 1.991/2.090 | 3.510/4.006 | 126.7 |
| 75,000 | 3,748 | 30 | 191.442/203.142 | 2.512/3.550 | 1.999/2.514 | 4.508/5.684 | 194.4 |
| 100,000 | 5,465 | 30 | 260.905/276.632 | 3.892/4.529 | 2.998/4.018 | 6.516/8.059 | 253.0 |

100,000 人池的阶段明细如下；时延和占比均为 p50/p95。`CandidateRanking` 已包含
其下的物化、Object Fact refresh/provider、评分和排序，不能与这些子项相加：

| 阶段 | 时延（ms；<1 ms 显示 µs） | 占 ProduceMatch |
|---|---:|---:|
| SeedPreparation | 0.000/0.000 | 0.0%/0.0% |
| SessionPreparation | 0.000/0.000 | 0.0%/0.0% |
| AttemptPreparation | 0.000/0.000 | 0.0%/0.0% |
| Prefilter | 0.000/1.000 | 0.0%/24.9% |
| CandidateRanking（含子项） | 2.009/2.512 | 67.0%/62.5% |
| CandidateMaterialization | 0.0 µs/999.0 µs | 0.0%/24.9% |
| ObjectFactRefresh（物化子项） | 0.000/0.000 | 0.0%/0.0% |
| ObjectFactProvider（物化子项） | 0.000/0.000 | 0.0%/0.0% |
| CandidateScoring | 0.0 µs/763.0 µs | 0.0%/19.0% |
| CandidateSort | 0.538/1.005 | 17.9%/25.0% |
| CanJoin | 0.0 µs/657.9 µs | 0.0%/24.8% |
| MatchFactUpdate | 0.0 µs/998.3 µs | 0.0%/32.0% |
| CanComplete | 0.000/0.000 | 0.0%/0.0% |
| MatchBuild | 0.000/0.000 | 0.0%/0.0% |
| Commit | 0.000/0.000 | 0.0%/0.0% |

CandidateMaterialization 的 5,493 次调用是统一的 `Frame.Object` seam 访问（5,464
次候选访问加 29 次 CanJoin 重访）；本规则没有 Object Facts，Ticket 的 slot 为 nil，
因此 `Frame.Object` 直接返回空 borrowed Facts，不产生 Object refresh/provider/cache/
growth/error 事件。

对应的聚合调用次数为：1 个 seed，1 次 Prefilter，5,464 个非 seed 候选访问，
5,493 次候选 ObjectFact seam 访问，obj-refresh/provider/cache/growth/errors 全为 0，
5,464 次评分，29 次 CanJoin，29 次 Match Fact 更新，30 次 CanComplete，以及 1 次
Commit。

结论：在该环境下，TicketID 黑白名单索引版本的 100,000 人池一次完整匹配尝试 p50
为约 6.516 ms、p95 为约 8.059 ms；其中 `BeginMatchRound` 约 3.892/4.529 ms，
`ProduceMatch` 约 2.998/4.018 ms。`setup` 是将池填充进索引的成本，100,000 人约
260.905/276.632 ms，不属于一次已入池匹配热路径。由于
`arrival` seed snapshot 需要遍历整个等待池，`round` 随 N 增长；本数据分布下动态
等级/分数范围命中的候选约 5,465 人，因此 `produce` 主要随 Prefilter 候选数增长。

内存复核显示，benchmark 规则没有 Object-scoped Fact 时不会为每个 Ticket 创建
`ObjectSlot` 或其 list buffer；`storedTicket` 只保留 nil 的可选 slot 指针。原先内嵌
`ObjectSlot` 为 120 B，100,000 人约多占 10.7 MiB；改为可选指针后本轮 heap 为
252.9 MiB，与未启用 ObjectSlot 的当前 HEAD 基线 252.1 MiB 接近。旧的 126.7 MiB
数据不是同一 benchmark 输入：当前规则包含每 Ticket 唯一 `ticketId` 的 Prefilter
索引；heap profile 中 `CloneTicket` 约 145.5 MB、输入 Ticket 约 70.5 MB，Prefilter
索引与 Ticket 所有权复制才是剩余主要占用。

阶段数据来自启用 `ProduceMatchWithMetrics` 的诊断路径，包含时钟采样本身的微小
开销，不应与默认关闭 metrics 的生产路径作严格的绝对值对比。Windows 上亚毫秒
阶段受系统计时粒度影响，显示为 0.000 并不表示调用次数为零；应结合上面的调用
次数和整体 `produce` 时延解读。

上述时间是本机一次运行的参考值；CPU 频率、后台负载、Go GC 和样本数都会影响结果，
不应当视为跨机器 SLA。`CandidateScoring` 现在使用 borrowed Ticket/Facts，不再为每个
候选 clone；Object slot 的稳定 refresh/cache 路径由下面的 microbenchmark 单独验证。

## 代码验证

```powershell
gofmt -w cmd/match-benchmark/main.go internal/common/ticket.go internal/matchsystem/candidate_ranking.go internal/matchsystem/candidate_ranking_test.go internal/matchsystem/evaluation_runtime.go internal/matchsystem/fact/frame.go internal/matchsystem/fact/frame_test.go internal/matchsystem/fact/object_slot.go internal/matchsystem/fact/object_slot_benchmark_test.go internal/matchsystem/fact/validator.go internal/matchsystem/fact_types.go internal/matchsystem/logical_node.go internal/matchsystem/object_fact_flow_test.go internal/matchsystem/produce_metrics.go internal/matchsystem/provider_descriptor_test.go internal/matchsystem/seed_evaluator.go internal/matchsystem/ticket_store.go internal/matchsystem/ticket_store_test.go internal/matchsystem/trusted_fact_flow_test.go internal/simulator/match_history_test.go internal/simulator/service.go
go test ./... -count=1
$env:GOMAXPROCS='1'; go vet ./...
go build ./...
go test ./internal/matchsystem/fact -run '^$' -bench 'ObjectSlot' -benchmem -count=5
go run ./cmd/match-benchmark -sizes=100000 -samples=20 -warmups=3
git diff --check
```
