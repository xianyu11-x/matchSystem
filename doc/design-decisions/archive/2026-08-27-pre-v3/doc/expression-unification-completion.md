# 表达式统一改造完成报告

> 状态：文档与当前 worktree 代码契约同步；本文件只记录文档审计结果，不代表一次提交。

## 结论

当前架构已经统一为一套 `expression` Arena/Root/Compiler/Program。每个节点和 Root 都
显式声明五种 ResultType 之一：Bitmap、Bool、Int64、Strings、Uint64s；合法 Kind、child
result、source/capability、Contract namespace、limits、canonical、dependencies 和 typed
runtime 入口由 shared expression 包集中约束。

`logical-node-contract/v2` 是 Prefilter 与 Evaluation 的唯一业务 Contract。Prefilter 和
Evaluation 的 `prefilter/v2`、`evaluation/v2` 只是各自 JSON envelope，和 Contract 版本
不是同一概念；两个 v1 envelope 只作为 `UNKNOWN_SCHEMA_VERSION` 的负向输入，不提供兼容
加载路径。

## 领域边界

- Prefilter 只提供 Bitmap DomainLeaf、index binding、query sidecar 和 Roaring executor。
  Dynamic operand 由 shared Compiler 编译为所属 Program 的 typed InstructionID；sidecar
  不创建子 Program，也不遍历通用 Arena/AST。
- Evaluation 每个 LogicalNode 选择一个 CandidateScorer；Join 和 Complete 是 Bool roots，
  Initialize/OnJoin 是 match-scope Fact 的 typed value roots。Join 可读取 Seed、Tick、当前
  Candidate 和 Match Fact；OnJoin 成功后由 owner 原子更新 Match Fact，后续不能读取已有成员
  的属性或 Fact。Complete 只读 Match/Tick Fact，不能遍历 Match 成员。
- `LogicalNodeSpec.Contract` 是唯一契约入口，实际调用为
  `prefilter.Compile(config, schema)` 和 `evaluation.Compile(config, schema, options)`。

## 文档同步范围

已同步根 README、文档总入口、Expression Core、Contract、Evaluation、Prefilter 使用/架构/
代码索引、JSON/热更新、索引初筛、Fact 生命周期、匹配框架、Router 和 Seed 生命周期。
目录入口均使用当前文件名和相对链接；已删除的兼容 API 不再作为可用入口记录。

## 文档检查

在当前 worktree 执行并通过：

- Markdown 本地链接解析：全部目标存在；
- Markdown 围栏配对：全部闭合；
- 旧 envelope/API 扫描：只保留 v1 明确拒绝说明，未保留旧正向示例或已删除 API 说明；
- `git diff --check`：无 whitespace error（Git 的换行转换提示不属于 diff error）。

代码测试与性能结果以主任务最终审计为准；本次文档子任务未修改 Go 文件，也未提交。
