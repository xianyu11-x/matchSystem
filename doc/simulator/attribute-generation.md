# 属性生成配置

批量生成界面逐属性启用配置，选择分布、抽取数量和重复策略；Web 与 Desktop 使用同一界面。演示模式提示连接服务，不伪造抽样结果。

`POST /api/v1/tickets/custom` 与 `/api/v1/tickets/batch` 的生成模式均接受 `attributeGenerators`，Go 使用 `BatchGeneratorSpec.AttributeGenerators`。旧 `stringChoices`、`uint64Choices`、`int64Ranges` 调用保持兼容；同一属性的新旧生成配置不可同时出现，新配置覆盖模板固定值。未配置属性保持模板值或缺省为空。

```json
{"attributeGenerators":{"level":{"type":"int64","min":"-100","max":"100","distribution":"triangular"},"regions":{"type":"strings","values":["east","west","central"],"count":2,"distribution":"low"},"ids":{"type":"uint64s","set":"1-100,200-400,18446744073709551615","count":5}}}
```

- `int64` 的 `min/max` 为十进制字符串，支持完整 int64 闭区间；`uint64s.set` 为逗号分隔的单值或闭区间，支持完整 uint64 域。客户端不转换为 Number。区间排序、合并重叠与相邻部分，只存端点，以任意精度整数计算容量，绝不展开巨大区间。
- 字符串 `values` 按首次出现顺序去重；uint64 按数值升序。默认 `count=1`，多值可配 0–4096（单属性单 Ticket 资源上限）；0 生成空列表。默认不放回，数量超过集合去重后容量报错；`replacement=true` 允许重复及超容量抽样。
- `uniform`（默认）取一个均匀随机序号；`low`/`high` 取两个独立均匀序号的最小/最大值；`triangular` 取两序号平均值向下取整，集中在中部。int64 序号映射至闭区间；多值抽样映射至候选顺序。不放回时每次在**剩余候选的有序序号**上重新应用分布，已抽值排除；它不等同于固定权重抽样。无需拒绝重试，也不会因抽满集合死循环。
- 同样配置、种子、起始 ID 及时间参数可重放；字段按名称排序。格式错误、未知分布与冲突在生成前拒绝。插入还须满足所选 Contract 的 maxValues 等属性限制。HTTP Ticket ID 仍遵循已有安全整数限制。

契约：[JSON Schema](../../api/schema/attribute-generators/v1.schema.json)、[OpenAPI](../../api/openapi/simulator.yaml)。Go 生成和存储保留完整 64 位整数。Ticket 观察响应中，超出 JavaScript 安全整数范围的 uint64/int64 属性以十进制字符串输出，安全范围仍输出数字，客户端保留文本显示。单 Ticket 输入继续使用原数值契约；Fact 数值分析保持排除非安全数字的既有策略。

## Ticket ID 与共享值

启用属性后，“值来源”可选 `ticketId`（当前 Ticket ID）或 `shared`（共享另一属性）。共享下拉框只展示已经启用的同类型属性，后端仍独立检查来源存在、类型一致和无环。来源删除后需要重新选择，否则提交会被拒绝。

```json
{"attributeGenerators":{"id":{"type":"uint64s","source":"ticketId"},"sameId":{"type":"uint64s","source":"shared","ref":"id"},"level":{"type":"int64","min":"1","max":"100"},"levelCopy":{"type":"int64","source":"shared","ref":"level"}}}
```

`ticketId` 将实际生成 ID 转为字符串、uint64 单元素列表或 int64 标量；int64 目标超出范围时整批生成前报错。`shared` 每条 Ticket 复制来源的完整值（包括多值列表），不消耗随机数，且列表内存独立；多个属性指向同一来源即可保持一致，也可引用另一个共享属性。字段按名称遍历并先生成依赖，声明顺序不影响结果。引用自身、循环、缺失引用和类型不符均拒绝；不支持跨类型隐式转换。两种派生来源不能同时填写 count、distribution、集合或边界等抽样参数。

字符串候选集合使用逐项输入、添加和删除，不使用逗号文本拆分；逗号、空字符串和前后空白均作为候选值的一部分保留，重复的相同值按一个候选处理。

## 可配置正态分布

int64 属性选择“按分布抽样 → 正态分布（取整并截到边界）”，填写最小值、最大值、
均值 μ 和标准差 σ。切换到正态时默认均值取区间中点，标准差取区间宽度的 1/6（至少 1），
均可修改；离开正态选项会移除专属参数。批量生成和持续流量使用同一个属性生成器。

HTTP/Schema 新增 `distribution: normal`、`mean`、`stdDev`。均值必须有限且处于闭区间内，
标准差必须有限且大于 0；仅用于 int64/sample，边界必须在 ±9007199254740991 内。
其他分布仍支持原来的完整 64 位范围，strings/uint64s 保持原来的集合序号分布。

后端计算 μ + σZ（Z 为标准正态），使用 math.Round 取整，然后将结果截到 min/max。
这是边界截断值的正态抽样，不是越界重抽的条件正态；边界可能聚集，实际均值/标准差
会受边界和整数化影响。相同输入与随机种子可复现，不承诺批量与持续流量具有相同序列。
