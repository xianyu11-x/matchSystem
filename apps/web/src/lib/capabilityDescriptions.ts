import type { CapabilityNode, Capabilities, RuleGraphNodeData, ValueType } from '../types'

const valueTypeLabels: Record<ValueType, string> = {
  bool: 'Bool（布尔）',
  int64: 'Int64（有符号整数）',
  strings: 'Strings（字符串集合）',
  uint64s: 'Uint64s（无符号整数集合）',
  bitmap: 'Bitmap（候选集合）',
}

/** Operation-specific explanations used by both the real and demo palette. */
const operationDescriptions: Record<string, string> = {
  bool_literal: '生成一个固定的布尔值，通常作为规则分支的开关或默认条件。',
  bool_and: '只有所有输入条件都成立时才成立；支持多个 Bool 输入。',
  bool_or: '任一输入条件成立即成立；支持多个 Bool 输入。',
  bool_not: '反转一个 Bool 条件的结果。',
  int64_literal: '生成一个固定的 Int64 数值。',
  int64_ref: '从指定的 Attribute 或 Fact 中读取一个 Int64 数值。',
  int64_step: '根据一组按阈值排列的 steps，把输入 Int64 映射到另一个 Int64 值。',
  int64_clamp: '把输入 Int64 限制在 min 与 max 边界之间，超出范围的值会被截断。',
  int64_add: '将两个 Int64 表达式相加。',
  int64_sub: '用左侧 Int64 表达式减去右侧表达式。',
  int64_min: '返回两个 Int64 表达式中的较小值。',
  int64_max: '返回两个 Int64 表达式中的较大值。',
  int64_eq: '判断两个 Int64 表达式是否相等。',
  int64_neq: '判断两个 Int64 表达式是否不相等。',
  int64_lt: '判断左侧 Int64 是否小于右侧 Int64。',
  int64_lte: '判断左侧 Int64 是否小于或等于右侧 Int64。',
  int64_gt: '判断左侧 Int64 是否大于右侧 Int64。',
  int64_gte: '判断左侧 Int64 是否大于或等于右侧 Int64。',
  strings_literal: '生成一个固定的字符串集合。',
  strings_ref: '从指定的 Attribute 或 Fact 中读取字符串集合。',
  strings_union: '合并多个字符串集合，并保留集合语义。',
  strings_eq: '判断两个字符串集合是否完全相等。',
  strings_neq: '判断两个字符串集合是否不相等。',
  strings_is_empty: '判断字符串集合是否为空。',
  strings_contains: '判断字符串集合是否包含配置中的单个 needle。',
  strings_contains_any: '判断左侧字符串集合是否包含右侧集合中的任意值。',
  strings_contains_all: '判断左侧字符串集合是否包含右侧集合中的全部值。',
  strings_intersects: '判断两个字符串集合是否存在交集。',
  uint64s_literal: '生成一个固定的 Uint64 集合。',
  uint64s_ref: '从指定的 Attribute 或 Fact 中读取 Uint64 集合。',
  uint64s_union: '合并多个 Uint64 集合，并保留集合语义。',
  uint64s_eq: '判断两个 Uint64 集合是否完全相等。',
  uint64s_neq: '判断两个 Uint64 集合是否不相等。',
  uint64s_is_empty: '判断 Uint64 集合是否为空。',
  uint64s_contains: '判断 Uint64 集合是否包含配置中的单个 needle。',
  uint64s_contains_any: '判断左侧 Uint64 集合是否包含右侧集合中的任意值。',
  uint64s_contains_all: '判断左侧 Uint64 集合是否包含右侧集合中的全部值。',
  uint64s_intersects: '判断两个 Uint64 集合是否存在交集。',
  none: '返回空的候选 Bitmap，常用于新建或兜底的 Prefilter 分支。',
  and: '取多个候选 Bitmap 的交集，只有同时满足的候选会保留。',
  or: '取多个候选 Bitmap 的并集，满足任一分支的候选会保留。',
  exclude: '从一个候选 Bitmap 中排除另一个 Bitmap 的候选。',
  if: '根据 Bool 条件在两个 Bitmap 分支之间选择结果。',
  lookup_string: '使用 multi_value 字符串索引查询候选 Bitmap；输入是要查找的字符串集合。',
  lookup_uint64: '使用 multi_value Uint64 索引查询候选 Bitmap；输入是要查找的无符号整数集合。',
  lookup_range:
    '使用 int64_range 索引查询候选 Bitmap；两个输入分别是最小值和最大值，返回落在区间内的候选。',
}

