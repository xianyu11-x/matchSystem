# ADR：场景持久化的 Python Provider 进程绑定

日期：2026-09-09。状态：已实施，接入验收通过；发布前仍需按[验收记录](../testing/python-provider-integration.md)复核发布环境。
本 ADR 记录生产模拟器接入对[研究 ADR](python-provider-research.md)的落地取舍；研究文档保留
实验基线和历史限制，不再作为当前 API 说明。

## 背景

模拟器的 Tick、Object、Match Provider 需要保留每个 LogicalNode 的脚本状态，同时让
场景通过 `GET/PUT /api/v1/scenario` 往返后仍使用同一个实现。浏览器不能启动或访问服务端
进程，也不能把本地脚本路径当成服务端资源。Provider 的调用成本还必须和固定 round 时间、
成局等待时间及服务端容量区分开。

## 决策

1. 在 `simulator-scenario/v1` 的每个 `rules[*]` 条目中增加可选 `pythonProvider`：
   `script` 是必填服务端文件路径，`python` 和 `runner` 可省略或留空，`timeoutMs` 为每次
   调用上限，0/省略使用 2 秒默认值。场景序列化会保留该对象，回读不会把它降级为内建
   Provider。JSON Schema、HTTP 组件和示例以
   [Python Provider 接入](../../simulator/python-provider.md)为准。
2. runtime 构造时为每个完整 LogicalNode 启动一个独立 Worker，并在发布前完成
   `handshake`。Worker 通过一行一请求的 v1 JSON-lines 协议适配 `tick`、`object`、
   `initialize` 和 `join`；返回值按 Rule Contract 校验。Descriptor 仍是独立的启动声明，
   Python 绑定不会自动生成或替代 Descriptor。
3. 场景替换采用“先构造、后交换、再关闭旧 runtime”。任一脚本、解释器、runner、握手或
   Descriptor 错误都会清理候选 runtime 并保留旧 runtime；成功交换或模拟器关闭时关闭并
   等待旧 Worker。Worker 发生异常、协议错误、Fact 越界或超时后进入终态，不自动重放或
   回退到 Go Provider，下一次成功调用需要新的场景/runtime。
4. `script`、`python` 和 `runner` 全部由模拟器宿主解析；内建 runner 要求脚本提供
   `provide(method, data)`。当前实现把脚本视为可信本机代码，只回收直接解释器，不提供
   沙箱、CPU/内存配额或派生进程树清理。单帧上限 1 MiB，标准错误保留有界诊断。
5. Python 接收调用方传入的 round `now`。该值是逻辑时间；单轮实验可通过现有
   `POST /api/v1/rounds` 请求的 `now` 字段控制，持续流量请求也支持 `mode: fixed`、
   `steps`、`stepMs` 和 `startTimeMs` 直接推进有限虚拟时钟，`mode: realtime` 保持墙钟
   调度。两种流量任务都不写入场景。Python 启动/握手发生在 runtime 构造阶段，不计入成功
   成局的 `processingDurationNs`；ProduceMatch 内的 Python RPC 等待会计入该字段。启动和
   RPC 都不推进逻辑时间，并分别影响实际部署容量、实时模式的 `lagMs` 或固定模式的
   `wallElapsedMs`。

## 错误与兼容性

Schema/场景校验在缺少 `script` 或 `timeoutMs < 0` 时返回 `422 VALIDATION_FAILED`，并带有
`$.rules[i].pythonProvider.*` 路径。脚本文件、解释器、runner 不可用或启动握手失败时，
场景替换失败且旧场景不变；当前 HTTP 适配层对未映射的 Provider 运行错误返回通用
`500 PYTHON_PROVIDER_FAILED`。客户端应依赖 OpenAPI 声明的错误码，而不能依赖 Go 错误字符串。

没有 `pythonProvider` 的旧场景继续使用原有 Go/内建 Provider；没有新增流量任务持久化字段。
同一个脚本路径被多个 LogicalNode 引用时仍创建多个进程，因而不共享模块全局状态。

## 取舍与影响

常驻进程避免逐候选启动解释器，且能在握手和调用边界隔离失败；代价是每个 LogicalNode
占用解释器和管道，物理节点 owner 串行调用仍会受到慢脚本阻塞。大规模模拟必须分别测量
Provider RPC、核心匹配和调度器的实际耗时，不能用固定逻辑时间掩盖线上吞吐或容量不足。
需要批量预取、沙箱、进程树治理或跨节点远程 Provider 时，应另建 ADR，不能把本地 JSON-lines
绑定解释为线上分布式协议。
