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
go run ./cmd/match-benchmark -samples=10 -warmups=2
```

每个规模先生成并填充一个新 `LogicalNode`。填充时间单独列为 `setup`，不计入一次
匹配热路径。计时包括：

- `round`：`BeginMatchRound`，包含 arrival seed snapshot 构建；
- `produce`：`ProduceMatch`，包含 Prefilter、候选排序、CanJoin/CanComplete 和
  30 人提交；
- `total`：`round + produce`，作为完整的一次匹配尝试。

表格中的时间为每个规模 10 次测量的 p50/p95，单位为毫秒；前 2 个 warmup 样本丢弃。
`heap` 是填充完成并 GC 后的 Go live heap（包含基准输入和节点索引），用于观察
规模增长，不是精确 RSS。

## ProduceMatch 阶段打点

基准调用显式的 `LogicalNode.ProduceMatchWithMetrics`，它返回一次调用的聚合
`ProduceMatchMetrics`；正常生产调用仍使用 `ProduceMatch`，默认不读时钟、不创建
metrics 快照，也不输出逐玩家日志。阶段包括：

- `SeedPreparation`：本轮 seed 预留和 cursor 推进；`SessionPreparation`：Tick
  Fact、Fact Frame 和 Prefilter TickSession 初始化；`AttemptPreparation`：seed
  Object Fact 与 Match Fact Initialize。
- `Prefilter`：索引查询、bitmap 组合和 seed 移除；同时返回 lookup/contains/AND/
  OR/AND-NOT 计数。
- `CandidateRanking`：候选 Object Fact 物化、评分、bounded Top-L heap 和最终排序；
  `CandidateMaterialization`、`CandidateScoring`、`CandidateSort` 是其中的细分阶段，
  不是与 `CandidateRanking` 相加的独立阶段。
- `CanJoin`、`MatchFactUpdate`、`CanComplete`：分别聚合谓词、OnJoin Fact 更新和
  完成判定调用耗时；`MatchBuild` 与 `Commit` 覆盖结果构造和 ticketStore 提交。

阶段表输出每个阶段的 p50/p95 时延，以及按每个样本计算的阶段占该样本
`ProduceMatch` 时延的 p50/p95 百分比；另有聚合调用次数表，避免用高基数玩家 ID
作为日志或指标标签。`CandidateRanking` 包含其细分阶段，所以阶段占比不能简单求和。

## 实际运行结果

运行命令：`go run ./cmd/match-benchmark -samples=10 -warmups=2`

运行日期：2026-09-01（Asia/Shanghai，三轮连续运行，约 19:40）。三轮均使用同一
工作区、同一命令和同一进程配置；下表采用第 2 轮完整结果。选择依据是 100,000 人池
的 `total` p50/p95：第 1 轮为 18.682/20.058 ms，第 2 轮为 18.380/19.021 ms，
第 3 轮为 17.444/18.336 ms，第 2 轮的 total 位于三轮中间。汇总表和下面的阶段表
均来自这同一轮的 10 个测量样本（前 2 个 warmup 已丢弃），没有跨轮拼接。

环境：Windows 11 专业版 x64（build 26100），Intel Core i7-13700（16 核/24 线程），
64 GiB 内存；Go `go1.24.6 windows/amd64`，`GOMAXPROCS=24`。这是单进程、单次串行
匹配测量，不包含网络、序列化或并发竞争。

| 池规模 | Prefilter 候选数 | Match | setup p50/p95 ms | round p50/p95 ms | produce p50/p95 ms | 完整一次 total p50/p95 ms | live heap MiB |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1,000 | 55 | 30 | 2.568/3.105 | 0.000/0.000 | 0.508/0.540 | 0.508/0.540 | 3.1 |
| 5,000 | 270 | 30 | 10.295/12.815 | 0.000/0.626 | 1.000/1.503 | 1.000/1.503 | 13.3 |
| 10,000 | 542 | 30 | 19.506/22.462 | 0.000/0.000 | 1.004/2.008 | 1.004/2.008 | 26.2 |
| 25,000 | 1,422 | 30 | 48.216/55.243 | 0.999/1.099 | 3.010/3.998 | 3.831/4.127 | 63.5 |
| 50,000 | 2,826 | 30 | 111.676/115.629 | 2.002/2.047 | 6.511/7.589 | 8.499/8.711 | 126.3 |
| 75,000 | 3,748 | 30 | 187.473/200.511 | 2.597/3.724 | 9.505/10.165 | 12.029/13.759 | 193.8 |
| 100,000 | 5,465 | 30 | 241.536/264.352 | 4.488/5.118 | 13.959/14.514 | 18.380/19.021 | 252.2 |

100,000 人池的阶段明细如下；时延和占比均为 p50/p95。`CandidateRanking` 已包含
其下的物化、评分和排序，不能与三个子项相加：

| 阶段 | 时延（ms；<1 ms 显示 µs） | 占 ProduceMatch |
|---|---:|---:|
| SeedPreparation | 0.000/0.000 | 0.0%/0.0% |
| SessionPreparation | 0.000/0.000 | 0.0%/0.0% |
| AttemptPreparation | 0.0 µs/505.9 µs | 0.0%/3.9% |
| Prefilter | 0.000/1.005 | 0.0%/6.9% |
| CandidateRanking（含子项） | 13.508/14.021 | 100.0%/100.0% |
| CandidateMaterialization | 2.000/4.505 | 13.9%/31.0% |
| CandidateScoring | 9.536/11.601 | 71.0%/81.2% |
| CandidateSort | 0.508/1.002 | 3.6%/7.5% |
| CanJoin | 0.000/1.089 | 0.0%/7.6% |
| MatchFactUpdate | 0.0 µs/996.0 µs | 0.0%/7.0% |
| CanComplete | 0.000/0.000 | 0.0%/0.0% |
| MatchBuild | 0.000/0.000 | 0.0%/0.0% |
| Commit | 0.000/0.000 | 0.0%/0.0% |

对应的聚合调用次数为：1 个 seed，1 次 Prefilter，5,464 个非 seed 候选访问，
5,493 次候选 ObjectFact 物化调用，5,464 次评分，29 次 CanJoin，29 次 Match
Fact 更新，30 次 CanComplete，以及 1 次 Commit。

结论：在该环境下，TicketID 黑白名单索引版本的 100,000 人池一次完整匹配尝试 p50
为约 18.4 ms、p95 为约 19.0 ms；其中 `BeginMatchRound` 约 4.5/5.1 ms，
`ProduceMatch` 约 14.0/14.5 ms。`setup` 是将池填充进索引的成本，100,000 人约
242/264 ms，不属于一次已入池匹配热路径。由于
`arrival` seed snapshot 需要遍历整个等待池，`round` 随 N 增长；本数据分布下动态
等级/分数范围命中的候选约 5,465 人，因此 `produce` 主要随 Prefilter 候选数增长。

阶段数据来自启用 `ProduceMatchWithMetrics` 的诊断路径，包含时钟采样本身的微小
开销，不应与默认关闭 metrics 的生产路径作严格的绝对值对比。Windows 上亚毫秒
阶段受系统计时粒度影响，显示为 0.000 并不表示调用次数为零；应结合上面的调用
次数和整体 `produce` 时延解读。

上述时间是本机一次运行（第 2 轮）的参考值；CPU 频率、后台负载、Go GC 和样本数都会
影响结果，不应当视为跨机器 SLA。此前文档中的 126.7 MiB heap 和 15.613/18.546 ms
total 属于旧实现/旧轮次，且曾把不同轮次的字段混在同一行，不能与本节 TicketID 版本
直接对比；本节已统一为当前实现同一轮次的数据。

## 代码验证

```powershell
go test ./...
go vet ./...
go build ./cmd/match-benchmark
```
