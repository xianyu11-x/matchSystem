# 匹配 DSL JSON Schema

本目录属于[匹配系统文档](../../doc/match-system/README.md)的机器可读契约入口；
面向配置作者的参数说明见[参数明细](../../doc/match-system/parameters.md)。

本目录保存给编辑器和 API tooling 使用的 JSON Schema 2020-12 文档：

- `match-rule/v1.schema.json`
- `logical-node-contract/v3.schema.json`
- `expression-scalar/v3.schema.json`
- `prefilter/v3.schema.json`
- `evaluation/v3.schema.json`
- `simulator-scenario/v1.schema.json`

`match-rule/v1.schema.json` 是一份规则的统一配置入口，使用 `match-rule/v1` 作为
顶层版本，并在同一文件中关联 `ruleKey`、Contract、Prefilter、Evaluation、Scoring、
Seed Selection 和运行时参数。Contract、Prefilter 和 Evaluation 的内部结构继续复用
对应的 v3 schema。

Schema 负责 JSON 形状、字段和基础类型的快速校验。以下语义仍只能由 Go 生产编译器
最终确认：Contract 内名称唯一性、Attribute/Fact 引用与 scope、index 绑定、Prefilter
scope lattice、复杂度预算和运行时 provider 绑定。

因此客户端的保存流程必须同时满足：

1. 本地 Ajv 校验通过；
2. 图编辑器的端口、基数和无环检查通过；
3. `POST /api/v1/rules/validate` 返回 `valid: true`。

Schema 只描述当前的 `match-rule/v1` 及其引用的 v3 子格式，不兼容
`doc/design-decisions/archive/` 中的旧格式。

- [attribute-generators/v1](attribute-generators/v1.schema.json)：批量属性生成配置；整数边界使用字符串。
- [simulator-traffic/v1](simulator-traffic/v1.schema.json)：持续流量配置；`mode=realtime` 使用墙钟，`mode=fixed` 使用 `steps`/`stepMs`/`startTimeMs` 推进有限虚拟时钟，运行状态不写入场景。

`simulator-scenario/v1.schema.json` 的 `rules[*].pythonProvider` 是可选的服务端 Python
JSON-lines Worker 绑定；其 `script` 必填且按 API 服务器文件系统解析，`python`/`runner` 可省略
（或留空）以使用宿主发现/内建 runner，`timeoutMs` 为每次调用的超时毫秒，0 使用宿主默认值。
Provider 能力描述来自启动自描述，场景只保存实现绑定和静态 `tickFacts`，不保存
`factProviderDescriptor`、`objectFactProviderDescriptor` 或 `matchFactProviderDescriptor`。
旧场景若仍包含这些字段，因 Schema 的 `additionalProperties: false` 应被拒绝，不能静默当作有效
配置。详细运行时语义见[模拟器 Fact 数据来源](../../doc/simulator/fact-sources.md)和
[Python Provider 示例](../../doc/simulator/python-provider.md)。

Python Provider 的 JSON-lines 协议版本仍为 `1`。握手请求沿用
`{"Version":1,"Method":"handshake","Input":null}`；内建 runner 调用脚本的
`describe()` 并返回 `{"Version":1,"Ready":true,"Descriptors":{"tick":...,"object":...,"match":...}}`。
每个 Descriptor 使用 `id`、`version`、`facts`，Fact 使用 `name`、`type`、`scope`、可选
`maxValues`/`description`。需要由 Provider 提供的每个 Contract scope 必须在握手描述中出现；
Ready-only 响应不能借助场景声明补齐能力。业务响应仍使用 PascalCase 的 `Facts` 值映射，且由
Rule Contract 独立校验；Descriptor 额外声明的合法能力不会扩展 Contract 可返回的字段。
`Descriptors` 的 `tick`、`object`、`match` 键按脚本能力可选，但至少应声明一个范围；缺少
`describe()`、返回 `null`、空对象或非法 Descriptor 时握手失败，不会退回场景字段。
Go Provider 也在加载阶段通过进程内 `Describe()` 返回自身 `ProviderDescriptor`；核心只使用
自描述结果校验 Contract，不会从 Contract 反向合成 Provider 能力。`GET /api/v1/logical-nodes/facts` 的运行态
`providerDescriptors.*.source` 使用 `go` 或 `python` 标识实现来源；该元数据同样不会回写场景。
