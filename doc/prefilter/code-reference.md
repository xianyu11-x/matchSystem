# Prefilter 逐文件逐函数说明

> 源码范围：`internal/matchsystem/prefilter` 当前全部 Go 文件，包括生产代码、测试和 benchmark。

## 1. `doc.go`

没有函数。Package comment 声明：

- Prefilter 是索引初筛。
- 过滤表达式是封闭声明式结构。
- 使用 Roaring Bitmap。
- TickSession 固定 Tick Facts；每次 Candidates 单独接收 Seed Facts，二者都不是索引并发快照。
- IndexStore 和 TickSession 只能由同一个 owner goroutine 串行驱动。
- 不允许扫描 Ticket 回退。
- 最终正确性和评分由上层负责。

## 2. `expr.go`

### 类型

- `Expr`：封闭 过滤表达式接口。
- `lookupExpr`、`andExpr`、`orExpr`、`excludeExpr`、`ifExpr`、`noneExpr`：六种私有节点。

### 函数

| 函数 | 说明 |
| --- | --- |
| `(*lookupExpr).expr()` | marker method，使 lookupExpr 实现 Expr。 |
| `(*andExpr).expr()` | marker method，使 andExpr 实现 Expr。 |
| `(*orExpr).expr()` | marker method，使 orExpr 实现 Expr。 |
| `(*excludeExpr).expr()` | marker method，使 excludeExpr 实现 Expr。 |
| `(*ifExpr).expr()` | marker method，使 ifExpr 实现 Expr。 |
| `(*noneExpr).expr()` | marker method，使 noneExpr 实现 Expr。 |
| `Lookup(query)` | 创建 Lookup 叶子；只保存 Query，不立即校验或执行。 |
| `And(children...)` | 复制 children slice，创建交集节点；空节点在 Compile 报 EMPTY_AND。 |
| `Or(children...)` | 复制 children slice，创建并集节点；空节点在 Compile 报 EMPTY_OR。 |
| `Exclude(child)` | 创建差集节点；Compile 要求它继承正向 scope。 |
| `If(condition,thenExpr,elseExpr)` | 创建二选一分支。 |
| `None()` | 创建恒为空结果节点。 |

## 3. `docset.go`

`DocSet` 是调用方拥有的可变 uint32 DocID 集合。

| 函数 | 说明 |
| --- | --- |
| `NewDocSet(ids...)` | 创建 Roaring Bitmap 并 AddMany。 |
| `wrapDocSet(data)` | 包装已有 roaring.Bitmap；data 为 nil 时创建空集合。 |
| `(*DocSet).ensure()` | 延迟初始化底层 data；nil receiver 本身不安全。 |
| `(*DocSet).Add(id)` | 原地加入 DocID。 |
| `(*DocSet).Remove(id)` | 原地删除 DocID。 |
| `(*DocSet).Contains(id)` | nil-safe 成员判断。 |
| `(*DocSet).Count()` | nil-safe 返回基数。 |
| `(*DocSet).IsEmpty()` | nil-safe 空判断。 |
| `(*DocSet).Clone()` | 深复制底层 Bitmap。 |
| `(*DocSet).Subtract(other)` | 原地差集；other 或其 data 为 nil 时不修改。 |
| `(*DocSet).IDs()` | 升序物化全部 DocID。 |
| `(*DocSet).ForEach(visit)` | 升序遍历；visit 返回 false 时短路；nil visit 无操作。 |

## 4. `fact_adapter.go`

### 类型

- 不再定义 `Document`；索引入口直接接收 `uint32 DocID + *common.Ticket`。
- `FactType`：中立 `matchsystem/fact.Type` 的别名；常量对应 strings、int64、uint64s。
- `FactSpec`：中立全链路 `fact.Spec` 的别名，Prefilter 编译器读取其中自己依赖的部分。
- `Facts`：中立 `fact.Values` 的别名；TickSession 只消费 Tick 层和当前 seed 对象层。

### 函数

| 函数 | 说明 |
| --- | --- |
| `validateFactTypes(path,facts)` | 调用 `fact.ValidateTypes`，再把通用 Fact 错误适配为 Prefilter evaluate 错误。 |
| `validateFactScopes(tickNames,seedNames)` | 调用 `fact.ValidateScopes` 校验 Tick/Seed 重名，并保留 Prefilter 错误边界。 |
| `adaptFactError(err)` | 将 `fact.Error` 的 Path、Code 和底层错误转换为 `prefilter.Error`。 |

