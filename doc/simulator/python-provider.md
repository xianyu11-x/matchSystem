# Python Provider 接入

模拟器可以在场景的 `rules[*]` 中保存一个服务端 Python Provider 绑定。绑定由
`internal/simulator` 在构造 `LogicalNode` 时启动；路径按 API 服务器文件系统解析，远程服务
不能读取浏览器本机文件。完整字段契约见
[simulator-scenario/v1 Schema](../../api/schema/simulator-scenario/v1.schema.json) 和
[OpenAPI](../../api/openapi/simulator.yaml)。

## 场景配置

`pythonProvider` 是可选对象，`script` 必填，且必须是 API 服务器上模拟器宿主可读取的
Python 文件路径。
`python` 指定解释器；显式配置优先，其次使用 `PYTHON_PROVIDER_EXECUTABLE`；未配置时 Windows
优先使用 `py -3`，其他环境探测 `python`、`python3` 等可用解释器。`runner` 指定服务端 JSON-lines runner；省略或留空时使用
内建 runner。`timeoutMs` 是每次 RPC 调用的超时，0 或省略使用 2 秒宿主默认值。

下面的完整 `rules` 条目展示了绑定与三类 Descriptor 的关系。`rule` 中的 Fact Contract
必须与三个 Descriptor 的事实逐项对应；Python 绑定不会自动生成 Descriptor，也不会把
Descriptor 当作运行时 Fact 值。

Web UI 的 scope 选择只启用对应的 `factProviderDescriptor`、
`objectFactProviderDescriptor` 或 `matchFactProviderDescriptor` 字段；它不会为每个 scope
创建独立绑定。一个 `pythonProvider` 绑定可同时实现所选 scope 的方法，且至少要保留一个
对应 Descriptor；脚本仍需按启用的 scope 提供相应 RPC 方法。

### 在 Web 界面配置并保存

1. 打开“规则”页，选中目标规则，进入“场景与部署”标签。在对应的“规则部署”条目中勾选
   “启用此 LogicalNode 的 Python Provider”。
2. 填写“Provider 脚本”，按需填写“Python 解释器”“RPC Runner”和“RPC 超时（ms）”。
   脚本路径按 API 服务器的文件系统解析；浏览器所在电脑上的路径不会被服务端读取。
3. 在“启用 Provider scope”中选择实际由脚本提供的范围。开关与场景字段的对应关系是：

   | 界面开关 | 场景 Descriptor 字段 | 脚本使用的事实范围 |
   | --- | --- | --- |
   | Tick（本轮） | `factProviderDescriptor` | 本轮 Tick Facts |
   | Object（对象） | `objectFactProviderDescriptor` | 每个 Ticket 的 Object Facts |
   | Match（成局） | `matchFactProviderDescriptor` | 成局时的 Match Facts |

   这个开关只决定对应 Descriptor 是否启用，不会为每个范围创建独立的
   `pythonProvider` 绑定或独立脚本进程。切换到“全部 Facts”标签，在“Provider 握手声明”中
   为已启用范围填写 Descriptor 的 ID、版本和事实字段；字段名、类型、scope 必须与规则
   Contract 一致。未启用的范围不应在脚本中声明为该绑定的运行职责。
4. 点击页面顶部“保存场景”。保存会提交全部规则草稿并重建运行态，成功后清空等待队列和
   Match 历史；校验或握手失败时新场景不会发布，旧场景继续运行。停用绑定或某个 scope
   也要保存场景后才会对运行中的模拟器生效。

```json
{
  "schemaVersion": "simulator-scenario/v1",
  "physicalNodes": [
    {
      "id": "simulator-1",
      "endpoint": "inproc://simulator-1",
      "enabled": true,
      "selector": "round_robin"
    }
  ],
  "rules": [
    {
      "logicalNode": {
        "rule": {"namespace": "demo", "ruleId": 1},
        "placementId": "default"
      },
      "physicalNodeId": "simulator-1",
      "weight": 1,
      "enabled": true,
      "rule": {
        "schemaVersion": "match-rule/v1",
        "ruleKey": {"namespace": "demo", "ruleId": 1},
        "contract": {
          "schemaVersion": "logical-node-contract/v3",
          "attributes": [],
          "facts": [
            {"name": "clock", "type": "int64", "scope": "tick"},
            {"name": "tier", "type": "int64", "scope": "object"},
            {"name": "members", "type": "int64", "scope": "match"}
          ],
          "indexes": []
        },
        "prefilter": {
          "schemaVersion": "prefilter/v3",
          "bitmap": {"resultType": "bitmap", "expr": {"op": "none"}}
        },
        "evaluation": {
          "schemaVersion": "evaluation/v3",
          "canJoin": {
            "schemaVersion": "expression-scalar/v3",
            "resultType": "bool",
            "expr": {"op": "bool_literal", "value": true}
          },
          "canComplete": {
            "schemaVersion": "expression-scalar/v3",
            "resultType": "bool",
            "expr": {"op": "bool_literal", "value": true}
          }
        },
        "scoring": {"type": "constant", "params": {"value": 0}},
        "seedSelection": {"type": "arrival", "params": {}},
        "runtime": {
          "candidateScoringLimitPerSeed": 500,
          "candidateLimitPerSeed": 50,
          "maxPlayers": 2,
          "attemptLimitPerProduceMatch": 500,
          "attemptLimitPerMatchRound": 500
        }
      },
      "factProviderDescriptor": {
        "id": "demo.python.tick",
        "version": "v1",
        "facts": [{"name": "clock", "type": "int64", "scope": "tick"}]
      },
      "objectFactProviderDescriptor": {
        "id": "demo.python.object",
        "version": "v1",
        "facts": [{"name": "tier", "type": "int64", "scope": "object"}]
      },
      "matchFactProviderDescriptor": {
        "id": "demo.python.match",
        "version": "v1",
        "facts": [{"name": "members", "type": "int64", "scope": "match"}]
      },
      "pythonProvider": {
        "script": "C:\\matchsystem\\providers\\demo.py",
        "timeoutMs": 2000
      }
    }
  ]
}
```

