# int64 范围索引方案评估

2026-11-20，在提交 `dd533b2` 上对比候选预筛选的内存索引。生产代码仍使用现有的 Roaring 分块倒排索引。

## 值域稳定的分数字段

现有索引为每个不同值保存 DocID 位图。查询对排序值做两次二分，中间按每 16 个值的整块取并集，两端按值取并集。改值时同步更新已有块，出现新值或某个值消失时，下次 `BeginTick` 会重排取值目录并重建块。小候选集还可以按 DocID 直接查值，默认阈值为 4096。[范围索引](../../../pkg/matchsystem/prefilter/int64_range_index.go) · [预筛选执行器](../../../pkg/matchsystem/prefilter/store.go)

前缀原型保留这些结构，在每个块末尾额外存一张累计位图及其基数。用右端前缀减去左端前缀，就能一次取得区间中所有完整块的 DocID。两端仍合并逐值位图。每张票在该字段只有一个值，因此两端不会引入重复计数，基数估计也可以用前缀计数之差计算。[位图范围编码原理](https://www.comp.nus.edu.sg/~chancy/sigmod99.pdf)

实验使用 10 万个连续 DocID，分数均匀随机取 1–500。下表的两个随机范围宽度各使用 1,024 个预生成窗口，所有窗口均在值域内。两种实现在这些窗口返回的 DocID 集合和基数估计逐项一致。

| 每次操作 | 当前分块索引 | 块边界前缀位图 |
| --- | ---: | ---: |
| 固定窄范围，覆盖 7 个值 | 6.04 µs | 约 6 µs |
| 随机范围，覆盖 101 个值 | 120.12 µs | 10.98 µs |
| 随机范围，覆盖 301 个值 | 185.97 µs | 11.29 µs |
| 将 100 张票改为已有分数，再准备一次索引 | 34.15 µs | 327.83 µs |

最后一行每次操作的分配量分别为 0 和 582,458 字节。前缀原型保留 33 张累计位图（含空前缀），每次准备都重新分配并构建它们。这里的分配量仅反映该操作的分配流量，常驻内存尚未测量。

按这些单项耗时相减，在一次这样的更新批次后，大约需要 3 次宽度 101 或 2 次宽度 301 的实际范围查询才能抵消额外维护时间。小 scope 的逐 DocID 检查不会获得这笔收益。该交叉点仅用于解释这个样本，线上判断还需要每次写入后实际执行的查询次数和窗口宽度。

### 接入仓库的组合规则

沿用现有的 10 万票预筛选基准，只将 `score` 字段切换成前缀原型。`level`、TicketID 黑白名单、动态边界、执行计划、固定 seed 和返回结果处理都沿用原路径。两种实现返回的 5,464 个非 seed 候选逐个一致，执行计数均为 4 次 lookup、0 次 contains、1 次交集、2 次并集和 1 次差集。校验时观测到 `score` 前缀被查询 1 次、估算 2 次。[基准规则与数据](../../../pkg/matchsystem/prefilter/prefilter_benchmark_test.go)

| 同批对照，500 ms × 5 次 | 预筛选耗时中位数（样本范围） | 每次分配字节 | 每次分配次数 |
| --- | ---: | ---: | ---: |
| 当前实现 | 459.66 µs（382.35–467.86） | 175,608 | 134 |
| 仅 score 使用前缀 | 242.86 µs（236.11–264.95） | 124,232 | 121 |

在同一进程中先测当前实现，再测前缀原型。计时覆盖 `CandidatesWithStats` 和从结果集中移除 seed。这个规则的查询耗时下降 47.2%。索引准备、成局和删票均在计时范围外。

同一次 `ProduceMatch` 尝试多个 seed 时可以复用已准备的索引。仓库另一组典型基准中，连续 10 次 `ProduceMatch` 恰好尝试 10 个 seed，且每次都提交并删除成员。在这种节奏下，按每次准备全量重建前缀的原型很难摊薄维护成本。[会话与匹配流程](../../../pkg/matchsystem/seed_evaluator.go) · [10 次匹配的阶段记录](performance-benchmark.md)

## 每张票取值都不同

另一组实验随机打乱 1–100,000，使每个 DocID 恰好对应一个唯一值。对照结构是按 `(值, DocID)` 排序的连续数组。它通过二分确定起止位置，将命中项写入新的 Roaring 位图，改值后批量重排。

| 每次操作 | 当前分块索引 | 有序数组 |
| --- | ---: | ---: |
| 固定范围命中 7 张票 | 0.54 µs | 0.38 µs |
| 固定范围命中 10,100 张票 | 1.28 ms | 0.70 ms |
| 固定范围命中 60,001 张票 | 1.54 ms | 1.31 ms |
| 替换 100 个独有值，再准备一次索引 | 57.29 ms | 17.29 ms |

这组数据支持对高基数且不同值频繁变化的字段单独评估有序数组。数组原型使用稠密 DocID，尚未覆盖删除后的空洞和 DocID 复用。低基数前缀原型也未在这组数据上运行。

另外对照了仓库已依赖的 Roaring v2.25.0 的位切片索引（BSI）。本次直接调用 `CompareValue(1, RANGE, min, max, nil)` 的原型在正数样本通过，混合有符号值时出现错误命中。例如查询 `[-999,0]` 会额外返回分数为 1 的 DocID。自动位宽和显式完整 int64 上下界均复现。该调用方式无法满足现有闭区间契约。

## 单字段平衡范围树

每个物理索引只管理自己的字段。规则提供查询边界并组合各字段返回的 DocID 集合，因此同一个 `score` 索引可以服务不同宽度的分数查询，查询方也可以单独使用其他字段的索引。

k-d 树用于单个数值字段时，每层都沿同一个数轴切分，等价于一维二叉搜索划分。设字段有 `D` 个不同值，平衡树和当前有序数组都能在 `O(log D)` 内定位边界。可进一步给树节点保存该子树的 DocID 位图，查询时将区间分解为至多 `O(log D)` 棵完整子树，再合并它们的位图。合并和生成结果的耗时另计。树上保存的所有数据都属于同一个字段。[一维范围树的区间分解](https://www.cs.umd.edu/class/fall2021/cmsc754/Lects/cmsc754-fall-2021-lects.pdf)

原型从已准备好的 `score` 索引复用逐值位图，按不同分数的中位位置建平衡树，每个叶节点最多存 8 个键。缓存版本在覆盖至少 16 个键的子树上另存聚合位图。500 个取值下共缓存 51 张位图，Roaring 的 `GetSizeInBytes` 合计 573,674 字节，未计树、映射及完整 Go 堆开销。已有分数间改值时更新原有索引，并修改旧、新叶节点到共同祖先之间的缓存。两条路径共用的祖先已经包含该 DocID。[单字段树实验](../../../experiments/rangeindex/testdata/score_tree_test.go)

实验沿用 10 万张票、500 个不同分数的均匀合成分布。每个窗口宽度预生成 1,024 个区间，校验完整 DocID 位图和基数估计。改值场景把同一组 100 张票的分数从 `v` 改为 `v % 500 + 1`，再改回。这种改动多数发生在相邻分数间，旧、新路径通常有较多共同祖先。任意跨度的改分、新分数的加入、分数最后一张票的删除，以及 DocID 复用均需另测。

| 单字段操作，300 ms × 3，中位数（µs/op） | 现有分块 | 块前缀 | 普通平衡树 | 缓存子树位图的平衡树 |
| --- | ---: | ---: | ---: | ---: |
| 查询范围覆盖 7 个值 | 15.45 | 15.01 | 15.07 | 16.07 |
| 查询范围覆盖 101 个值 | 127.05 | 11.15 | 265.28 | 75.22 |
| 查询范围覆盖 301 个值 | 181.94 | 11.86 | 315.74 | 63.94 |
| 100 张票改到已有分数，再准备索引一次 | 36.32 | 367.02 | 38.21 | 44.05 |

宽度 7 这一行使用变化的查询窗口，前文的窄范围使用固定窗口。表内四列采用相同口径。普通树对每个命中分数都取一次位图，宽范围的成本超过现有分块实现。缓存树的查询和改值成本介于分块与前缀之间。以宽度 101 为例，与缓存树相比，前缀每批更新多花约 323 µs，每次查询省约 64 µs，在该样本中约需 6 次查询摊平差额。这里用的是单项计时的算术估算。树的实测尚未覆盖完整 `ProduceMatch`。

## 测量条件与适用范围

Windows 11 Pro 26100，Intel Core i7-13700，Go 1.24.6 windows/amd64，`GOMAXPROCS=1`、`GOGC=100`。表中取多次 Go benchmark 的 ns/op 中位数，每次 ns/op 是多轮操作的平均耗时。固定查询与改值实验每项 200 ms × 3，随机窗口每项 500 ms × 3，组合规则每项 500 ms × 5。生成取值用固定种子 791900017，随机窗口用 314159。改值实验往返修改同一批 DocID 1–100，输入票据预先生成。

仓库的等级和分数规则是合成负载。目前缺少线上字段取值、查询宽度及写入与查询比例样本。生产选型还需要带真实入池、撤销、成局删除和 DocID 复用的回放，对比完整 `ProduceMatch` 的 p95 和每个逻辑节点的常驻内存。前缀方案值得先对分数等中宽范围字段验证按变更标记跳过重复准备，以及更新时的增量维护。[既有匹配池基准](performance-benchmark.md)

## 复现

在仓库根目录运行以下 PowerShell。Go 的 `-overlay` 选项把[单索引对照](../../../experiments/rangeindex/testdata/range_experiment_benchmark_test.go)、[单字段范围树](../../../experiments/rangeindex/testdata/score_tree_test.go)和[组合规则实验](../../../experiments/rangeindex/testdata/prefix_integration_test.go)作为同包测试加载，便于直接与私有索引实现比较。

```powershell
$packageDir = (Resolve-Path 'pkg/matchsystem/prefilter').Path
$files = @{}
Get-ChildItem 'experiments/rangeindex/testdata/*_test.go' | ForEach-Object {
  $files[(Join-Path $packageDir $_.Name)] = $_.FullName
}
$overlay = Join-Path $env:TEMP 'matchsystem-range-index-overlay.json'
$json = @{ Replace = $files } | ConvertTo-Json
[IO.File]::WriteAllText($overlay, $json, [Text.UTF8Encoding]::new($false))
$env:GOMAXPROCS = '1'
$env:GOGC = '100'
$pkg = './pkg/matchsystem/prefilter'
go test "-overlay=$overlay" $pkg -run '^(TestExperimentRangeAlternativesParity|TestExperimentRandomWindowParity|TestCompositionPrefixParity)$' -count=1
go test "-overlay=$overlay" $pkg -run '^$' -bench '^BenchmarkExperiment(Ranges|Update100AndPrepare)$' -benchmem -benchtime=200ms -count=3
go test "-overlay=$overlay" $pkg -run '^$' -bench '^BenchmarkExperimentRandomWindows$' -benchmem -benchtime=500ms -count=3
go test "-overlay=$overlay" $pkg -run '^$' -bench '^BenchmarkCompositionPrefixAB$' -benchmem -benchtime=500ms -count=5
go test "-overlay=$overlay" $pkg -run '^TestScoreTree' -count=1
go test "-overlay=$overlay" $pkg -run '^$' -bench '^BenchmarkScoreTree' -benchmem -benchtime=300ms -count=3
```

在同一 PowerShell 会话中，用下面的测试单独复现 v2.25.0 的 BSI 有符号区间错误。本次观测结果为测试失败，输出含具体区间及多命中的 DocID。

```powershell
go test "-overlay=$overlay" $pkg -run '^TestExperimentBSIInt64Bounds$' -count=1 -v
```