## 5. `errors.go`

| 函数 | 说明 |
| --- | --- |
| `(*Error).Error()` | 格式化 Phase、Path、Code、Err；nil receiver 返回 `<nil>`。 |
| `(*Error).Unwrap()` | 返回底层 Err，支持 errors.Is/As。 |
| `compileError(path,code,format,args...)` | 构造 `Phase=compile` 的结构化错误。 |
| `jsonError(path,code,format,args...)` | 构造 `Phase=json` 且使用 JSON Path 的结构化错误。 |
| `evaluationError(path,code,format,args...)` | 构造 `Phase=evaluate` 的结构化错误。 |

## 6. `index.go`

### 类型

- `IndexType`：multi_value 或 int64_range。
- `KeyType`：string 或 uint64。
- `MultiValueIndexConfig`、`Int64RangeIndexConfig`：用户声明。
- `IndexSpec`：封闭物理索引工厂接口。
- `RequiredIndex`、`Requirements`：编译结果契约。
- `mutableIndex`：内部单 owner 生命周期接口，包含 validate/add/remove/prepare/estimate/lookup/contains。

### 函数

| 函数 | 说明 |
| --- | --- |
| `NewMultiValueIndex(config)` | 保存 MultiValue 配置，返回封闭 IndexSpec。 |
| `NewInt64RangeIndex(config)` | 保存 Int64Range 配置。 |
| `(multiValueIndexSpec).indexSpec()` | 默认文档/query key 上限各 64，默认 KeyType=string，生成 indexSpec。 |
| `(int64RangeIndexSpec).indexSpec()` | 生成 int64_range indexSpec。 |
| `newIndex(spec)` | 根据已编译 kind 创建 MultiValue 或 Int64Range 索引；未知内部 kind panic。 |

## 7. `multi_value_index.go`

### string MultiValue

| 函数 | 说明 |
| --- | --- |
| `newMultiValueIndex(spec)` | 根据 KeyType 分派 string 或 uint64 实现。 |
| `(*stringIndex).keys(ticket)` | 读取配置字段的 `common.Ticket.StringLists`，排序去重。 |
| `(*stringIndex).validate(ticket)` | 检查唯一 key 数不超过 MaxDocumentValues。 |
| `(*stringIndex).add(docID,ticket)` | 写 keysByDoc，并把 DocID 加入每个 string posting；无 key 时无操作。 |
| `(*stringIndex).remove(docID)` | 按 keysByDoc 清理 posting，删除空 posting 和反向记录。 |
| `(*stringIndex).prepare()` | 空操作；MultiValue posting 不需要批次前整理。 |
| `(*stringIndex).estimate(query)` | 校验 bound query 类型，累加 posting 基数；同 DocID 多 key 命中时可能高估。 |
| `(*stringIndex).lookup(query)` | OR 合并 query key 对应 posting，返回新 Bitmap。 |
| `(*stringIndex).contains(query,docID)` | 任一 query key posting 包含 DocID 即 true。 |

### uint64 MultiValue

| 函数 | 说明 |
| --- | --- |
| `(*uint64Index).keys(ticket)` | 读取 `common.Ticket.Uint64Lists`，升序去重。 |
| `(*uint64Index).validate(ticket)` | 检查 uint64 唯一 key 上限。 |
| `(*uint64Index).add(docID,ticket)` | 写原生 uint64 posting 和 keysByDoc。 |
| `(*uint64Index).remove(docID)` | 清理 uint64 posting、空 posting 和反向记录。 |
| `(*uint64Index).prepare()` | 空操作；uint64 posting 不需要 Tick 前整理。 |
| `(*uint64Index).estimate(query)` | 校验 boundUint64Query，累加 posting 基数。 |
| `(*uint64Index).lookup(query)` | OR 合并 uint64 posting。 |
| `(*uint64Index).contains(query,docID)` | 逐 query key 做原生 uint64 posting 成员探测。 |

## 8. `int64_range_index.go`

