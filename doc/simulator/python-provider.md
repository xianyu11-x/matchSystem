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
Provider 能力描述只来自启动握手。内建 runner 调用脚本必需的 `describe()`，并把实际能力
范围的 `Descriptors` 放入握手响应；场景不保存任何 Provider Descriptor。
`Descriptors` 的 `tick`、`object`、`match` 键按脚本实际能力可选，但规则 Contract 使用的每个
scope 都必须在握手描述中出现。未使用的范围可以继续作为能力目录公开，不会因此扩大业务
Contract。

这里有两层连续的启动过程：第一层是 Python Worker 的 JSON-lines `handshake`，取得
`Ready` 和脚本返回的 `Descriptors`；第二层是现有 `LogicalNode` 的 Provider/Contract
能力覆盖校验，宿主把第一层得到的声明交给核心，按名称、类型、scope 和 `maxValues` 校验
Contract 覆盖关系。两层都成功后才发布 runtime；任何一层失败都不会从 Contract 合成声明、
修改 Contract 或替换旧 runtime。Go Provider 也在加载阶段通过进程内 `Describe()` 返回
`ProviderDescriptor`，
其运行态来源在 Facts 查询中标记为 `source: "go"`。

不再区分脚本来源模式，也不需要在场景或表单中手填 Descriptor。`rule.contract.facts`
仍是匹配规则的唯一 Fact Contract；脚本 Descriptor 必须覆盖 Contract 中对应 scope 的
每个 Fact，且名称、类型、scope、`maxValues` 一致。Descriptor 可以声明额外的合法 Fact，
但额外声明不会写入 Contract，也不会允许业务响应返回 Contract 未声明的 Fact。
业务 `Facts` 每次仍由现有 Contract Validator 独立校验。

`describe()` 是必需导出：脚本没有该函数、函数不可调用、抛出异常、返回 `None`/`null`、
空对象或非法 Descriptor 时，握手失败并关闭 Worker；不会降级为 Ready-only，也不会读取
场景字段补齐。有效 `Descriptors` 只在这次启动握手取得，后续业务 RPC 不能热更新它。

Web UI 只编辑 `pythonProvider` 的实现绑定和超时；scope 与能力列表由启动握手返回，并在
Facts 页只读展示。一个 `pythonProvider` 绑定仍由同一个 Worker 提供各 scope 的 RPC 方法。
旧 JSON 中的三个场景 Descriptor 字段及 `descriptorSource` 都不再属于
`simulator-scenario/v1`，Schema/API 会拒绝它们，不能通过再次 PUT 静默保留。

### 在 Web 界面配置并保存

1. 打开“规则”页，选中目标规则，进入“场景与部署”标签。在对应的“规则部署”条目中勾选
   “启用此 LogicalNode 的 Python Provider”。
2. 填写“Provider 脚本”，按需填写“Python 解释器”“RPC Runner”和“RPC 超时（ms）”。
   脚本路径按 API 服务器的文件系统解析；浏览器所在电脑上的路径不会被服务端读取。
3. 启用绑定后，Worker 必须在启动握手中通过 `describe()` 按实际能力返回相应的
   Tick/Object/Match Descriptor，且覆盖 Contract 使用的每个 scope；不需要也不能填写 Descriptor。
   `/api/v1/logical-nodes/facts` 的
   `providerDescriptors.tick/object/match` 展示实际运行态结果，`source` 为 `python`。
   脚本可以公开 Contract 当前未使用的合法 scope/Fact 能力；这些能力不会改变当前规则
   Contract 或业务响应校验。Ready-only custom runner 不能再通过场景声明补齐能力。
