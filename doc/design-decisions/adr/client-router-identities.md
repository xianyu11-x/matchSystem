# ADR：匹配系统共享 uint64 身份与可注入选择器

- 状态：已实施
- 日期：2026-11-20
- 更新：2026-11-23，规则身份收敛为 RuleID，Namespace 作为文本别名
- 范围：`pkg/client` 路由契约、选择策略及宿主适配边界

## 背景

调用方需要统一的数值身份和可替换的节点选择策略。2026-11-20 的实现为 `client` 引入
独立数值身份，由宿主将匹配核心的字符串命名空间、部署 ID 和 `int32` 规则编号转换成
路由身份。宿主需要维护桥接状态，规则查找和部署热更新均受到影响。
共享身份的初次迁移曾在输入时把文本名称映射成数值。现有规则文件可以一次换成对应
的数值，运行时由此可以直接解析十进制身份。

## 备选方案

1. **共享 `identity` 和 `common` 的 `uint64` 类型**。路由计算、部署查找和投递能直接
   使用同一身份。Go 调用方需要迁移字面量和变量类型，JSON 边界需要无损表示这些整数。
2. **为 `client` 提供独立的数值身份**。2026-11-20 的实现采用此方案。宿主需维护
   规则和部署映射，并处理编码冲突。

## 决定

采用方案 1。`pkg/identity` 定义 `RuleKey`、`LogicalNodeKey` 和 `OwnerRef`，其中
RuleID、PlacementID 和 PhysicalNodeID 使用 `uint64`。Namespace 是可选字符串别名。`common` 定义
路由请求和结果，其中 TicketID、AffinityKey、RequestID 和 DecisionID 为 `uint64`。
字段校验和零值回退规则见[路由接入说明](../../match-system/client-router.md)。

规则库、路由表、规则配置绑定和部署查找都使用 RuleID。逻辑节点身份由 RuleID 和
PlacementID 构成，Owner 还包括 PhysicalNodeID。别名可以独立编辑，规则指纹、默认
选路和决策摘要只使用相关数值字段。

JSON 输出用十进制字符串承载身份，保留 JavaScript 无法用 Number 精确表示的高位。
Go 解码器也接受精确的 JSON 整数。文本形式使用 `0` 或无前导零的正十进制表示。
共享身份结构使用标准 JSON 字段标签。规则编译器、模拟器和 HTTP 在各自的输入边界
解析数字，`pkg/identity` 负责保存和校验已经得到的数值。

模拟器在场景校验时直接构造 `client.RouteTable`，在运行时调用具体的 Router 和节点
worker。`AddTicket` 直接返回 `TicketView`，其 `Decision` 字段携带路由结果。HTTP
输入输出转换和节点 worker 的串行调用分别由各自入口处理。

默认仍采用 weighted rendezvous hashing（加权最高随机权重哈希），并允许构造 Router
时注入 `RouteSelector`。选择器只在 Router 提供的合格候选中选择 Owner；路由表校验、
启用状态过滤、选择结果校验、Endpoint 解析与已有 Owner 的精确解析由 Router 统一
负责。宿主可以扩展选择策略，但不能绕开这些路由边界。

## 影响和代价

- Go 调用方可直接使用 `identity.RuleKey` 和数值部署 ID。`client.RuleKey` 是同一共享
  类型的别名。全局规则库每个 RuleID 保留一条定义，同一规则在不同物理节点上可有多个部署。
- 原先用不同命名空间区分的规则需要各自使用唯一 RuleID。文字可写入 `namespace` 作为别名。
- 规则库和场景按数值键验证唯一性，数值身份决定查找、分片和 Owner 解析。
- 默认选择器和 `DecisionID` 按大端 `uint64` 编码数值字段。此次升级移除了旧命名空间
  字段，原版本的亲和分配和摘要可能变化。新版下改名时，数值输入相同即可保持分配和摘要。
- `DecisionID` 由 SHA-256 摘要前 64 位构造为非零 uint64，存在碰撞。模拟器通过
  十进制字符串传输它以保持精度，宿主使用完整 Owner 解析已有 Ticket 的目标。

详细行为见[路由接入说明](../../match-system/client-router.md)和
[模拟器架构](../../simulator/architecture.md#4-多物理节点运行时)。