| 函数 | 说明 |
| --- | --- |
| `newInt64RangeIndex(spec)` | 初始化 postingsByValue 和 valueByDoc。 |
| `(*int64RangeIndex).validate(document)` | 当前恒返回 nil。 |
| `(*int64RangeIndex).add(document)` | 字段存在时写 value posting 与 DocID->value；新 value 标记 valuesDirty。 |
| `(*int64RangeIndex).remove(docID)` | 用 forward value 清理 posting；空 posting 删除并标记 valuesDirty。 |
| `(*int64RangeIndex).prepare()` | valuesDirty 时重建并升序排列 sortedValues；由 BeginTick 调用。 |
| `(*int64RangeIndex).rangeKeys(query)` | 两次 sort.Search 找到闭区间内 sortedValues。 |
| `(*int64RangeIndex).estimate(query)` | 校验 query 类型并累加范围 posting 基数。 |
| `(*int64RangeIndex).lookup(query)` | OR 合并范围内 postingsByValue。 |
| `(*int64RangeIndex).contains(query,docID)` | 从 valueByDoc 读取单值并判断闭区间。 |

## 9. `expressions.go`

### 9.1 StringExpr 构造函数

| 函数 | 说明 |
| --- | --- |
| `LiteralStrings(values...)` | 复制字面量 slice。 |
| `SeedStrings(field)` | 引用 seed.StringLists[field]。 |
| `FactStrings(name)` | 引用 Facts.StringLists[name]。 |
| `UnionStrings(items...)` | 复制子表达式 slice。 |

### 9.2 StringExpr marker

| 函数 | 说明 |
| --- | --- |
| `(*literalStringsExpr).stringExpr()` | 封闭接口 marker。 |
| `(*seedStringsExpr).stringExpr()` | 封闭接口 marker。 |
| `(*factStringsExpr).stringExpr()` | 封闭接口 marker。 |
| `(*unionStringsExpr).stringExpr()` | 封闭接口 marker。 |

### 9.3 StringExpr bind

| 函数 | 说明 |
| --- | --- |
| `(*literalStringsExpr).bindStrings(ctx)` | 返回字面量去重排序结果。 |
| `(*seedStringsExpr).bindStrings(ctx)` | 读取 seed 字段；缺失返回普通 error，Query 外层包装为 QUERY_BIND。 |
| `(*factStringsExpr).bindStrings(ctx)` | 读取 string Fact；缺失返回 error。 |
| `(*unionStringsExpr).bindStrings(ctx)` | 逐子项绑定，合并并去重排序。 |

### 9.4 StringExpr validate

| 函数 | 说明 |
| --- | --- |
| `(*literalStringsExpr).validateStrings(ctx,path)` | 无额外检查。 |
| `(*seedStringsExpr).validateStrings(ctx,path)` | 拒绝空字段名。 |
| `(*factStringsExpr).validateStrings(ctx,path)` | 要求 Fact 已注册且 Type=FactTypeStrings，并记录 required Fact。 |
| `(*unionStringsExpr).validateStrings(ctx,path)` | 拒绝空 Union 和 nil item，递归校验。 |

### 9.5 StringExpr canonical

| 函数 | 说明 |
| --- | --- |
| `(*literalStringsExpr).canonicalStrings()` | 对值去重排序后生成 literal 规范串。 |
| `(*seedStringsExpr).canonicalStrings()` | 生成 seed-strings 规范串。 |
| `(*factStringsExpr).canonicalStrings()` | 生成 fact-strings 规范串。 |
| `(*unionStringsExpr).canonicalStrings()` | 对子规范串排序后生成 union 规范串。 |
| `uniqueStrings(values)` | map 去重，字典序排序。 |
| `declaredStringValueCount(value,facts)` | 静态推导 literal/Fact/Union 最大 key 数；SeedStrings 返回 unknown。 |

### 9.6 Int64Expr 构造函数

| 函数 | 说明 |
| --- | --- |
| `LiteralInt64(value)` | int64 常量。 |
| `SeedInt64(field)` | 引用 seed.Int64Values。 |
| `FactInt64(name)` | 引用 int64 Fact。 |
| `Int64Step{At,Value}` | 通用阶梯项；At 是输入阈值。 |
| `StepInt64(input,steps...)` | 复制通用阶梯，并以任意 Int64Expr 作为输入。 |
| `ClampInt64(value,min,max)` | 创建动态闭区间钳制表达式。 |
| `AddInt64(left,right)` | 创建饱和加法表达式。 |
| `SubInt64(left,right)` | 创建饱和减法表达式。 |

### 9.7 Int64Expr marker

