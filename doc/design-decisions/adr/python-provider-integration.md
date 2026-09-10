# ADR：统一 Provider 自描述与场景 Python 绑定

日期：2026-09-09。状态：已实施。本 ADR 记录生产模拟器接入对[研究 ADR](python-provider-research.md)
的落地取舍；研究文档保留实验基线和历史限制，不作为当前 API 说明。

## 背景

模拟器的 Tick、Object、Match Provider 需要保留每个 LogicalNode 的实现状态，同时让场景
通过 `GET/PUT /api/v1/scenario` 往返后仍使用同一个实现。浏览器不能启动或访问服务端进程，
也不能把本地脚本路径当成服务端资源。Provider 的能力描述必须由实现自己声明，不能依赖
规则拥有者在场景中复制一份容易过期的描述，也不能从 Rule Contract 反向猜测实现能力。

## 决策

1. `simulator-scenario/v1` 的每个 `rules[*]` 条目可以保存可选 `pythonProvider`：`script`
   是必填的服务端文件路径，`python` 和 `runner` 可省略或留空，`timeoutMs` 为每次调用
   上限，0/省略使用宿主默认值。场景只保存实现绑定和静态 `tickFacts`；不保存
   `factProviderDescriptor`、`objectFactProviderDescriptor` 或 `matchFactProviderDescriptor`，
   也不保存 `descriptorSource`。Schema/API 的未知字段校验会拒绝旧 JSON，不能静默保留或
   重新生效。字段契约以 [Python Provider 接入](../../simulator/python-provider.md)和
   [simulator-scenario/v1 Schema](../../../api/schema/simulator-scenario/v1.schema.json) 为准。
2. Go Provider 在 LogicalNode 加载阶段调用进程内 `Describe()`，由实现返回自身的
   `ProviderDescriptor`。这个调用不经过序列化或 RPC。Python Provider 由内建 runner 在
   JSON-lines `handshake` 中调用脚本 `describe()`，并从 `Descriptors.tick/object/match`
   取得等价描述。两种实现的结果在核心进入同一套 Provider/Contract 能力覆盖校验；Contract
   使用的每个 Fact 必须被对应 Descriptor 覆盖，名称、类型、scope 和 `maxValues` 严格匹配。
   Descriptor 可以声明额外合法能力，但能力不会加入 Contract。
3. `rule.contract.facts` 仍是规则侧唯一 Contract。任何 Provider 能力都不能从 Contract
   自动合成；启动阶段统一校验 Descriptor 覆盖关系。业务 `Facts` 的运行时校验保持边界
   语义：Go 可信 Provider 由实现和契约测试保证，Python 适配边界对每次响应执行 Validator。
   没有声明某个 scope 的 Contract Fact 时，该 scope 不要求 Provider；一旦声明，Provider
   必须存在并返回相应自描述。
4. Python JSON-lines 协议版本为 `1`。握手请求为
   `{"Version":1,"Method":"handshake","Input":null}`；成功响应包含
   `{"Version":1,"Ready":true,"Descriptors":...}`。脚本必须导出可调用的 `describe()`
   和 `provide(method, data)`；缺少 `describe()`、返回 `null`、空对象、非法 Descriptor
   或只返回 Ready-only 都使握手失败，不再有旧场景声明或 `descriptorSource` 兼容路径。
   Descriptor 的范围键为小写 `tick`、`object`、`match`，每项使用小写 `id`、`version`、
   `facts`，Fact 使用 `name`、`type`、`scope` 和可选 `maxValues`/`description`。
   上面的 `Input:null` 仅适用于公共 `StartPythonProvider(config, specs)` 通用入口；场景生产
   runtime 构造时会把 `ContractFacts`（PascalCase Fact Spec 字段，零值 `MaxValues` 省略）和
   `TickFacts` 放入握手 `Input`。内建 runner 在脚本模块加载完成后把该 Input 注入脚本全局
   `_provider_context`，随后调用零参数 `describe()`；模块加载期间不能假设该变量已存在，
   后续 `provide(method, data)` 才能读取它。
5. 场景替换采用“先构造、后交换、再关闭旧 runtime”。候选 runtime 先编译 RuleJSON，调用
   Go Provider `Describe()`、启动 Python Worker 并完成 `describe()` 握手，再执行核心
   Contract 覆盖校验；任一实现、握手或校验错误都会清理候选 runtime 并保留旧 runtime。
   成功交换或模拟器关闭时关闭并等待旧 Worker。Worker 发生异常、协议错误、Fact 越界或
   超时后进入终态，不自动重放或回退到其它 Provider。
