# 发布与验证

当前规则发布对象是一份不可变的 `match-rule/v1` RuleJSON，以及它所依赖的宿主动态
Fact Provider inventory（依赖清单）。RuleJSON 包含完整 `RuleKey`、Contract、Prefilter、
Evaluation、内置评分、Seed 选择和 runtime；Provider 实现不写入规则文件。

`CompileRuleJSON` 会产生不可变的 `CompiledRuleConfig`，其中包括 Contract、Prefilter
Plan、Evaluation 谓词、评分器、Seed 顺序和运行预算。完整 fingerprint 是规则文件的
规范化 SHA-256 摘要，Prefilter Plan fingerprint 仍可单独用于索引缓存。核心没有热加载
Manager、revision 管理或原子 swap API，也不替上层保存配置版本；`PhysicalNode.Load`
只在 owner 边界创建一个 LogicalNode，发布切换和回滚必须由上层路由/进程编排完成。

## 建议的上层发布流程

1. 为每个完整 `RuleKey` 准备一份不可变 `match-rule/v1` RuleJSON，并给 release bundle
   一个外部版本和内容摘要。文件中的 `ruleKey` 必须与部署的 `LogicalNodeKey.Rule`
   一致，PlacementID 留在场景拓扑中。
2. 在隔离编译阶段调用 `CompileRuleJSON`，校验所有嵌套 section、内置评分和 Seed
   参数，记录完整 fingerprint、`Plan.Fingerprint()`、有效 runtime 和所有依赖；编译
   失败时不接流量。
3. 为每个 LogicalNode 完成 Provider inventory，确认 Match scope Fact 都有唯一动态
   Provider，并确认 Tick/Object/Match Provider 的输入权限、完整快照和错误策略符合预期。
4. 在影子或新进程中用该 RuleJSON 创建 PhysicalNode，执行健康检查和验证清单；通过后
   由上层一次性把新请求路由到新 owner。旧 owner 继续 drain，直到其 Ticket 生命周期
   完成。
5. 发布记录保存完整 RuleJSON 摘要、RuleKey、完整 fingerprint、Plan fingerprint、
   程序构建版本、Provider 版本、验证结果和时间。

核心只承诺编译产物不可变及 owner 串行语义；路由层的切换原子性、持久化和跨进程
协调不属于本仓库。

## 回滚流程

上层保留最近一个已验证的完整 RuleJSON bundle，而不是只保存某个 Plan fingerprint。
发现错误时：

1. 停止将新流量路由到当前 bundle；
2. 将路由切回上一份 bundle 对应的已验证 owner/进程；
3. 对切回目标重新核对完整 RuleJSON fingerprint、RuleKey、Prefilter Plan fingerprint、
   runtime 和全部 Provider inventory；
4. 记录回滚原因、时间、影响范围和最终生效摘要。

如果只修改了规则文件，必须重新编译和重新记录两个 fingerprint；不能把新 RuleJSON 与
旧 Plan、旧 Contract 或旧 Provider 混用。若实现或依赖版本变化，也应视为新的 bundle。

## Provider inventory / checklist

每个 LogicalNode 的发布记录至少包括：

- LogicalNode key、完整 RuleKey、PlacementID、bundle 版本和构建版本；
- 完整 RuleJSON 摘要和 `CompiledRuleConfig.Fingerprint()`；
- `contract` 摘要、声明的 Fact/Attribute/index 以及有效 limits；
- `prefilter` 摘要、`Plan.Fingerprint()`、`containsProbeThreshold` 和 requirements；
- `evaluation` 摘要，确认只有 `canJoin`/`canComplete` 两个 Bool root；
- `scoring` 的内置类型和规范化参数；
- `seedSelection` 的内置类型、参数和确定性随机种子（如适用）；
- `runtime` 的候选上限、组大小和两个 Seed 尝试上限；
- Tick `FactProvider`、Object `ObjectFactProvider`、Match `MatchFactProvider` 的
  实现标识、版本和是否配置；
- 每个 Match scope Fact 的唯一 Provider 归属；没有 Match scope Fact 时记录“不调用”；
- Provider 输入 clone、完整快照校验、error/cancel 处理和原子提交检查结果；
- 预发布验证命令、输出摘要、操作者和时间。

## 本轮验证记录

本轮代码阶段应执行并记录：

```text
git diff --check
go test -count=1 -shuffle=on ./...
go vet ./...
go build ./...
go mod verify
go run ./cmd/app
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-expression-deps.ps1
```

统一 RuleJSON 的随机输入检查应以 `CompileRuleJSON` 为入口，确认未知字段、重复键、
尾随 JSON、非法 scorer/Seed 参数和错误 Contract 引用都不会 panic；正常配置的语义仍
由单元测试和集成测试覆盖。

`go test -race ./...` 需要在支持 CGO 的 CI 上补跑并记录结果；若当前环境
`CGO_ENABLED=0`，不能将未执行 race runtime 表述为已通过。

相关实现与验证入口：[RuleJSON 编译](../internal/matchsystem/rule_config.go)、
[LogicalNode 创建](../internal/matchsystem/logical_node.go)、
[Prefilter Plan](../internal/matchsystem/prefilter/plan.go)、
[依赖边界脚本](../scripts/check-expression-deps.ps1)。