| 函数 | 说明 |
| --- | --- |
| `(*literalInt64Expr).int64Expr()` | marker。 |
| `(*seedInt64Expr).int64Expr()` | marker。 |
| `(*factInt64Expr).int64Expr()` | marker。 |
| `(*stepInt64Expr).int64Expr()` | marker。 |
| `(*clampInt64Expr).int64Expr()` | marker。 |
| `(*binaryInt64Expr).int64Expr()` | marker。 |

### 9.8 Int64Expr bind

| 函数 | 说明 |
| --- | --- |
| `(*literalInt64Expr).bindInt64(ctx)` | 返回常量。 |
| `(*seedInt64Expr).bindInt64(ctx)` | 读取 seed.Int64Values；缺失返回 error。 |
| `(*factInt64Expr).bindInt64(ctx)` | 先读取 Seed Facts、再读取 Tick Facts；缺失返回 error。 |
| `(*stepInt64Expr).bindInt64(ctx)` | 绑定 input，二分查找最后一个 At<=input 的 Value；低于首个 At 时返回首项。 |
| `(*clampInt64Expr).bindInt64(ctx)` | 绑定 value/min/max 后执行钳制；动态 min>max 返回 error。 |
| `(*binaryInt64Expr).bindInt64(ctx)` | 递归绑定左右值，执行 saturatingAdd/Sub。 |

### 9.9 Int64Expr validate

| 函数 | 说明 |
| --- | --- |
| `(*literalInt64Expr).validateInt64(ctx,path)` | 无额外检查。 |
| `(*seedInt64Expr).validateInt64(ctx,path)` | 拒绝空字段名。 |
| `(*factInt64Expr).validateInt64(ctx,path)` | 要求 Fact 存在且 Type=FactTypeInt64，记录依赖。 |
| `(*stepInt64Expr).validateInt64(ctx,path)` | 递归校验 input；要求 steps 非空且 At 严格递增；兼容等待入口额外要求 At 非负。 |
| `(*clampInt64Expr).validateInt64(ctx,path)` | 拒绝 nil，递归校验三个参数，并拒绝可静态确定的 min>max。 |
| `(*binaryInt64Expr).validateInt64(ctx,path)` | 拒绝 nil 左右操作数，递归校验。 |

### 9.10 Int64Expr canonical 与算术

| 函数 | 说明 |
| --- | --- |
| `(*literalInt64Expr).canonicalInt64()` | 常量规范串。 |
| `(*seedInt64Expr).canonicalInt64()` | 生成 seed-int64 规范串。 |
| `(*factInt64Expr).canonicalInt64()` | fact-int64 规范串。 |
| `(*stepInt64Expr).canonicalInt64()` | 编码 input 和 At:Value；兼容等待入口保留 wait-steps 规范串。 |
| `(*clampInt64Expr).canonicalInt64()` | 按 value/min/max 顺序生成 clamp 规范串。 |
| `(*binaryInt64Expr).canonicalInt64()` | 编码操作符和左右规范串。 |
| `saturatingAdd(left,right)` | 溢出钳制到 MinInt64/MaxInt64。 |
| `saturatingSub(left,right)` | 下溢/上溢钳制到 int64 边界。 |

### 9.11 Condition

| 函数 | 说明 |
| --- | --- |
| `GreaterOrEqual(left,right)` | 创建 compareInt64Condition，操作符为 >=。 |
| `(*compareInt64Condition).condition()` | 封闭 Condition marker。 |
| `(*compareInt64Condition).evaluate(ctx)` | 绑定左右值并返回 left>=right。 |
| `(*compareInt64Condition).validateCondition(ctx,path)` | 拒绝 nil 操作数，递归校验 Int64Expr。 |
| `(*compareInt64Condition).canonicalCondition()` | 生成稳定 condition 规范串。 |

## 10. `uint64_expr.go`

### 构造和 marker

| 函数 | 说明 |
| --- | --- |
| `LiteralUint64s(values...)` | 复制 uint64 字面量。 |
| `SeedUint64s(field)` | 引用 seed.Uint64Lists。 |
| `FactUint64s(name)` | 引用 Uint64 Fact。 |
| `UnionUint64s(items...)` | 复制子表达式。 |
| `(*literalUint64sExpr).uint64Expr()` | marker。 |
| `(*seedUint64sExpr).uint64Expr()` | marker。 |
| `(*factUint64sExpr).uint64Expr()` | marker。 |
| `(*unionUint64sExpr).uint64Expr()` | marker。 |