将上述对象作为 `{"scenario": <对象>}` 提交到 `PUT /api/v1/scenario`。场景替换会先
编译规则、启动并握手全部 Worker；任一绑定失败时新场景不会发布，旧场景继续运行。成功
替换后旧场景的 Worker 被关闭并回收；关闭模拟器也会回收直接启动的解释器。一个完整
`LogicalNode` 拥有一个独立 Worker，即使多个节点使用同一路径也不共享 Python 模块状态。

## 脚本与 JSON-lines 协议

内建 runner 加载 `script`，要求脚本导出 `provide(method, data)` 可调用对象。标准输出
保留给协议，脚本的普通 `print` 会被重定向到标准错误。每次请求和响应是一行 UTF-8 JSON，
版本固定为 1；字段使用 Go 的导出名：

```json
{"Version":1,"Method":"tick","Input":{"Now":1700000000000}}
{"Version":1,"Facts":{"Int64Values":{"clock":1700000000000}}}
```

启动握手使用 `handshake`，业务方法为 `tick`、`object`、`initialize` 和 `join`。`object`
的输入包含 `Ticket`、`Now`、`TickFacts`；`join` 的输入包含当前 Match Fact 快照。响应的
`Facts` 使用 `StringLists`、`Uint64Lists`、`Int64Values`，必须通过 Rule Contract 的
Fact Validator。协议帧上限为 1 MiB；解析错误、异常、超时、未知 Fact、类型/范围错误或
不完整 Match 快照都会关闭 Worker，不会自动重放或回退到内建 Provider。

最小业务脚本可以写成：

```python
def provide(method, data):
    if method == "tick":
        return {"Int64Values": {"clock": data["Now"]}}
    if method == "object":
        return {"Int64Values": {"tier": 1}}
    if method == "initialize":
        return {"Int64Values": {"members": 1}}
    if method == "join":
        before = data["MatchFactsBefore"]["Int64Values"]["members"]
        return {"Int64Values": {"members": before + 1}}
    raise ValueError("unknown method")
```

`experiments/pythonprovider/example.py` 保留了相同协议的可编辑示例；其中实验包的
`runner.py` 适合研究和回归，不改变生产场景绑定的字段契约。

## 时间、错误与容量边界

模拟器把调用方传入的 round `now` 作为本轮逻辑时间，并传给 Python 的 Tick/Object 输入。
单轮固定时间可通过 `POST /api/v1/rounds` 的 `now` 字段控制；持续流量还可在同一个
`simulator-traffic/v1` 请求中使用 `mode: fixed`、`steps`、`stepMs` 和 `startTimeMs`，
由内建调度器直接推进有限虚拟时钟。`mode: realtime` 仍由现有墙钟定时器和
`matchIntervalMs` 驱动。两种流量任务都是运行状态，不写入场景。Python 进程等待、启动、
序列化和执行不会推进逻辑时钟，但会消耗实际执行时间和服务端容量。Worker 启动/握手发生
在 runtime 构造阶段，不计入成功成局的 `processingDurationNs`；ProduceMatch 内的 Python
RPC 等待会计入该字段。固定模式的状态用 `wallElapsedMs` 报告任务墙钟成本，实时模式用
`lagMs` 报告调度延迟。匹配记录的 `durationMs` 是逻辑排队等待，`processingDurationNs`
观测成功 `ProduceMatch` 的单调墙钟耗时。大规模模拟应分别记录这些时间与 Worker 数量/
调用吞吐。

缺少 `script` 或使用负 `timeoutMs` 时，场景校验返回 `422 VALIDATION_FAILED`，路径分别为
`$.rules[i].pythonProvider.script` 和 `$.rules[i].pythonProvider.timeoutMs`。脚本文件、解释器、
runner 不可用或启动握手失败会使 `PUT /scenario` 失败并保持旧场景；当前 HTTP 适配层把这类
Provider 运行错误由 HTTP 层返回 `500 PYTHON_PROVIDER_FAILED`，并关闭发生错误的 Worker；不
自动回退或重放。Python 是宿主可信代码，当前实现
不提供沙箱、资源配额或派生进程树清理；线上容量评估必须把解释器进程和 RPC 等待纳入实际
部署预算。

相关当前行为见[Fact 数据来源](fact-sources.md)、[持续流量与定时匹配](continuous-traffic.md)、
[Match 历史](match-history.md)；研究阶段取舍见
[Python Provider 生产接入 ADR](../design-decisions/adr/python-provider-integration.md)。
