# 发布与验证

当前核心的发布对象只有两类不可变产物：

- Expression/Evaluation 编译后的只读 Program/Predicates；
- Prefilter 编译后的只读 `Plan` 及其 `prefilter-fingerprint/v5`。

核心没有热加载 Manager、revision 管理或原子 swap API，也不替上层保存配置版本。
`PhysicalNode.Load` 只在 owner 边界创建一个 LogicalNode；发布切换和回滚必须由上层
路由/进程编排完成。

## 建议的上层发布流程

1. 将 `logical-node-contract/v3`、`prefilter/v3`、`evaluation/v3` JSON、scorer 和
   Provider 实现组成一个不可变 release bundle，并给 bundle 一个外部版本/内容摘要。
2. 在隔离的编译阶段解析同一份 Contract，编译 Prefilter 与 Evaluation，记录
   `Plan.Fingerprint()`、有效 limits 和所有依赖；编译失败时不接流量。
3. 为每个 LogicalNode 完成 Provider/scorer inventory，确认 Match scope Fact 都有
   唯一 Provider，CandidateScorer 非 nil，输入权限和错误策略符合预期。
4. 在影子或新进程中用该 bundle 创建 PhysicalNode，执行健康检查和验证清单；通过后
   由上层一次性把新请求路由到新 owner。旧 owner 继续 drain，直到其 Ticket 生命周期
   完成。
5. 发布记录同时保存 JSON 摘要、Contract 摘要、Prefilter fingerprint、程序构建
   版本、Provider/scorer 版本、验证结果和时间。

核心只承诺编译产物不可变及 owner 串行语义；路由层的切换原子性、持久化和跨进程
协调不属于本仓库。

## 回滚流程

上层保留最近一个已验证的完整 bundle，而不是只保存某个 fingerprint。发现错误时：

1. 停止将新流量路由到当前 bundle；
2. 将路由切回上一份 bundle 对应的已验证 owner/进程；
3. 对切回目标重新核对 Contract、Prefilter fingerprint、Evaluation JSON、scorer
   和全部 Provider inventory；
4. 记录回滚原因、时间、影响范围和最终生效摘要。

如果只修改了 JSON，必须重新编译和重新记录 fingerprint；不能把新 JSON 与旧 Plan、
旧 Contract 或旧 Provider 混用。若实现或依赖版本变化，也应视为新的 bundle。

## Provider inventory / checklist

每个 LogicalNode 的发布记录至少包括：

- LogicalNode key、bundle 版本和构建版本；
- Contract JSON 摘要、声明的 Fact/Attribute/index 以及有效 limits；
- Prefilter JSON 摘要、`Plan.Fingerprint()`、`containsProbeThreshold` 和 requirements；
- Evaluation JSON 摘要，确认只有 `canJoin`/`canComplete` 两个 Bool root；
- CandidateScorer 的实现标识、版本和非 nil 检查结果；
- Tick `FactProvider`、Object `ObjectFactProvider`、Match `MatchFactProvider` 的
  实现标识、版本和是否配置；
- 每个 Match scope Fact 的唯一 Provider 归属；没有 Match scope Fact 时记录“不调用”；
- Provider 输入 clone、完整快照校验、panic/error/cancel 处理和原子提交检查结果；
- 预发布验证命令、输出摘要、操作者和时间。

## 本轮验证记录

本轮代码阶段已通过：

```text
git diff --check
go test -count=1 -shuffle=on ./...
go vet ./...
go build ./...
go mod verify
go run ./cmd/app
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-expression-deps.ps1
```

Demo 成功产出两个 Match。Expression scalar、Prefilter loader 和 Evaluation loader
的短时 Fuzz 验证均通过，使用的入口分别是
标量、Prefilter 和 Evaluation loader 的随机输入检查；Fuzz 用于确认随机 JSON 不 panic，正常配置的语义仍
由单元测试和集成测试覆盖。

`go test -race ./...` 本轮未执行：当前环境 `CGO_ENABLED=0`，无法启用 race runtime。
这项环境限制不等同于功能验证失败；在支持 CGO 的 CI 上应补跑并记录结果。

相关实现与验证入口：[LogicalNode 创建](../internal/matchsystem/logical_node.go)、
[Prefilter Plan](../internal/matchsystem/prefilter/plan.go)、
[Evaluation 编译](../internal/matchsystem/evaluation/predicates.go)、
[依赖边界脚本](../scripts/check-expression-deps.ps1)。
