import scalarSchema from '../../../../api/schema/expression-scalar/v3.schema.json'
import bitmapSchema from '../../../../api/schema/prefilter/v3.schema.json'
import type { JsonObject, JsonValue, ValueType } from '../types'

interface SchemaField {
  type?: string
  $ref?: string
  oneOf?: SchemaField[]
  items?: SchemaField
  minimum?: number
  minItems?: number
  properties?: Record<string, SchemaField>
  const?: string
  enum?: string[]
}
export interface ExpressionDefinition {
  op: string
  resultType: ValueType
  fields: Record<string, SchemaField>
}

/** Derive editable operators from the same versioned contract as validation. */
export const expressionDefinitions: ExpressionDefinition[] = (
  ['bool', 'int64', 'strings', 'uint64s', 'bitmap'] as ValueType[]
).flatMap((resultType) => {
  const definitions = resultType === 'bitmap' ? bitmapSchema.$defs : scalarSchema.$defs
  const branches = (definitions as unknown as Record<string, { oneOf: SchemaField[] }>)[
    `${resultType}Expr`
  ].oneOf
  return branches.flatMap((branch) => {
    const { op, ...fields } = branch.properties!
    return (op.enum ?? [op.const!]).map((name) => ({ op: name, resultType, fields }))
  })
})

export const expressionLabels: Record<string, string> = {
  none: '空候选集',
  and: '交集',
  or: '并集',
  exclude: '排除',
  if: '条件分支',
  lookup_string: '字符串索引查询',
  lookup_uint64: '无符号整数索引查询',
  lookup_range: '整数范围查询',
  bool_literal: '布尔常量',
  bool_and: '全部满足',
  bool_or: '任一满足',
  bool_not: '取反',
  int64_literal: '整数常量',
  int64_ref: '整数引用',
  int64_add: '相加',
  int64_sub: '相减',
  int64_min: '取较小值',
  int64_max: '取较大值',
  int64_step: '阶梯映射',
  int64_clamp: '限制区间',
  int64_eq: '等于',
  int64_neq: '不等于',
  int64_lt: '小于',
  int64_lte: '小于等于',
  int64_gt: '大于',
  int64_gte: '大于等于',
  strings_literal: '字符串集合',
  strings_ref: '字符串引用',
  strings_union: '字符串集合合并',
  uint64s_literal: '无符号整数集合',
  uint64s_ref: '无符号整数引用',
  uint64s_union: '整数集合合并',
}
for (const prefix of ['strings', 'uint64s']) {
  for (const [suffix, label] of Object.entries({
    eq: '集合相等',
    neq: '集合不等',
    is_empty: '集合为空',
    contains: '包含固定值',
    contains_any: '包含任一值',
    contains_all: '包含全部值',
    intersects: '存在交集',
  }))
    expressionLabels[`${prefix}_${suffix}`] = `${prefix === 'strings' ? '字符串' : '整数'}${label}`
}

export function expressionFieldType(
  field: SchemaField,
  op: string,
): { type: ValueType; envelope: boolean } | undefined {
  // The schema allows both types for is_empty; the compiler requires the matching type.
  if (field.oneOf)
    return { type: op.startsWith('uint64s') ? 'uint64s' : 'strings', envelope: false }
  const match = field.$ref?.match(/\/(bool|int64|strings|uint64s|bitmap)(Expr|Root)$/)
  return match ? { type: match[1] as ValueType, envelope: match[2] === 'Root' } : undefined
}

export function defaultExpression(type: ValueType): JsonObject {
  if (type === 'bitmap') return { op: 'none' }
  if (type === 'bool') return { op: 'bool_literal', value: false }
  if (type === 'int64') return { op: 'int64_literal', value: 0 }
  return { op: `${type}_literal`, values: [] }
}

export function defaultExpressionField(field: SchemaField, op: string): JsonValue {
  const expression = expressionFieldType(field, op)
  if (expression) {
    const expr = defaultExpression(expression.type)
    return expression.envelope
      ? { schemaVersion: 'expression-scalar/v3', resultType: expression.type, expr }
      : expr
  }
  if (field.type === 'array')
    return Array.from({ length: field.minItems ?? 0 }, () =>
      defaultExpressionField(field.items!, op),
    )
  if (field.type === 'object')
    return Object.fromEntries(
      Object.entries(field.properties!).map(([key, item]) => [
        key,
        defaultExpressionField(item, op),
      ]),
    )
  if (field.type === 'integer') return 0
  if (field.type === 'boolean') return false
  return field.$ref?.endsWith('/source') ? 'seed_attributes' : ''
}

export function createExpression(op: string): JsonObject {
  const definition = expressionDefinitions.find((item) => item.op === op)
  if (!definition) throw new Error(`未知操作：${op}`)
  return {
    op,
    ...Object.fromEntries(
      Object.entries(definition.fields).map(([key, field]) => [
        key,
        defaultExpressionField(field, op),
      ]),
    ),
  }
}
