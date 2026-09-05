# ADR：成局等待与调用耗时分离

- 日期：2026-09-06
- 状态：已采纳

## 背景

`durationMs` 已被用于最早成员到轮次逻辑时间的等待分析。轮次整体耗时同时含准备和未成局尝试，不能平均分摊后充当每局测量。

## 决策

在模拟器应用层围绕成功的 `PhysicalNode.ProduceMatch` 调用独立使用 Go 单调时钟，新增 HTTP `MatchView.processingDurationNs` 纳秒整数。包含 owner 命令投递、调度、Provider、核心提交与返回；排除模拟器命令锁等待、轮次准备、之前独立失败调用、观察仓库、事件、HTTP 和浏览器。它描述调用墙钟时间，不描述 CPU 或整轮端到端延迟。

保留 `durationMs` 及轮次时间原定义。两个字段独立传递、存储、分析。空/旧数据不合成测量值，时钟分辨率导致的零值原样保留。多选集合、窗口交集、统计与显示留在 Web，核心 LogicalNode 不保存 UI 选择或跨节点聚合状态。

## 后果与验证

应用层增加每次尝试两次时钟读取，不启用核心阶段 metrics，不改变匹配结果或所有权。只有产生 Match 的调用进入历史；这些值之和不是整轮成本。未来需要轮次成本时必须另设独立观测，不能改变本字段语义。

四种模拟器入口的测试使用实际阻塞 Provider 验证各局耗时包含对应调用区间、总测量不超过外层真实耗时。HTTP 测试验证轮次、列表与详情值一致，前端测试覆盖选择交集、缺失值、零值和不安全整数。完整用户说明见[Match 历史](../../simulator/match-history.md)。

2026-09-06 本次实现验证：`go test ./internal/simulator ./internal/simulatorapi ./cmd/simulator-api`、`go vet ./internal/simulator ./internal/simulatorapi`、Web `npm test` 和 `npm run build` 通过。真实 HTTP 服务创建三局（等待 100/200/300 ms），浏览器选择后两局显示均值 250 ms、P95 295 ms；清空选择显示 0 场且统计为空。构建保留现有大 bundle 提示。验证环境中极短成局调用可能读到 0 ns，阻塞 Provider 用例验证了包含实际耗时的边界；纳秒单位不承诺纳秒时钟精度。
