# 模拟器路由配置

在「运行总览 → 拓扑 → 路由策略」选择跨物理节点的路由算法。选择权重优先算法时填写 Ticket 阈值。应用后，
按规则投递的新 Ticket 使用新算法，已有 Ticket、匹配历史和持续流量继续保留。

每个物理节点在拓扑的添加或编辑表单中配置自己的路由同步间隔，默认 10 秒。同步读取
该节点所有逻辑部署的等待 Ticket 数。节点装载或部署修改成功后立即发布路由，后续队列
变化按各节点的间隔同步。按节点投递时，目标是指定的部署。

| 算法 | 路由行为 |
| --- | --- |
| 加权 Rendezvous（默认） | 按部署权重和亲和键选择，省略亲和键时使用 TicketID。 |
| 队列加权随机 | 各候选按 `1 / (1 + TicketCount)` 参与抽样，等待 Ticket 越少越容易被选中。 |
| 权重优先与队列阈值 | 先在队列低于阈值的候选中取配置权重最高者。达到阈值后切换，回落后恢复优先。全满时取队列最短者。 |

队列算法使用最近发布的快照。同步间隔越长，选路看到的数量与当前队列的差异可能越大。
权重优先算法的阈值用于选路，实际队列可超过阈值。节点内的逻辑节点调度仍由物理节点
的 `selector` 配置，决定一轮匹配尝试在哪个逻辑节点执行。
[客户端选择器契约](../match-system/client-router.md#路由选择策略)

## 场景 JSON 与 HTTP

场景根对象中的 `routing` 配置跨物理节点的路由算法。每条 `physicalNodes` 记录中的
`routeUpdateIntervalMs` 配置该节点的同步间隔，以整数毫秒为单位。以下是配置片段。

```json
{
  "routing": { "algorithm": "weighted_priority", "ticketLimit": 50 },
  "physicalNodes": [
    { "id": "1", "endpoint": "inproc://1", "enabled": true, "routeUpdateIntervalMs": 10000 }
  ]
}
```

`routing.algorithm` 可用 `weighted_rendezvous`、`queue_weighted_random` 或
`weighted_priority`。省略 `routing` 或算法时使用 `weighted_rendezvous`。
`weighted_priority` 的 `ticketLimit` 必须是正整数。`routeUpdateIntervalMs` 省略或为 0
时使用 `10000`，其余允许值为 `1` 至 `9223372036854`。

HTTP `PUT /api/v1/routing` 接受 `routing` 对象本身，适用于保留运行态并切换算法。
`GET /api/v1/scenario` 可查询并导出当前配置。节点间隔随新增、编辑物理节点请求提交，
拓扑接口会返回实际生效的间隔。[HTTP 契约](../../api/openapi/simulator.yaml) ·
[场景 Schema](../../api/schema/simulator-scenario/v1.schema.json)