function fallbackOperationDescription(capability: CapabilityNode): string {
  if (capability.type === 'source.attribute')
    return '读取当前 Ticket 的 typed Attribute，供后续表达式或 Prefilter 使用。'
  if (capability.type === 'source.fact')
    return '读取指定 scope 的 Fact snapshot，供后续表达式使用；可用 scope 由 Contract 和 Provider Descriptor 约束。'
  if (capability.type === 'literal.string') return '创建字符串集合常量。'
  if (capability.type === 'literal.int64') return '创建 Int64 数值常量。'
  if (capability.type === 'literal.uint64') return '创建 Uint64 集合常量。'
  if (capability.type === 'literal.bool') return '创建 Bool 常量。'
  if (capability.type === 'compare.int64') return '对两个 Int64 表达式执行比较并输出 Bool。'
  if (capability.type === 'compare.strings') return '对两个字符串集合执行相等、包含或交集判断。'
  if (capability.type === 'compare.uint64') return '对两个 Uint64 集合执行相等、包含或交集判断。'
  if (capability.type === 'logic.and') return '合并多个 Bool 条件，所有条件成立时输出 true。'
  if (capability.type === 'logic.or') return '合并多个 Bool 条件，任一条件成立时输出 true。'
  if (capability.type === 'logic.not') return '反转一个 Bool 条件。'
  if (capability.type === 'prefilter.lookup') return '通过索引查询并生成候选 Bitmap。'
  if (capability.type === 'prefilter.exclude') return '从候选 Bitmap 中排除一个子集。'
  if (capability.type === 'prefilter.combine') return '按交集或并集合并多个候选 Bitmap。'
  if (capability.type === 'evaluation.join') return '将 Bool 表达式接入 Evaluation.canJoin，决定候选是否允许加入 Match。'
  if (capability.type === 'evaluation.complete') return '将 Bool 表达式接入 Evaluation.canComplete，决定 Match 是否可以完成。'
  return capability.op
    ? `执行服务端声明的 ${capability.op} 操作。`
    : `执行 ${capability.label} 功能节点。`
}

function inputDescription(capability: CapabilityNode): string {
  if (capability.inputTypes.length === 0) return '输入：无。'
  const types = capability.inputTypes.map((type) => valueTypeLabels[type]).join('、')
  if (capability.variadic)
    return `输入：${types}；这是可变长输入，最多 ${capability.maxInputs} 个，新增端口必须按顺序连接。`
  return `输入：${types}。`
}

function configDescription(capability: CapabilityNode): string {
  const fields = capability.fields?.filter((field) => field !== 'op') ?? []
  if (fields.length === 0) return ''
  const details: Record<string, string> = {
    source: '数据源',
    name: '字段名',
    value: '常量值',
    values: '集合值/查询表达式',
    other: '右侧集合表达式',
    needle: 'needle 单值',
    steps: '阈值 steps',
    input: '输入表达式',
    left: '左表达式',
    right: '右表达式',
    min: '最小边界',
    max: '最大边界',
    index: '索引名',
    children: '子表达式列表',
    items: '集合项列表',
    when: '条件表达式',
    then: '条件为真分支',
    else: '条件为假分支',
  }
  return `配置：${fields.map((field) => details[field] ?? field).join('、')}。`
}

/**
 * Build a complete, user-facing description rather than exposing the raw
 * server string such as "服务端 capability: lookup_range".
 */
export function describeCapability(capability: CapabilityNode): string {
  const operation =
    (capability.op && operationDescriptions[capability.op]) ?? fallbackOperationDescription(capability)
  const output = `输出：${valueTypeLabels[capability.outputType]}。`
  const config = configDescription(capability)
  return [operation, inputDescription(capability), output, config].filter(Boolean).join(' ')
}

export function capabilityTooltip(capability: CapabilityNode): string {
  return [
    capability.label,
    describeCapability(capability),
    '拖动到画布后，再从输出端连接到兼容输入端。',
  ].join('\n')
}

/** Use the same detail level for nodes already placed on the canvas. */
export function describeRuleGraphNode(data: RuleGraphNodeData): string {
  const categoryByType: Record<RuleGraphNodeData['nodeType'], CapabilityNode['category']> = {
    'source.attribute': 'source',
    'source.fact': 'source',
    'literal.string': 'literal',
    'literal.int64': 'literal',
    'literal.uint64': 'literal',
    'literal.bool': 'literal',
    'compare.int64': 'expression',
    'compare.strings': 'expression',
    'compare.uint64': 'expression',
    'expression.generic': 'expression',
    'logic.and': 'expression',
    'logic.or': 'expression',
    'logic.not': 'expression',
    'prefilter.lookup': 'prefilter',
    'prefilter.exclude': 'prefilter',
    'prefilter.combine': 'prefilter',
    'prefilter.generic': 'prefilter',
    'prefilter.output': 'prefilter',
    'evaluation.join': 'evaluation',
    'evaluation.complete': 'evaluation',
  }
  const capability: CapabilityNode = {
    type: data.nodeType,
    op: typeof data.config.op === 'string' ? data.config.op : undefined,
    label: data.label,
    description: '',
    category: categoryByType[data.nodeType],
    outputType: data.outputType,
    inputTypes: data.inputTypes,
    maxInputs: data.maxInputs ?? data.inputTypes.length,
    variadic: data.variadic,
    variadicInputType: data.variadicInputType,
    fields: Object.keys(data.config),
  }
  return describeCapability(capability)
}

/** Apply the same description policy to demo and wire-discovered capabilities. */
export function enrichCapabilityDescriptions(capabilities: Capabilities): Capabilities {
  return {
    ...capabilities,
    nodeTypes: capabilities.nodeTypes.map((capability) => ({
      ...capability,
      description: describeCapability(capability),
    })),
  }
}