### bind

| 函数 | 说明 |
| --- | --- |
| `(*literalUint64sExpr).bindUint64s(ctx)` | 字面量去重排序。 |
| `(*seedUint64sExpr).bindUint64s(ctx)` | 读取 seed uint64 字段；缺失返回 error。 |
| `(*factUint64sExpr).bindUint64s(ctx)` | 读取 uint64 Fact；缺失返回 error。 |
| `(*unionUint64sExpr).bindUint64s(ctx)` | 绑定子项、合并、去重排序。 |

### validate

| 函数 | 说明 |
| --- | --- |
| `(*literalUint64sExpr).validateUint64s(ctx,path)` | 无额外检查。 |
| `(*seedUint64sExpr).validateUint64s(ctx,path)` | 拒绝空字段。 |
| `(*factUint64sExpr).validateUint64s(ctx,path)` | 要求 Fact 存在且 Type=FactTypeUint64s。 |
| `(*unionUint64sExpr).validateUint64s(ctx,path)` | 拒绝空 Union、nil item，递归校验。 |

### canonical 与辅助

| 函数 | 说明 |
| --- | --- |
| `(*literalUint64sExpr).canonicalUint64s()` | 对值去重排序后编码。 |
| `(*seedUint64sExpr).canonicalUint64s()` | 生成 seed-uint64s 规范串。 |
| `(*factUint64sExpr).canonicalUint64s()` | fact-uint64s 规范串。 |
| `(*unionUint64sExpr).canonicalUint64s()` | 排序子规范串并编码。 |
| `uniqueUint64s(values)` | 复制、升序排序并压缩重复项。 |
| `joinUint64s(values)` | 为规范串把 uint64 格式化为十进制；物理 posting 不使用 string。 |
| `declaredUint64ValueCount(value,facts)` | 静态推导 literal/Fact/Union key 数；Seed 字段返回 unknown。 |

## 11. `query.go`

### marker

| 函数 | 说明 |
| --- | --- |
| `(StringQuery).indexQuery()` | string Query marker。 |
| `(Uint64Query).indexQuery()` | uint64 Query marker。 |
| `(Int64RangeQuery).indexQuery()` | Int64Range marker。 |
| `(boundStringQuery).boundIndexQuery()` | bound string marker。 |
| `(boundUint64Query).boundIndexQuery()` | bound uint64 marker。 |
| `(boundInt64RangeQuery).boundIndexQuery()` | bound numeric marker。 |

### compiled query

| 函数 | 说明 |
| --- | --- |
| `(*compiledStringQuery).indexSlot()` | 返回编译期绑定的物理索引 slot。 |
| `(*compiledStringQuery).bind(ctx,path)` | 绑定 string keys，检查运行时 MaxQueryValues。 |
| `(*compiledStringQuery).canonical()` | 生成 multi-value-string 规范串。 |
| `(*compiledUint64Query).indexSlot()` | 返回 uint64 索引 slot。 |
| `(*compiledUint64Query).bind(ctx,path)` | 绑定 uint64 keys，检查运行时 key 上限。 |
| `(*compiledUint64Query).canonical()` | 生成 multi-value-uint64 规范串。 |
| `(*compiledInt64RangeQuery).indexSlot()` | 返回数值索引 slot。 |
| `(*compiledInt64RangeQuery).bind(ctx,path)` | 绑定 Min/Max；Min>Max 返回 INVALID_RANGE。 |
| `(*compiledInt64RangeQuery).canonical()` | 生成 int64-range 规范串。 |

## 12. `compiler.go`

### Plan

| 函数 | 说明 |
| --- | --- |
| `(*Plan).Fingerprint()` | nil-safe 返回 Fingerprint。 |
| `(*Plan).Requirements()` | nil-safe 返回 Requirements slice 副本。 |

### pathName

| 函数 | 说明 |
| --- | --- |
| `(*lookupNode).pathName()` | 返回 Lookup 过滤表达式path。 |
| `(*andNode).pathName()` | 返回 And path。 |
| `(*orNode).pathName()` | 返回 Or path。 |
| `(*excludeNode).pathName()` | 返回 Exclude path。 |
| `(*ifNode).pathName()` | 返回 If path。 |
| `(*noneNode).pathName()` | 返回 None path。 |

### anchor 和 scope

