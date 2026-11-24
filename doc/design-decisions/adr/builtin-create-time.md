# ADR：票据创建时间作为内建属性

- 状态：已实施
- 日期：2026-11-24
- 范围：`pkg/common`、规则 Contract、Prefilter、Evaluation、模拟器能力目录

`oldest` 和 `created_at` 已直接读取 `Ticket.CreatedAt`。规则过去要在额外的 Attribute 中
重复填写创建时间才能预筛。直接调用 Go 库和使用模拟器需要相同的规则与时间语义。

## 决策

1. `createTime` 是 `int64` 只读内建属性，读取宿主传入的 `Ticket.CreatedAt`。宿主保留
   选择实时时钟或虚拟时钟的责任。
2. 有效 Contract 为标量表达式和字段型算法提供内建属性。标量引擎沿用属性引用与
   `Lookup` 接口。Prefilter 在规则主动声明 `int64_range` 后按同一属性取值建索引。
3. Go 提供公开内建目录。模拟器的能力接口从该目录发布字段信息，规则编辑器独立展示
   内建属性。Prefilter 计划将内建依赖与宿主提供的属性依赖分别公开。

曾考虑由模拟器把创建时间复制进普通属性，但直接调用库的宿主仍要手动保持同步，
同一张票据会保留两个可分歧的值。内建属性沿用现有表达式格式，且每条票据只保留
现有的创建时间字段。

`createTime` 设为保留名。新版核心对旧规则中的同名 Attribute、Fact 和票据输入给出冲突
提示，便于作者显式迁移。发现入口、索引声明和迁移步骤见
[内建属性契约](../../match-system/reference/logical-node-contract.md#内建属性)。