4. 点击页面顶部“保存场景”。保存会提交全部规则草稿并重建运行态，成功后清空等待队列和
   Match 历史；校验或握手失败时新场景不会发布，旧场景继续运行。停用绑定也要保存场景后
   才会对运行中的模拟器生效。

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
      "pythonProvider": {
        "script": "C:\\matchsystem\\providers\\demo.py",
        "timeoutMs": 2000
      }
    }
  ]
}
```

将上述对象作为 `{"scenario": <对象>}` 提交到 `PUT /api/v1/scenario`。场景替换会先
编译 RuleJSON，启动每个 Worker 并完成握手/Descriptor 校验，再把有效 Descriptor 交给
现有 Contract 能力覆盖校验；任一绑定失败时新场景不会发布，旧场景继续运行。成功替换后
旧场景的 Worker 被关闭并回收；关闭模拟器也会回收直接启动的解释器。一个完整
`LogicalNode` 拥有一个独立 Worker，即使多个节点使用同一路径也不共享 Python 模块状态。

Python Provider 的最小 `describe()` 返回值如下。返回对象的键只允许 `tick`、`object`、`match`；
每个范围的 Descriptor 使用小写 `id`、`version`、`facts`，Fact 使用小写 `name`、`type`、
`scope`，`strings`/`uint64s` 必须带正数 `maxValues`，`int64` 不带 `maxValues`：

```python
def describe():
    return {
        "tick": {
            "id": "demo.python.tick",
            "version": "v1",
            "facts": [
                {"name": "clock", "type": "int64", "scope": "tick"}
            ],
        },
        "object": {
            "id": "demo.python.object",
            "version": "v1",
            "facts": [
                {"name": "tier", "type": "int64", "scope": "object"}
            ],
        },
        "match": {
            "id": "demo.python.match",
            "version": "v1",
            "facts": [
                {"name": "members", "type": "int64", "scope": "match"}
            ],
        },
    }
```

`describe()` 只在启动握手时调用一次，不能通过后续业务 RPC 热更新 Descriptor。有效
Descriptor 会在 `GET /api/v1/logical-nodes/facts` 的 `providerDescriptors` 中展示，例如：

```json
{
  "providerDescriptors": {
    "tick": {
      "id": "demo.python.tick",
      "version": "v1",
      "source": "python",
      "facts": [{"name": "clock", "type": "int64", "scope": "tick"}]
    }
  }
}
```

`source` 是运行态元数据，不是 `simulator-scenario/v1` 的可写字段；Python Provider 返回
`source: "python"`，Go Provider 返回 `source: "go"`。`GET /scenario` 和成功的
`PUT /scenario` 响应只回传原始绑定，不会把 `providerDescriptors` 回填到场景。客户端再次
PUT 时只需保留 `pythonProvider`，不要把查询结果拼回场景 JSON。

## 脚本与 JSON-lines 协议

内建 runner 加载 `script`，要求脚本导出 `provide(method, data)` 可调用对象。标准输出
保留给协议，脚本的普通 `print` 会被重定向到标准错误。每次请求和响应是一行 UTF-8 JSON，
版本固定为 1；字段使用 Go 的导出名：

```json
{"Version":1,"Method":"tick","Input":{"Now":1700000000000}}
{"Version":1,"Facts":{"Int64Values":{"clock":1700000000000}}}
```

启动握手使用 `handshake`，业务方法为 `tick`、`object`、`initialize` 和 `join`。请求与
响应的协议版本是整数 `1`，握手请求实际为：

```json
{"Version":1,"Method":"handshake","Input":null}
```

Python Worker 的握手响应实际为：

```json
{
  "Version": 1,
  "Ready": true,
  "Descriptors": {
    "tick": {"id": "demo.python.tick", "version": "v1", "facts": [{"name": "clock", "type": "int64", "scope": "tick"}]},
    "object": {"id": "demo.python.object", "version": "v1", "facts": [{"name": "tier", "type": "int64", "scope": "object"}]},
    "match": {"id": "demo.python.match", "version": "v1", "facts": [{"name": "members", "type": "int64", "scope": "match"}]}
  }
}
```

顶层握手字段使用 `Version`、`Ready`、`Descriptors`、`Error`；`Descriptors` 内部的范围、
Descriptor 和 Fact 字段使用小写 JSON 名称。没有 `describe()`、没有 `Descriptors`、返回空
范围或非法描述的脚本/custom runner 会握手失败；不能只返回 `{"Version":1,"Ready":true}`，
也不能只返回业务 `Facts` 伪装成握手。自定义 runner 必须原样遵守这套协议版本和字段。

`object` 的输入包含 `Ticket`、`Now`、`TickFacts`；`join` 的输入包含当前 Match Fact
快照。业务响应的 `Facts` 使用 `StringLists`、`Uint64Lists`、`Int64Values`，每次都必须
通过 Rule Contract 的 Fact Validator；Provider Descriptor 的额外能力不会扩大这个允许
集合。协议帧上限为 1 MiB；解析错误、异常、超时、未知 Fact、类型/范围错误或不完整 Match
快照都会关闭 Worker，不会自动重放或回退到内建 Provider。

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