| 函数 | 说明 |
| --- | --- |
| `(*lookupNode).canAnchor()` | true。 |
| `(*lookupNode).canExecuteWithoutScope()` | true。 |
| `(*andNode).canAnchor()` | 任一 child 可 anchor 即 true。 |
| `(*andNode).canExecuteWithoutScope()` | 可 anchor 或静态 None。 |
| `(*orNode).canAnchor()` | 所有 child 可无 scope 执行，且至少一个可 anchor。 |
| `(*orNode).canExecuteWithoutScope()` | 所有 child 都可无 scope 执行。 |
| `(*excludeNode).canAnchor()` | false。 |
| `(*excludeNode).canExecuteWithoutScope()` | false。 |
| `(*ifNode).canAnchor()` | 两分支均可无 scope，且至少一支可 anchor。 |
| `(*ifNode).canExecuteWithoutScope()` | Then/Else 均可无 scope。 |
| `(*noneNode).canAnchor()` | false。 |
| `(*noneNode).canExecuteWithoutScope()` | true。 |

### canonical

| 函数 | 说明 |
| --- | --- |
| `(*lookupNode).canonical()` | lookup(query canonical)。 |
| `(*andNode).canonical()` | 调用 canonicalChildren("and")。 |
| `(*orNode).canonical()` | 调用 canonicalChildren("or")。 |
| `(*excludeNode).canonical()` | exclude(child canonical)。 |
| `(*ifNode).canonical()` | 编码 condition、Then、Else。 |
| `(*noneNode).canonical()` | 返回 none。 |
| `canonicalChildren(kind,children)` | 排序子规范串，使 And/Or 换序不改变 fingerprint。 |
| `isStaticallyNone(node)` | 在 compiled node tree 上判断恒空。 |

### 编译主流程

| 函数 | 说明 |
| --- | --- |
| `Compile(config)` | 默认 probe=4096；校验并注册 index/fact；编译 过滤表达式/Query；生成 requirements；对 canonical 内容做 SHA-256。 |
| `isNilInterface(value)` | 识别 interface 中包装的 nil pointer。 |
| `(*compileContext).compileExpr(expr,scope,path)` | DFS 编译；检测 cycle、nil、空组合、Exclude scope、If condition，并生成 compiledNode。 |
| `sourceCanAnchor(expr)` | 创建 visiting map，进入源表达式 anchor 分析。 |
| `sourceCanAnchorVisit(expr,visiting)` | 递归判断是否可建立正向 scope，防循环。 |
| `sourceCanRunWithoutScopeVisit(expr,visiting)` | 递归判断无继承 scope 可执行性。 |
| `sourceIsStaticallyNone(expr)` | 创建 visiting map，进入源表达式恒空分析。 |
| `sourceIsStaticallyNoneVisit(expr,visiting)` | 递归判断 None/And/Or/If 恒空性。 |
| `(*compileContext).compileQuery(query,path)` | 校验 Query、Index type、KeyType、动态值、Fact 和静态 key requirements，生成 compiledIndexQuery。 |
| `(*compileContext).resolveIndex(name,kind,path)` | 查找索引、校验 kind、记录 required index，返回 spec 和 slot。 |
| `(*compileContext).requirements()` | 收集实际依赖 index/fact，并按名称排序。 |
| `canonicalRequirements(requirements)` | 编码 index kind、KeyType、字段、限制和 Fact。 |
| `cloneRequirements(in)` | 复制 Indexes/Facts slice。 |

## 13. `json_contract.go` 与 `json.go`

### 固定契约与入口

| API | 说明 |
| --- | --- |
| `LogicalNodeContractSchemaVersion` | 独立 LogicalNode Index/Fact 契约固定为 `logical-node-contract/v1`。 |
| `JSONSchemaVersion` | 当前固定为 `prefilter/v1`。 |
| `JSONLimits` / `DefaultJSONLimits()` | JSON 字节、深度、值数量、children、常量数组、Step、字符串、Index 和 Fact 数量上限。 |
| `ParseLogicalNodeContract(data,limits)` | 严格解析独立契约 JSON，生成全部可用 IndexSpec/FactSpec。 |
| `LogicalNodeContract` | 已解析并准备冻结的全链路 IndexSpec、FactSpec 和资源上限；`JSONContract` 是兼容别名。 |
| `NewJSONCompiler(contract)` | 用 typed 编译器校验并快照契约，构造不可变 JSONCompiler。 |
| `(*JSONCompiler).Parse(data)` | 严格解码 JSON，按固定契约解析索引/Fact，生成普通 typed Config。 |
| `(*JSONCompiler).Compile(data)` | Parse 后复用 `Compile(Config)` 生成 Plan。 |

