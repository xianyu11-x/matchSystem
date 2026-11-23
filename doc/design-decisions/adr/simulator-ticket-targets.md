# ADR：Ticket 投递目标与持续流量绑定

- 日期 2026-11-23
- 状态 已实施

## 背景

Ticket 表单原先统一选择规则和部署。单条和批量生成直接使用该部署，持续流量按规则交给
Router。相同选择在两类操作中有不同含义，批量生成也缺少验证部署权重的操作入口。

## 决定

表单统一提供指定规则和指定节点。规则模式以 RuleID 去重，采用已部署规则的 Contract，
每条 Ticket 使用 Router 的权重和亲和键。节点模式按 RuleID 和 PlacementID 精确选择部署。
HTTP 继续通过可选的 `placementId` 表达目标。

持续任务启动时在模拟器锁内解析并绑定完整 OwnerRef。每次直接投递复查 Owner 和启用状态，
目标不可用时任务失败。另一种做法是每次按 RuleID 和 PlacementID 查找当前部署，位置在拓扑
热更新期间被其他物理节点复用时会改变目标。绑定完整 OwnerRef 能把同一任务保留在用户选定的物理节点。

用法见[匹配对象](../../simulator/interface-guide.md#匹配对象)和[持续流量](../../simulator/continuous-traffic.md)。
