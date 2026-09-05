# Python Provider 研究实验记录

日期：2026-09-06；基线 `f999a51fe7bb4a137b69c7cdc234d4ae1230bc58`。
Windows amd64，Intel Core Ultra 7 265K，Go 1.26.5，Python 3.14.5。
代码与复现入口：[独立实验包](../../../experiments/pythonprovider/README.md)。
设计分析：[ADR](../adr/python-provider-research.md)。

## 复现

在仓库根目录运行（测试进程会把工作目录设为包目录，自动找到 runner.py/example.py）：

```powershell
python --version
go version
go test ./experiments/pythonprovider -count=1 -v
go test ./experiments/pythonprovider -run '^$' -bench . -benchmem -benchtime=2s
go test ./... -count=1
go vet ./experiments/pythonprovider
```

当前结果全部通过，没有 Python SKIP。未运行 race detector；owner-only 契约及进程隔离
实验不能替代竞态检测。未修改核心、模拟器运行时代码或客户端 UI。

## 功能证据

| 测试 | 观察/断言 |
| --- | --- |
| TestPrecisionAndThreeContracts | MinInt64、MaxInt64、2^53+1 Tick 往返精确；Object 的 MaxUint64 和中文列表正确；同 Frame 二次读缓存；Match 1→2 |
| TestLogicalNodesProduceMatch | 两个 Placement 各自独享 Worker，重复使用相同 TicketID 各成两人局；成员数 2 和 MaxUint64 保存在成局快照 |
| TestSimulatorRunRound | NewSimulator → RuleSpec → validating wrappers → RunRound 成一局，成员数 2；成员 Object Facts 中保存 18446744073709551615、9007199254740993 |
| TestExplicitReloadAndProcessIsolation | 同源脚本不同 PID，A 连续读 1、2，B 首读仍 1；改脚本后旧 B 仍读 2；关闭/重新启动 A 读 99 |
| TestErrorsAndReaping | 异常、退出码 7、非法 JSON、int64/uint64 溢出、负 uint64、小数、错误类型/scope、未声明字段、超帧、Match 不完整、超 maxValues 均失败关闭；ProcessState 非空，后续调用拒绝 |
| TestTimeoutAndCancellation | 预热后脚本 sleep(60)，50 ms 上限约 51.86 ms 返回，10 ms 主动取消约 12.20 ms 返回；解释器均已 Wait 回收 |
| TestObjectTimeoutReaps | 真正通过 Frame.Object 调用无 ctx 的 Object 回调，约 52.35 ms 返回 DeadlineExceeded，直接解释器已回收 |

超时分支关闭管道后还等待内部 goroutine 的 done；上述超时实验同时证明受测路径没有
卡在等待 done，并检查直接进程已 Wait。它不证明派生子进程清理、恶意脚本沙箱、跨平台行为。
Python 脚本加载的语法/导入错误在第一次调用而非 Start 时发现，尚无产品健康握手。

## 调用成本与规模限制

`-benchtime=2s` 原始基准摘要：

```text
BenchmarkWarmRPC-20             90201       24265 ns/op     2710 B/op   36 allocs/op
BenchmarkNativeCallback-20   36250545          65.15 ns/op    256 B/op    2 allocs/op
```

WarmRPC 包含请求序列化、管道往返、Python JSON/业务函数、响应解码、校验、超时定时器；
NativeCallback 是相同 Tick 小 map 的纯 Go 示例，不含 RPC 校验，不能把约 372 倍差值
当成整个匹配系统的性能倍数。`-20` 是本机 GOMAXPROCS，不代表 20 个并行 Worker。

启动到首次成功调用约 32.55 ms。另用每档 300 次 Initialize 请求测得：

| 输入额外字符串 | 样本均值 | 输出大小 |
| --- | --- | --- |
| 0 B | 32.37 μs | 单个 members 字段 |
| 4 KiB | 34.43 μs | 同上 |
| 64 KiB | 109.15 μs | 同上 |

本机极短调用的 time.Now 差值大量为 0；测试输出的 P50=0、P95 约 0.51–1 ms 带有时钟
量化影响，**不据此承诺分位延迟**。吞吐判断优先采用多次调用总耗时的 benchmark。
这不是双向大列表、复杂算法、网络访问、多节点竞争或真实业务吞吐测量。

估算一个成功局的额外耗时为 `T_tick + U*T_object + A*T_initialize + J*T_join`，其中
U 是本次访问的不同 Ticket 数，A 是实际尝试的 seed 数，J 是实际加入次数；失败和再次
ProduceMatch 会增加总调用次数。按 24.3 μs 的小包基准粗算，1000 次调用约 24 ms，
10000 次约 243 ms；64 KiB 输入按 109 μs 则约 109 ms/1000 次。这些是线性预算估算，
不是实测千票/万票成局 SLA。单节点理论串行调用上界约 4.1 万次/秒，还未扣除匹配工作。

因此当前适合低频 Tick、低候选量、小规模交互实验；不能据此承诺高频逐票扫描或大规模
生产模拟。若每轮预算 10 ms，小包 RPC 的纯传输预算也只有约 412 次，实际应更少。
每节点一个解释器的启动成本、常驻内存及物理节点串行阻塞仍限制规模。未来需在宿主做
有一致性定义的批量预取/快照，另测实际候选量和节点数；本研究未实现该优化。

## 交付边界

- 脚本接入已在 Go 装配和模拟器 RunRound 验证；HTTP/UI 和场景持久化脚本绑定尚未实现。
- Python 为本机外部依赖；可用环境变量选择虚拟环境中的 python.exe，原型不安装依赖。
- 当前仅验证单文件 Provider。runner 的 sys.path[0] 是 runner 所在目录，不会自动加入
  外部脚本父目录；相邻 helper 模块、包相对导入、venv/模块路径需产品化另行定义。
  Start 的相对 runner/script 路径按 Go 宿主工作目录解析，正式宿主应规范化绝对路径。
- 无自动文件监听或原子热更新，脚本修改后需显式替换进程；不能在局中改变业务版本。
- 进程清理覆盖直接解释器，不覆盖脚本派生进程树；无沙箱/配额/崩溃恢复。
- 本次仅提交研究分支，不推送，不把研究原型纳入当前产品支持声明。
