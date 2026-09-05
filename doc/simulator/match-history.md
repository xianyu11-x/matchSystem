# 模拟器 Match 历史

模拟器在一次 `ProduceMatch` 成功提交完整 Match 后，会把成员和 Match Fact 复制为独立的观察快照。记录不是事件日志：每条记录都可以通过 Match ID 查询并展开成员。

## 数据与接口

列表接口：

```http
GET /api/v1/matches?cursor=0&limit=100
```

列表按成局时间倒序返回，第一页优先包含最近的 Match；`limit=50` 因而表示最近保留的 50 条记录。`cursor` 是当前内存列表上的 offset，新增或淘汰记录后旧 cursor 不保证继续指向同一条记录，应在需要时重新读取第一页。响应中的 `total` 始终存在，空历史明确返回 `0`，客户端可据此丢弃旧缓存。

详情接口：

```http
GET /api/v1/matches/{matchId}
```

`matchId` 由同一个 `Simulator` 实例在其整个生命周期内按 `match-1`、`match-2` … 生成（不会随单次 runtime 重置），列表和成局响应返回同一个 ID。详情响应中的字段包括：

- `round`、`createdAt`、`physicalNodeId`：成局轮次、轮次时间和承载节点；
- `memberCount`：本次 Match 实际接受的 Ticket 数量，包含 seed；它与 `tickets` 的原始成员数量一致，
  即使 HTTP 为 JavaScript 安全整数边界而省略某些 Ticket，仍表示 Match 的真实成员数；
- `createdAt` 使用 Unix 毫秒时间戳；`durationMs`：以毫秒为单位的队列等待耗时，定义为 `max(0, Match.createdAt - min(member.createdAt))`。该时间是调用方指定的轮次逻辑时间，不是每局真实完成时刻；这个字段不表示匹配 CPU/处理耗时；
- `processingDurationNs`：该局成功 `PhysicalNode.ProduceMatch` 调用的实测墙钟耗时（纳秒），测量边界见下文；
- `logicalNode.rule`、`logicalNode.placementId`：RuleKey 与 Placement 标识；
- `facts`：成局时的 Match Fact 快照；
- `tickets`：兼容现有客户端的紧凑成员列表；
- `members`：完整的成员观察，包括 Ticket 属性、成局本次 frame 中由默认/自定义 ObjectFactProvider 计算出的 Object Fact、Owner、RouteDecision 和状态。

找不到记录（包括已被上限淘汰的记录）返回 HTTP 404，错误码为 `MATCH_NOT_FOUND`。

## 内存上限和生命周期

Match 历史只保留内存中的最近记录，不是持久化存储。默认最多保留 `1000` 条，可在场景顶层设置：

```json
{
  "schemaVersion": "simulator-scenario/v1",
  "matchHistoryLimit": 500,
  "physicalNodes": [],
  "rules": []
}
```

`matchHistoryLimit` 为 `0` 或省略时使用默认值 `1000`；负数会在场景校验阶段拒绝。超过上限时按成局顺序淘汰最旧记录，等待中的 Ticket 观测不受该上限影响。

`ReplaceScenario` 会先构建并校验新运行时，发布成功后丢弃旧运行时的 Ticket、Match 历史和事件；Match ID 序列绑定于 `Simulator` 实例生命周期，不会因场景替换重置，因此旧 ID 不会被新场景复用。替换失败时旧场景和历史保持不变。调用 `Close` 后运行时不可再查询，所有记录随运行时释放。

## 快照安全

观察仓库由读写锁保护。写入时复制 Match、Ticket、Fact map 和 slice；列表、详情和成局返回值再次复制。调用方修改响应中的成员属性、Object Fact、Match Fact 或成员数组，不会改变仓库中的记录，也不会影响匹配核心。

## 等待与处理耗时的测量边界

`durationMs` 保持既有语义：每局取最早成员的等待量，聚合时每局一个样本，不是所有成员等待量的均值。未来创建时间产生的负差钳制到 `0`；整数减法溢出钳制到 `MaxInt64`。HTTP 创建 Ticket 时省略/零创建时间使用服务端当前时间，直接嵌入 Go 的宿主仍须遵守其时间单位契约。前端旧记录缺失等待字段不补零；超出 JavaScript 安全整数范围的值被排除并计数。

`processingDurationNs` 在模拟器应用层 `produceOne` 中，通过 `time.Now()` / `time.Since()` 的单调时钟直接包围一次 `adapter.ProduceMatch(ctx)` 调用。包含 owner 命令投递、goroutine 调度、Provider、核心匹配与核心提交、结果返回；不包含模拟器命令锁等待、`BeginRound`、此前未成局调用、历史快照存储、事件写入、HTTP 编解码、网络和浏览器渲染。这不是 CPU 时间，也不是整轮端到端延迟。`RunRound`、`ProduceAll`、`ProduceOne`、`ProduceMatch` 都经过同一测量点。

仅成功产生 Match 的调用记录对应耗时；没有把整轮耗时按比赛数平均分摊，也不把独立的失败调用归因给后续比赛。一次成功调用内部发生的候选尝试自然包含在该调用内。字段使用纳秒整数避免毫秒截断，但时钟实际分辨率由 Go/操作系统决定，短调用可以真实返回 `0`；`0` 不能解释为没有执行工作。历史列表、详情和成局响应保留同一原始值。

## 多局聚合分析

进入 Web「比赛分析」，先选择时间窗口，再勾选比赛列表中的多场比赛。所有汇总卡片、数值属性分布和 Rule / Placement 分组都只计算当前窗口内的已选比赛。详情按钮独立打开单局详情，不改变多选。

- 「分析窗口全部」持续分析当前窗口，包括后续刷新的新增记录。
- 「选中窗口全部」固定当前 ID 集合；后续新增比赛不会自动加入。
- 「清空选择」保留显式的空选集，统计为空，不悄悄回退全部。
- 已选但在窗口外或已被历史上限淘汰的记录不参与统计；更换窗口后仍存在的选中 ID 可重新进入统计。

等待量和处理耗时分别显示均值、P95、有效样本数/分析比赛数。属性分析默认选等待量，可切换到处理纳秒或 Match Facts，并展示总体方差、标准差、中位数、极差和 P95（排序后线性插值）。字段缺失时跳过样本；处理时间为零时仍作为真实样本。旧服务端与 demo 没有处理测量时不会伪造数据。处理汇总换算为毫秒显示，原始纳秒可在属性分析和详情查看。