契约和计划解析器都先用 token visitor 拒绝重复 key、`null`、尾随 JSON、无效 UTF-8 和资源超限，再用 `DisallowUnknownFields` 解码每个封闭 tagged union。契约解析索引类型、KeyType、容量及 Fact 类型；`resolveJSONIndex` / `resolveJSONFact` 在计划转换期间验证引用，错误路径以 `$` 开头。

## 14. `store.go`

### IndexStore

| 函数 | 说明 |
| --- | --- |
| `New(plan)` | 拒绝 nil plan；按 spec 创建物理索引和空 Active。 |
| `(*IndexStore).Add(docID,ticket)` | 拒绝 0/重复 DocID 和 nil Ticket；先验证所有索引，再写全部索引，最后加入 Active。 |
| `(*IndexStore).Remove(docID)` | 非 Active 返回 false；否则清理所有索引和 Active。 |
| `(*IndexStore).Len()` | nil-safe 返回 Active 基数。 |
| `(*IndexStore).BeginTick(tickFacts)` | 校验 Tick Fact 类型命名空间、准备索引并只读借用 Tick Facts；调用方须保持其在 Session 期间不可变。 |

### TickSession

| 函数 | 说明 |
| --- | --- |
| `(*TickSession).Candidates(seedDocID,seedTicket,seedFacts)` | 调用 CandidatesWithStats，丢弃统计。 |
| `(*TickSession).CandidatesWithStats(seedDocID,seedTicket,seedFacts)` | 校验非 nil Ticket、active seed DocID、Seed Fact 类型和 Tick/Seed 同名冲突，再以分层 Fact 执行 root。 |
| `(*TickSession).eval(node,scope,ctx,stats)` | 分派 None、Lookup、Exclude、If、Or、And；执行 Bitmap 语义和统计。 |
| `(*TickSession).evalLookup(node,scope,ctx,stats)` | 绑定 Query；小 scope 用 Contains，其他用 Lookup 并按需 AND scope。 |
| `(*TickSession).evalAnd(node,scope,ctx,stats)` | 有 scope 时 clone；无 scope 时选最小 estimate anchor；顺序执行其他 child，空集短路。 |
| `(*TickSession).estimate(node,ctx)` | Lookup estimate、And 最小、Or 饱和求和、If 选中路径；无正向估算时报错。 |

## 15. `expressions_test.go`

| 函数 | 验证目标 |
| --- | --- |
| `TestStepInt64BindsArbitraryInput` | 通用 Step 在首阈值前、阈值处、区间内和末阈值后的绑定。 |
| `TestStepInt64BindsSeedFact` | 通用 Step 从普通 Seed Fact 字段绑定输入。 |
| `TestStepInt64CopiesSteps` | 构造函数复制调用方 steps。 |
| `TestClampInt64BindsBounds` | Clamp 的下限、区间内、上限和动态非法边界。 |
| `TestStepAndClampDriveInt64RangeQuery` | Step 与 Clamp 组合后驱动 Int64RangeQuery。 |
| `TestCompileRejectsInvalidStepAndClampExpressions` | nil、空/乱序阶梯和非法 Clamp 边界的编译错误。 |
| `TestCompileAcceptsNegativeGenericStepThreshold` | 通用 Step 允许负阈值。 |
| `TestStepAndClampAffectFingerprint` | Step/Clamp 参数进入计划指纹。 |
| `TestStepAndClampRecordFactDependencies` | 嵌套表达式 Fact 进入 Requirements。 |
| `numericExpressionConfig(expr)` | 测试辅助：把 Int64Expr 放入 Int64RangeQuery。 |

## 16. `compile_test.go`

| 函数 | 验证目标 |
| --- | --- |
| `TestCompileRejectsInvalidPlans` | Root、空组合、Exclude、Index、Query、Fact、KeyType 和 key requirements 错误。 |
| `TestCompileDetectsCycle` | 私有 过滤表达式自引用返回 CYCLE。 |
| `TestFingerprintNormalizesCommutativeChildren` | And 子项换序指纹不变，Requirements 有序。 |
| `TestRequirementsIncludesKeyType` | uint64 KeyType 进入 Requirements。 |
| `TestDefaultAndExplicitStringKeyTypeHaveSameFingerprint` | 默认 string 与显式 string 等价。 |

