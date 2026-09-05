# ADR：Python Provider 采用宿主进程适配器开展研究

日期：2026-09-06。状态：研究结论，非产品发布。基线 `f999a51fe7bb4a137b69c7cdc234d4ae1230bc58`，
独立分支 `codex/python-provider-research`，不合并前五项增强分支。

## 结论与范围

现有 Tick/Object/Match Provider 可由 Go 宿主适配到 Python，无需修改匹配核心。
本次只增加 [experiments/pythonprovider](../../../experiments/pythonprovider/README.md)，
通过真实模拟器 `RunRound` 证明三类脚本回调可参加成局。脚本修改后显式替换 Worker 可生效，
无需重编 Go；现有客户端、HTTP、场景 JSON **尚不支持选择和运行 Python 脚本**。

## 源码调用路径

1. [RuleSpec](../../../internal/simulator/types.go) 的函数/接口依赖由宿主注入，
   [scenario_json.go](../../../internal/simulator/scenario_json.go) 的 `ruleSpecJSON` 只持久化
   TickFacts 与 Descriptor 等声明，不保存 Go 回调，更没有脚本路径。
2. [service.go](../../../internal/simulator/service.go) 构造 runtime 时编译 RuleJSON、创建
   Fact Validator，`runtimeLogicalNodeSpec` 选择宿主 Provider 或内建默认并包裹 validating
   wrappers（校验包装器），传入 `adapter.Load`。
3. [logical_node.go](../../../internal/matchsystem/logical_node.go) 的 `NewLogicalNode` 按三个
   scope 校验 Provider 与 Descriptor 握手；`ProduceMatch` 创建本节点快照交给
   [seed_evaluator.go](../../../internal/matchsystem/seed_evaluator.go) 的 `BeginSession`。
4. Tick 在每次 ProduceMatch 的会话中同步取值，并非每个 RunRound 只取一次。Object 通过
   [Frame.Object](../../../internal/matchsystem/fact/frame.go) 和
   [ObjectSlot.ensure](../../../internal/matchsystem/fact/object_slot.go) 按 Ticket/生成号惰性刷新；
   同次 ProduceMatch 缓存结果，跨次刷新。
5. `initializeMatchFacts` 以 seed 初始化完整 Match 层；`onJoinMatchFacts` 输入候选和
   `MatchFactsBefore`，返回完整新层，再由评价逻辑判断完成。输入 Ticket/Facts 已复制。
   Match 是完整替换，不能返回局部 patch；本次直接复用
   [ValidateCompleteMatch](../../../internal/matchsystem/fact/validator.go)。

核心把同仓库 Provider 视为可信，不保证每次快照全量校验。Python 属于外部数据边界，
故本适配器对每次返回额外校验，直接注入 LogicalNode 也不绕过类型、scope、maxValues、
未声明字段和完整 Match 检查。Descriptor 由示例宿主独立提供，未从 Rule Contract 自动推导。

## 三类契约

| 回调 | 输入 | 输出与生命周期 |
| --- | --- | --- |
| Tick | Now、Node.Key/State/WaitingCount | Tick Facts；可省略字段，缺失语义仍由规则决定；ctx 限制加适配器上限 |
| Object | Ticket 全部属性、Now、TickFacts | 先完整解码/校验再写 schema-bound Writer；不保留借用对象；接口无 ctx，适配器自设超时 |
| Match.Initialize | Now、SeedAttributes、SeedFacts、TickFacts | 完整 Match Facts，缺任一声明字段失败 |
| Match.OnJoin | 上述字段加 Candidate、CandidateFacts、MatchFactsBefore | 完整下一状态；聚合状态应由输入显式传递，避免失败重试产生脚本副作用 |

## 方案比较与选择依据

| 方案 | 优点 | 代价/结论 |
| --- | --- | --- |
| 每回调启动 Python | 实现简单、总读新脚本 | 本机启动加首调约 33 ms；逐候选使用不可取 |
| 每节点常驻进程、管道 JSON RPC | 无核心依赖；Python 崩溃可隔离；可强制终止直接解释器 | 有序列化和系统调用成本；选为原型，显式轮次边界重启 |
| CPython 嵌入 Go/CGO | 可减少管道传输，能复用解释器 | Python C ABI、发行包/DLL、引用计数、线程状态和 GIL 管理；卡死/原生崩溃影响宿主，缺少安全强杀边界；未实现或测量 |
| 独立网络 RPC 服务 | 可远端部署、复用运维能力 | 额外传输、认证、隔离路由及重试语义；本次不需要网络，未测量 |

