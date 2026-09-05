# 持续流量与定时匹配

Tickets 页批量生成区域提供持续流量控制。先选择规则，配置生成器属性、属性随机种子及起始 TicketID，再配置到达模式与匹配参数，点击“开始持续注入”。停止会等待当前串行操作结束；返回后不会继续注入。关闭页面不会停止服务端任务，重新打开可查看和停止。

- 恒定：相邻到达间隔为 `1 / rate` 秒。
- Poisson（泊松）：间隔服从指数分布，均值为 `1 / rate` 秒，使用独立到达随机种子。
- 周期突发：每隔 `burstIntervalMs` 注入 `burstSize` 条。
- 匹配：每隔 `matchIntervalMs` 开始真实匹配轮次，`maxMatches` 是本轮所有物理节点合计成局上限，规则不允许则产出为零。

启动时复制完整生成器配置。属性生成统一调用 `GenerateBatch`，保留生成器扩展；每条 Ticket 使用属性随机流派生种子，ID 单调递增，创建时间为实际注入时的 Unix 毫秒。批量配置中的 count、createdAtStart 不控制持续流量。流量按 RuleKey 路由，不指定 PlacementID；客户端会移除所选 PlacementID。

相同属性种子与到达种子可重现属性序列与计划间隔，实际调度时间、手动操作和匹配结果不保证完全重放。计时器按下一到期时间唤醒，延迟时保留过期工作，状态 `lagMs` 报告最近一次调度延迟，不保证操作系统实时性。

状态显示已注入、已产出、轮数、下一 TicketID 与错误。停止后重启需把起始 ID 更新为下一 TicketID，或选择未使用的 ID 段。重复 ID、生成/路由/规则错误会停止任务并保留成功前缀计数。ID 不超过 JavaScript 安全整数 `9007199254740991`；耗尽时失败，下一 ID 显示 0。

成功替换场景和关闭模拟器都会停止旧任务；替换验证失败时旧任务继续运行。每个实例最多一个任务，重复开始会拒绝。任务属于运行状态，不保存进场景文件，不在服务重启后恢复。

HTTP 使用 `GET /api/v1/traffic` 查询、`POST /api/v1/traffic` 开始、`DELETE /api/v1/traffic` 停止。POST 为 `{ "config": <流量配置>, "generator": <CustomTicketsRequest> }`。见 [OpenAPI](../../api/openapi/simulator.yaml) 与 [配置 Schema](../../api/schema/simulator-traffic/v1.schema.json)。速率上限每秒 10000 条，匹配/突发间隔 100..86400000 毫秒，每次匹配及突发数量上限 10000。

后台运行不限制池及事件内存总量，用户应监测状态并按需停止。Demo 模式提示需要真实服务，不伪造运行状态。
