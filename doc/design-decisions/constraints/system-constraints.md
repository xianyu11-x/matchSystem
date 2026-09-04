# 系统设计约束

本文汇总当前实现必须持续满足的设计边界。它是变更评审清单，不替代具体 API 文档。

## 所有权与并发

- 一个 PhysicalNode 由单一 owner goroutine 串行调用；`PhysicalNode`、`LogicalNode` 和其
  运行时组件默认不承诺 goroutine-safe。
- 一个 LogicalNode 独占 Ticket store、DocID、索引、Fact frame、Seed 策略和轮次预算。
- DocID 是 LogicalNode 内部索引身份，不进入 HTTP API，也不能跨节点复用。
- 对外查询和历史记录必须返回独立快照，不暴露 store 内借用的指针、map 或 slice。

## 依赖方向

```text
fact
contract ──> expression ──> prefilter
                    └─────> evaluation
contract + prefilter + evaluation + fact ──> matchsystem
apps/web -> HTTP/SSE -> simulatorapi -> simulator -> matchsystem
```

- `internal/matchsystem` 不导入 simulator、HTTP、桌面或前端类型。
- `expression` 只拥有标量编译与求值，不拥有 Bitmap、索引、Match 成员或 Fact 写入。
- Prefilter 和 Evaluation 共享 Contract 与标量表达式契约，但拥有各自的领域编译器。
- 依赖边界由 [`scripts/check-expression-deps.ps1`](../../../scripts/check-expression-deps.ps1)
  做静态检查。

## 配置与身份

- 一条规则只有一份 `match-rule/v1` RuleJSON；Contract、Prefilter、Evaluation、评分、
  Seed 选择和运行预算必须一起编译、发布与回滚。
- `RuleKey` 是规则语义身份，`PlacementID` 是部署身份；两者不得混用。
- 同一个 RuleKey 在同一场景内只能绑定同一份完整规则 fingerprint。
- 所有 JSON 入口 fail-closed：未知字段、重复键、`null`、非法版本、类型或资源超限均拒绝。
- JSON Schema 用于工具提示；Go 生产编译器是跨字段和运行时绑定的最终裁决者。

## Fact 与提交

- Contract 声明 Fact 的名称、类型和 scope；Provider Descriptor 独立声明宿主能力；运行时
  Fact 值不能反向生成任一声明。对每个 scope，Contract Facts 必须是 Provider Descriptor
  Facts 的子集；Contract 使用的 name、type、scope 和 `maxValues` 必须逐项严格匹配。
- Tick、Object、Match 三种 scope 不互相推导。Provider 可以声明额外的合法 Fact，规则不使用
  的额外声明不会自动进入 Contract；没有 Contract Fact 的 scope 也允许保留合法的非空
  Descriptor。
- Match Fact Provider 是 Match Fact 的唯一写入者，每次返回完整快照，不提供 patch/merge
  旁路。
- 候选求值期间的读取视图是借用且只读的；需要跨调用保留时必须 clone。
- Match 只有在完整通过 `CanComplete` 后才能原子提交；失败、取消或 Provider 错误不得留下
  半提交成员或部分 Fact。

## 运行时与模拟器

- `BeginMatchRound` 启动一轮有界、不重复的 Seed stream；轮次预算跨多次
  `ProduceMatch` 累计。
- 场景替换先完整构建并验证新 runtime，再原子发布；失败时旧场景继续运行。
- 模拟器第一阶段只承诺进程内状态和确定性重放，不承诺持久化、鉴权或跨进程一致性。
- 桌面端与浏览器端都只通过版本化 HTTP/JSON + SSE 访问模拟器，不使用业务绑定绕过 API。
- 桌面壳只终止自己持有 handle 的 sidecar，不按进程名清理其他实例。

## 变更门槛

以下变化必须新增或更新 ADR，并同步参数、包级指南和验证记录：

- 改变版本化 JSON 形状、默认值、错误语义或 fingerprint 范围；
- 改变所有权、并发模型、提交原子性或 Provider 生命周期；
- 反转包依赖，或在 expression 中引入 Bitmap/索引/业务领域对象；
- 改变 Seed/候选预算含义或排序稳定性；
- 引入持久化、鉴权、跨进程一致性或新的客户端通信边界。
