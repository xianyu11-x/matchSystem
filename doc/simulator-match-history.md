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
- `createdAt` 使用 Unix 毫秒时间戳；`durationMs`：以毫秒为单位的队列等待耗时，定义为 `max(0, Match.createdAt - min(member.createdAt))`。由于当前模拟器没有引擎执行起止时间，这个字段不表示匹配 CPU/处理耗时；
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
