# 功能验证矩阵

本文把关键设计约束映射到可执行测试。它记录验证入口，不复制测试实现；新增行为时应在
同一行补充测试文件或增加新的行为域。

## 核心匹配

| 行为域 | 主要保证 | 证据入口 |
| --- | --- | --- |
| RuleJSON 编译 | 严格结构、版本、fingerprint、算法参数和运行预算 | [`rule_config_test.go`](../../../internal/matchsystem/rule_config_test.go) |
| LogicalNode 生命周期 | 创建、状态转换、Fact 元数据副本、候选上限 | [`logical_node_round_test.go`](../../../internal/matchsystem/logical_node_round_test.go)、[`candidate_limit_test.go`](../../../internal/matchsystem/candidate_limit_test.go) |
| 匹配流水线 | 固定阶段、成功提交、metrics 聚合、Provider 输入所有权 | [`logical_node_produce_match_test.go`](../../../internal/matchsystem/logical_node_produce_match_test.go) |
| Ticket store | DocID、增删、Object slot、提交预检和原子性 | [`ticket_store_test.go`](../../../internal/matchsystem/ticket_store_test.go) |
| Seed 策略 | arrival/oldest/priority/random、轮内不重复、下一轮恢复 | [`seed_order_test.go`](../../../internal/matchsystem/seed_order_test.go) |
| 候选排序 | Top-L、评分上限、稳定 tie-break、错误/取消 | [`candidate_ranking_test.go`](../../../internal/matchsystem/candidate_ranking_test.go)、[`candidate_ranking_fastpath_test.go`](../../../internal/matchsystem/candidate_ranking_fastpath_test.go) |
| 完整规则算法 | scoring、seedSelection 与 RuleKey 绑定 | [`rule_algorithm_integration_test.go`](../../../internal/matchsystem/rule_algorithm_integration_test.go) |

## 子包契约

| 包 | 主要保证 | 证据入口 |
| --- | --- | --- |
| `contract` | Fact 描述、类型、限制和严格解析 | [`contract_description_test.go`](../../../internal/matchsystem/contract/contract_description_test.go) |
| `evaluation` | trusted snapshot、`CanJoin`/`CanComplete` 求值 | [`predicates_test.go`](../../../internal/matchsystem/evaluation/predicates_test.go) |
| `fact` | Frame、Writer、Object slot 缓存、Validator 生命周期 | [`frame_test.go`](../../../internal/matchsystem/fact/frame_test.go) |
| `prefilter` | Store/TickSession、动态查询、范围索引和可信 Fact | [`store_test.go`](../../../internal/matchsystem/prefilter/store_test.go)、[`int64_range_index_test.go`](../../../internal/matchsystem/prefilter/int64_range_index_test.go) |

`expression` 与 `jsonstrict` 的行为还会被 Contract、Prefilter、Evaluation 和 RuleJSON 的
集成测试覆盖；修改其公共错误或节点语义时，应补充对应包的直接测试，而不是只依赖上层。

## Fact 与 Provider

| 行为域 | 主要保证 | 证据入口 |
| --- | --- | --- |
| Provider 握手 | Contract Facts 是 Descriptor Facts 的子集；Provider ID/version 合法，使用项的 name、type、scope、`MaxValues` 严格对齐，额外合法 Fact 可保留 | [`provider_descriptor_test.go`](../../../internal/matchsystem/provider_descriptor_test.go) |
| 可信快照 | Provider 值沿固定流程传递且不被运行时重复校验 | [`trusted_fact_flow_test.go`](../../../internal/matchsystem/trusted_fact_flow_test.go) |
| Object Fact | refresh/cache/writer 错误 metrics 与生命周期 | [`object_fact_flow_test.go`](../../../internal/matchsystem/object_fact_flow_test.go) |
| Match Fact | 完整快照模式、clone 与返回结果 | [`logical_node_produce_match_test.go`](../../../internal/matchsystem/logical_node_produce_match_test.go) |

## 模拟器与 API

| 行为域 | 主要保证 | 证据入口 |
| --- | --- | --- |
| 场景 JSON | wire 名称、默认值、往返与空场景 | [`scenario_json_test.go`](../../../internal/simulator/scenario_json_test.go) |
| 场景替换与运行时 | 原子替换、批量回滚、多节点运行、查询快照 | [`service_test.go`](../../../internal/simulator/service_test.go) |
| Match 历史 | 顺序、保留上限、详情和独立副本 | [`match_history_test.go`](../../../internal/simulator/match_history_test.go) |
| HTTP 契约 | 路由、JSON wire、错误、SSE、分页和能力列表 | [`handler_test.go`](../../../internal/simulatorapi/handler_test.go) |
| Fact 查询 | 元数据分层、not found/ambiguous/legacy 边界 | [`logical_node_facts_test.go`](../../../internal/simulatorapi/logical_node_facts_test.go) |
| API 启动握手 | 动态端口 ready JSON | [`main_test.go`](../../../cmd/simulator-api/main_test.go) |
| Web 数据适配 | API、能力、Fact、规则 IO、图编辑和分析 | [`apps/web/src/lib`](../../../apps/web/src/lib) 中的 `*.test.ts` |

## 建议执行顺序

```powershell
# Go 单元与集成测试
go test ./... -count=1

# 包依赖约束
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-expression-deps.ps1

# Web 类型与行为
npm --prefix apps/web run typecheck
npm --prefix apps/web test -- --run

# Desktop 配置契约（不要求执行 Rust/UE 编译）
npm --prefix apps/desktop run check:config
```

性能数据不属于功能通过条件，单独见[性能基准](performance-benchmark.md)；发布和回滚检查
见[发布验证](release-validation.md)。