Python 官方说明 int 是任意精度，JSON 默认整数字面量使用 int；Go 必须直接解到有类型字段，
不能经过 `map[string]any` 的 float64。依据：[Python int](https://docs.python.org/3/library/stdtypes.html#numeric-types-int-float-complex)、
[Python JSON](https://docs.python.org/3/library/json.html)。本研究的精度结论同时有边界值实验，
不是把 JSON 一概当成无损协议。

嵌入方案参考 [CPython embedding](https://docs.python.org/3/extending/embedding.html) 与
[线程状态/GIL](https://docs.python.org/3/c-api/init.html#thread-state-and-the-global-interpreter-lock)。
进程控制依据 [Go os/exec](https://pkg.go.dev/os/exec)：直接启动可执行文件不用 shell；
Process.Kill 不等于进程树沙箱。原型显式关闭管道、Kill、Wait，并在超时分支等 I/O goroutine
结束。测试证据仅覆盖受控直接 Python 解释器，不承诺任意操作系统故障下的严格实时截止。

## 隔离、失败和加载边界

- 每 LogicalNode 独立 Worker/解释器全局状态，绝不共享缓存、脚本 globals 或 Match 状态。
  物理节点执行线程仍是同步串行，某节点慢调用会延迟该物理节点上的其他逻辑节点，
  进程隔离不能消除这种调度影响。脚本共享文件等外部资源也不受此隔离保证。
- 任一脚本异常、协议错误、类型越界或超时使 Worker 关闭；不使用空 Facts 兜底，不自动重放。
  直接解释器已回收；无限循环/阻塞回调不会仅在 Go 返回后继续在该解释器运行。
- Start 只启动进程，导入/语法错误首次调用才暴露，没有能力协商、脚本哈希、健康握手或
  原子热替换。当前 stderr 丢弃，未来需有界诊断。协议也不对恶意重复 JSON 键做专项防御。
- 可信脚本可以访问宿主权限内的文件/网络并创建子进程。原型只回收直接解释器，未提供
  Windows Job Object、进程组、权限沙箱、内存/CPU 配额、孤儿进程恢复或宿主崩溃回收。
- 每节点一个进程的内存随节点数增加；本次未测驻留内存或多节点并发负载，不能宣称支持
  成千上万个 Python 节点。Worker 必须 owner-only，Close 不能与另一调用并发。

## 未来模拟器产品化路径

1. 在模拟器宿主配置引入版本化 `providerBinding`：实现类型、脚本资源 ID/路径、解释器、
   超时、scope 与独立 Descriptor；HTTP 校验可选资源，UI 提供明确选择/测试/错误显示。
   浏览器不能把用户本地路径直接当作服务端路径，需要上传或受控脚本目录机制。
2. 在构造 runtime 时为每个完整 LogicalNodeKey 建立 Worker，握手验证协议、能力、脚本
   内容版本和 Contract 后再绑定回调；节点加载失败须关闭所有已启动候选 Worker。
3. `ReplaceScenario` 会重建 runtime。需将 Worker 所有权纳入 runtime，先完整构造验证候选，
   在暂停/轮次边界原子发布，再关闭旧 runtime 的 Worker；失败保留旧场景并清理候选。
   场景导入导出必须保留 binding，不能只保留 Descriptor 后悄悄恢复内建 Provider。
4. 停止持续流量、关闭模拟器、卸载节点、解释器失败、应用退出均明确关闭 Worker；
   产品允许脚本派生进程前补齐平台进程树策略。健康失败应可见且停止受影响匹配，不静默回退。
5. 根据实测候选量设计批量 Object 数据预取/快照更新、宿主缓存有效期与背压，遵守现有
   每次 ProduceMatch 一致性。不能擅自跨轮缓存或并发重入核心。UI 上明确性能预算与错误状态。

这条路径需要修改模拟器和客户端宿主代码，但业务 Provider 后续可只迭代脚本；核心仍保持 Go。
实验与限制见[验证记录](../testing/python-provider-research.md)。
