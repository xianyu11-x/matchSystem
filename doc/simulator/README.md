# 模拟器文档

本分类描述匹配模拟器及 MatchScope 客户端。模拟器是 `internal/matchsystem` 的宿主与
观测工具，不重新定义匹配规则语义。

## 阅读顺序

1. [使用指南](usage-guide.md)：启动 API、Web 和 Windows 桌面端，加载并校验规则。
2. [架构](architecture.md)：进程边界、多 PhysicalNode 编排、路由、并发与 API 边界。
3. [Fact 数据来源](fact-sources.md)：Contract、Provider Descriptor 与运行时值的分层。
4. [Match 历史](match-history.md)：列表、详情、保留上限和快照语义。
5. [客户端构建与发布](client-build.md)：Windows 安装包和便携 ZIP。

## 契约与代码入口

- HTTP 契约：[`api/openapi/simulator.yaml`](../../api/openapi/simulator.yaml)
- 场景 Schema：[`api/schema/simulator-scenario/v1.schema.json`](../../api/schema/simulator-scenario/v1.schema.json)
- 应用层：[`internal/simulator`](../../internal/simulator)
- HTTP 适配层：[`internal/simulatorapi`](../../internal/simulatorapi)
- 服务入口：[`cmd/simulator-api`](../../cmd/simulator-api)
- Web 客户端：[`apps/web`](../../apps/web)
- 桌面壳：[`apps/desktop`](../../apps/desktop)

规则参数和匹配语义不在本分类重复维护，统一参见
[匹配系统参数明细](../match-system/parameters.md)。