6. `ValidateScenario` 不在只读校验中启动外部进程，也不把 Contract 当作已完成的 Provider
   握手。它只校验静态场景、RuleJSON、路径和超时；运行时的 Go `Describe()`、Python
   `describe()` 缺失或能力覆盖错误在候选 runtime 构造阶段报告。事实能力查询返回运行态
   `providerDescriptors`，每个 Descriptor 的 `source` 为 `go` 或 `python`；查询结果不会
   回写 GET/PUT Scenario。
7. `script`、`python` 和 `runner` 全部由模拟器宿主解析；内建 runner 要求脚本提供
   `provide(method, data)` 与 `describe()`。当前实现把脚本视为可信本机代码，只回收直接
   解释器，不提供沙箱、CPU/内存配额或派生进程树清理。单帧上限 1 MiB，标准错误保留有界
   诊断。
8. Python 接收调用方传入的 round `now`。该值是逻辑时间；单轮实验可通过现有
   `POST /api/v1/rounds` 请求的 `now` 字段控制，持续流量请求也支持 `mode: fixed`、
   `steps`、`stepMs` 和 `startTimeMs` 直接推进有限虚拟时钟，`mode: realtime` 保持墙钟
   调度。两种流量任务都不写入场景。Python 启动/握手发生在 runtime 构造阶段，不计入成功
   成局的 `processingDurationNs`；ProduceMatch 内的 Python RPC 等待会计入该字段。启动和
   RPC 都不推进逻辑时间，并分别影响实际部署容量、实时模式的 `lagMs` 或固定模式的
   `wallElapsedMs`。
9. 仓库同时提供 [Go 内建 Provider 的 Python 参考实现](../../../examples/simulator/builtin_provider.py)。
   它在固定目录中实现五项标准能力，`describe()` 不从 Contract 生成 Descriptor；生产 runner
   只在握手 Input 中传入当前 `ContractFacts` 和静态 `TickFacts`，业务 object 输入传入已有的
   `ObjectFacts`，脚本按 Contract 子集投影响应，最终仍由 Python 适配边界的 Validator 校验。
   `preferredRoles`/`latencyMs` 只复制 Object Facts，`waitingTime` 使用饱和的
   `max(0, Now-CreatedAt)`，`memberCount` 从 seed 的 1 开始并在溢出时报错。该脚本必须显式
   绑定到 `rules[*].pythonProvider.script`；删除绑定仍使用默认 Go Provider，默认 Go 行为不变。

## 错误与迁移

Schema/场景静态校验在缺少 `script`、`timeoutMs < 0` 或未知场景属性时返回
`422 VALIDATION_FAILED`。旧 JSON 中的三个 Descriptor 字段和 `pythonProvider.descriptorSource`
必须删除；它们不会被转换为新的自描述配置。脚本文件、解释器、runner 不可用、Python
握手没有 `describe()`/`Descriptors`、Go `Describe()` 返回无效描述，或任一 Descriptor 与
Contract 不匹配时，场景替换失败且旧场景不变；HTTP 适配层对 Provider 运行或握手错误返回
结构化的 `500 PYTHON_PROVIDER_FAILED`（Go Provider 错误沿对应 API 错误边界返回）。客户端
应依赖 OpenAPI 声明的错误码，不应依赖 Go 错误字符串。

此前验收记录中的 Ready-only、场景显式 Descriptor、`descriptorSource=script/scenario` 和
`source=script/scenario` 是旧基线描述；它们保留在历史记录中用于解释当时证据，但不再代表
当前行为。当前运行态来源只有 `go` 和 `python`。

## 取舍与影响

由 Provider 自描述避免实现能力和场景副本漂移，并使 Go 与 Python 在核心拥有同一 Contract
覆盖语义；代价是所有需要 Fact 的实现都必须更新为自描述 Provider，旧场景和 Ready-only
脚本需要迁移。Go 自描述保持进程内调用的低成本，Python 仍需常驻解释器和管道，慢脚本会
阻塞物理节点 owner。大规模模拟必须分别测量 Provider RPC、核心匹配和调度器的实际耗时，
不能用固定逻辑时间掩盖线上吞吐或容量不足。需要批量预取、沙箱、进程树治理或跨节点远程
Provider 时，应另建 ADR，不能把本地 JSON-lines 绑定解释为线上分布式协议。