## 17. `store_test.go`

| 函数 | 验证目标 |
| --- | --- |
| `TestAndExcludeAndDynamicInt64Range` | And、锚定 Exclude 和普通 Fact 驱动的动态范围。 |
| `TestFactLayersRejectTypeAndScopeCollisions` | Tick/Seed Fact 单层类型冲突与跨层同名冲突。 |
| `TestSeedFactsTakePartInGenericFactBinding` | Seed Facts 通过通用 FactInt64 驱动查询。 |
| `TestOrAndIfOnlyEvaluateSelectedPath` | Or；If 不绑定未选路径的缺失 Fact。 |
| `TestNestedOrWithExcludeKeepsInheritedScope` | Or 中 Exclude 继承外层 scope。 |
| `TestSmallAccumulatorUsesContainsProbe` | 小 accumulator Contains、大 anchor Lookup。 |
| `TestInt64RangeUsesSparseDistinctKeysAndRemove` | int64 极值、稀疏 keys、Remove。 |
| `TestRuntimeQueryKeyLimitIsError` | seed 动态 string key 超限。 |
| `TestUint64QueryWithSeedFactAndLiteralUnion` | uint64 Seed/Fact/Literal、0/MaxUint64、去重、Tick Fact 借用边界、Remove。 |
| `TestUint64QueryLimits` | uint64 document/query key 上限。 |
| `TestUint64QueryUsesContainsProbe` | uint64 小集合 probe。 |
| `mustIndexStore(t,config)` | 测试辅助：Compile+New。 |
| `addTickets(t,store,tickets...)` | 测试辅助：逐个传入测试 DocID 和 common.Ticket。 |
| `candidates(t,session,seed)` | 测试辅助：调用 Candidates，错误即失败。 |
| `assertIDs(t,bitmap,want...)` | 比较升序 IDs。 |

## 18. `json_contract_test.go` 与 `json_test.go`

| 函数 | 验证目标 |
| --- | --- |
| `TestParseLogicalNodeContractThenCompileSeparatePlan` | 独立契约 JSON 构造 Index/Fact 后冻结，并编译另一份计划 JSON。 |
| `TestContractAndPlanJSONCannotBeMixed` | 契约对象拒绝 plan，计划对象拒绝 indexes/facts。 |
| `TestParseLogicalNodeContractRejectsInvalidIndexAndFactDeclarations` | 索引类型、KeyType、容量、Fact 类型/上限和重复名称校验。 |
| `TestParseLogicalNodeContractAppliesStrictDecodingAndLimits` | 契约重复 key 以及 Index/Fact 数量限制。 |
| `TestJSONCompilerMatchesTypedConfigAndExecutes` | JSON 与 typed Config 的 Fingerprint 相同，并验证分层 Fact、Step 和候选结果。 |
| `TestJSONCompilerValidatesFixedIndexAndFactContract` | 未提供索引/Fact、索引类型、KeyType 和 Fact 类型在 JSON 阶段拒绝。 |
| `TestJSONCompilerSupportsClosedV1ExpressionSet` | 覆盖 v1 的 Lookup/And/Or/If/Exclude/None 与 string/uint64/int64 表达式转换。 |
| `TestJSONCompilerStrictDecoding` | schema、必需字段、重复 key、unknown field、null、尾随值和未知 type。 |
| `TestJSONCompilerLimitsAndNumericValidation` | 字节、Step 数量和 int64 范围限制。 |
| `TestNewJSONCompilerValidatesContractAndSnapshotsSlices` | 固定契约先校验且不受调用方后续 slice 修改影响。 |

## 19. `oracle_test.go`

| 函数 | 验证目标 |
| --- | --- |
| `TestIndexedResultMatchesScanOracle` | 随机 500 Ticket，索引结果与测试专用扫描 oracle 一致，覆盖 Remove。 |
| `overlaps(left,right)` | oracle string slice 交集。 |
| `containsString(values,target)` | oracle string 成员判断。 |

生产代码没有使用这两个扫描函数。

## 20. `prefilter_bench_test.go`

| 函数 | 验证目标 |
| --- | --- |
| `BenchmarkIndexStoreRoaring` | 分别构建 100k、500k、1M 文档；数据构建在计时外；报告 Candidates 分配与耗时。 |
