# 持续流量与定时匹配

Tickets 页的“批量与持续流量”区域提供持续流量控制。先选择规则与节点，配置生成器属性、
属性随机种子、起始 TicketID 及共同 Object Facts，再配置时钟模式、到达模式与匹配参数。
实时模式按钮为“开始实时注入”，固定模式按钮为“开始固定步长运行”。停止会等待当前串行
操作结束；返回后不会继续注入。关闭页面不会停止服务端任务，重新打开可查看和停止。

## Web 界面固定步长操作

在“持续流量与定时匹配”中将“模拟时钟”设为“固定步长运行”，然后填写三个固定时间字段：

- “虚拟步数”（`steps`）：1..10000000 的安全整数；任务执行这些步后自动结束。
- “步长（毫秒）”（`stepMs`）：1..86400000 的安全整数；每一步将虚拟时间推进该值。
- “虚拟起始时间（Unix ms）”（`startTimeMs`）：非负 JavaScript 安全整数；API 省略该字段时
  使用服务端时钟，界面默认填入当前时间，手动填写时也必须保持安全整数范围。

三个字段组合出的最终时间 `startTimeMs + (steps - 1) * stepMs` 也必须保持 JavaScript 安全
整数；超出范围的虚拟时间线会在开始时被拒绝。

同时设置“到达分布”“匹配间隔（毫秒）”“每次最多产出 Match”和“到达随机种子”。固定模式
的匹配间隔允许 1..86400000 毫秒；选择“恒定”或“Poisson（泊松）”时填写到达速率，选择
“周期突发”时填写突发数量和突发间隔。点击“开始固定步长运行”后，表单在任务运行期间锁定。
状态变为“已完成”时，状态区的 `step/totalSteps`、`virtualNow` 和“墙上耗时”可用于核对
进度；“墙上耗时”包含 Python RPC、Go 和核心匹配的执行成本，`virtualNow` 只按 `stepMs`
推进。停止后可点击“接续编号”把下一 TicketID 带回起始编号，再次运行。

`POST /api/v1/traffic` 的 `config.mode` 有两种：

- `realtime`（省略时的默认值）：使用宿主墙钟定时器。恒定、Poisson（泊松）和周期突发
  按真实到期时间注入，`lagMs` 记录最近一次调度延迟，实际执行速度会影响后续操作和结果。
- `fixed`：在同一个流量请求中启用有限虚拟时钟。`steps` 必填（1..10000000），每步把
  `virtualNow` 推进 `stepMs`（1..86400000；省略时使用 `matchIntervalMs`），不等待墙钟；
  `startTimeMs` 是虚拟 Unix 毫秒起点，省略时使用开始任务时的服务端墙钟。`matchIntervalMs` 在
  fixed 模式允许 1..86400000 毫秒，并决定哪些虚拟步执行匹配；`maxMatches` 是每次匹配
  调用的上限。

- 恒定：相邻到达间隔为 `1 / rate` 秒。
- Poisson（泊松）：间隔服从指数分布，均值为 `1 / rate` 秒，使用独立到达随机种子。
- 周期突发：每隔 `burstIntervalMs` 注入 `burstSize` 条。
- 匹配：`realtime` 每隔墙钟 `matchIntervalMs` 开始一轮；`fixed` 在虚拟时间达到下一
  `matchIntervalMs` 边界时开始一轮。`maxMatches` 是本轮所有物理节点合计成局上限，规则不允许则产出为零。

启动时复制完整生成器配置。属性生成统一调用 `GenerateBatch`，保留生成器扩展；每条 Ticket 使用属性随机流派生种子，ID 单调递增；realtime 的创建时间是实际墙钟注入时刻，fixed 的创建时间是对应虚拟到达时刻。批量配置中的 count、createdAtStart、createdAtStep 与 atomic 不控制持续流量。流量按 RuleKey 路由，不指定 PlacementID；客户端会移除所选 PlacementID。

相同属性种子与到达种子可重现属性序列与计划间隔。`realtime` 的实际调度时间、手动操作
和匹配结果不保证完全重放；计时器按下一到期时间唤醒，延迟时保留过期工作，不保证操作系统
实时性。`fixed` 使用同一套有种子的生成路径和虚拟 `createdAt`/round `now`，在相同配置、
脚本和输入下可复现逻辑时间序列；它仍不替代脚本外部资源或宿主环境的确定性保证。

固定模式不把 Python RPC、核心匹配或 Go 调度的墙钟耗时加入 `virtualNow`。这些成本会延长
整个任务的 `wallElapsedMs`，并消耗实际 CPU、内存、解释器和管道容量；`realtime` 还会体现在
`lagMs` 和后续到期工作上。两种模式的任务均属于运行状态，不写入场景，服务重启后也不会恢复。

状态显示已注入、已产出、轮数、下一 TicketID 与错误。固定模式另外显示 `step`、`totalSteps`、
`virtualNow` 和 `wallElapsedMs`，完成状态为 `completed`；墙钟模式的固定步字段省略。停止后
重启需把起始 ID 更新为下一 TicketID，或选择未使用的 ID 段。重复 ID、生成/路由/规则错误会
停止任务并保留成功前缀计数。ID 不超过 JavaScript 安全整数 `9007199254740991`；耗尽时失败，
下一 ID 显示 0。

成功替换场景和关闭模拟器都会停止旧任务；替换验证失败时旧任务继续运行。每个实例最多一个任务，重复开始会拒绝。任务属于运行状态，不保存进场景文件，不在服务重启后恢复。

HTTP 使用 `GET /api/v1/traffic` 查询、`POST /api/v1/traffic` 开始、`DELETE /api/v1/traffic` 停止。POST 为 `{ "config": <流量配置>, "generator": <CustomTicketsRequest> }`。见 [OpenAPI](../../api/openapi/simulator.yaml) 与 [配置 Schema](../../api/schema/simulator-traffic/v1.schema.json)。速率上限每秒 10000 条；realtime 匹配间隔和所有突发间隔为 100..86400000 毫秒，fixed 匹配间隔为 1..86400000 毫秒；每次匹配及突发数量上限 10000。

后台运行不限制池及事件内存总量，用户应监测状态并按需停止。Demo 模式提示需要真实服务，不伪造运行状态。

共同 Object Facts 与路由/请求前缀也沿用生成面板配置；启动后锁定输入，停止后可使用“接续编号”。参见[界面操作](interface-guide.md)。
