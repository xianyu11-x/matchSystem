# ADR：公开 Go 包与模块边界

- 状态：已实施
- 日期：2026-11-06
- 范围：Go 模块路径、四个公开包及 `matchsystem` 的六个子包

## 背景

原有模块名为 `matchSystem`，`client`、`common`、`identity` 和 `matchsystem` 位于
`internal/`。Go 的 `internal` 访问规则使仓库外的 Go 模块不能直接导入这些能力。
需要让外部宿主直接接入匹配核心，同时保留进程内匹配和模拟器的职责边界。

## 备选方案

1. **根模块的 `pkg/` 目录**：集中公开四包及现有子包，由一个模块共同版本化；
   外部导入路径包含 `pkg/`，已有仓库内调用方需要更新导入。
2. **保留 `internal/`**：无需迁移目录，但外部模块不能直接导入这些包，无法满足接入需求。
3. **独立 SDK 模块**：公开包可以独立发布，但会增加 `go.mod`、跨模块依赖和版本协调；
   四包与模拟器当前在同一仓库共同演进，没有必要分开发布。
4. **内部实现加薄门面**：可以限制公开表面，但现有接口和模拟器依赖 `matchsystem`
   的多个子包，门面需要持续维护别名和适配，增加接口漂移与维护成本。

## 决定

将 `internal/client`、`internal/common`、`internal/identity` 和 `internal/matchsystem`
分别迁至 `pkg/client`、`pkg/common`、`pkg/identity`、`pkg/matchsystem`。后者的
`contract`、`evaluation`、`expression`、`fact`、`jsonstrict` 和 `prefilter` 六个子包
也可按各自的导出 API 使用。

根模块路径改为 `github.com/xianyu11-x/matchSystem`；`pkg/` 不增加 `go.mod`，四包
及其子包随根模块共同选择版本、发布与回滚。`internal/simulator` 和
`internal/simulatorapi` 仍为内部应用；`apps/web/go.mod` 保留其独立隔离边界。
`examples/go-consumer` 是验证接入的独立模块，不构成独立 SDK 发布模块。

`client` 负责路由计算，不提供网络 RPC。宿主负责传输、Fact Provider，并由单一 owner
goroutine 串行调用 PhysicalNode 及其 LogicalNode；轮次期间的 Ticket 投递约束见
[匹配系统使用指南](../../match-system/usage-guide.md)。RuleJSON、业务匹配行为和 JSON
契约不因目录迁移改变。

## 兼容代价

模块名与包导入路径均变化：原 `matchSystem/internal/...` 调用方需改为
`github.com/xianyu11-x/matchSystem/pkg/...` 并更新依赖。不同导入路径的 Go 包类型
具有不同身份，本次不提供旧路径兼容包装层。公开六个子包也扩大了需要考虑外部调用方
的 API 面。本地 `replace` 应指向包含根 `go.mod` 的仓库目录，不是 `pkg/`。

外部导入、宿主约束及独立消费者验证方式见 [Go 接入指南](../../match-system/go-consumer.md)；
实际检查结果和限制见 [验证记录](../testing/public-go-packages-validation.md)。
