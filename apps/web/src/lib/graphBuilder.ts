import type {
  EvaluationDocument,
  JsonObject,
  JsonValue,
  RuleDocument,
  RuleGraphDocument,
  RuleGraphEdge,
  RuleGraphNode,
  ValueType,
} from '../types'

interface WalkResult {
  id: string
  outputType: ValueType
}

interface InputEntry {
  key: string
  value: unknown
  expectedType: ValueType
}

/** A JSON child slot, including a slot whose value is currently missing. */
export interface AstInputSlot {
  key: string
  expectedType: ValueType
  value: unknown
}

const object = (value: unknown): JsonObject =>
  value && typeof value === 'object' && !Array.isArray(value) ? (value as JsonObject) : {}
const text = (value: unknown) => (typeof value === 'string' ? value : '')
const list = (value: unknown): unknown[] => (Array.isArray(value) ? value : [])
const hasOwn = (value: object, key: string) => Object.prototype.hasOwnProperty.call(value, key)
const expressionValue = (value: unknown): unknown => {
  const envelope = object(value)
  return hasOwn(envelope, 'expr') ? envelope.expr : value
}

function outputTypeForOp(op: string, expected: ValueType): ValueType {
  if (
    op === 'none' ||
    op === 'and' ||
    op === 'or' ||
    op === 'exclude' ||
    op === 'if' ||
    op.startsWith('lookup_')
  )
    return 'bitmap'
  if (
    op === 'bool_literal' ||
    op.startsWith('bool_') ||
    op.includes('_eq') ||
    op.includes('_neq') ||
    op.includes('_lt') ||
    op.includes('_lte') ||
    op.includes('_gt') ||
    op.includes('_gte') ||
    op.endsWith('_is_empty') ||
    op.includes('contains') ||
    op.includes('intersects')
  )
    return 'bool'
  if (op.startsWith('int64_')) return 'int64'
  if (op.startsWith('strings_')) return 'strings'
  if (op.startsWith('uint64s_')) return 'uint64s'
  return expected
}

/**
 * Return the addressable expression slots for an AST operation. Scalar
 * envelopes in Prefilter are intentionally exposed through `<field>/expr` so
 * graph edits replace the expression without dropping its schemaVersion and
 * resultType wrapper.
 */
export function astInputSlots(op: string, value: JsonObject): AstInputSlot[] {
  if (op === 'bool_and' || op === 'bool_or' || op === 'and' || op === 'or')
    return list(value.children).map((item, index) => ({
      key: `children/${index}`,
      value: item,
      expectedType: op === 'bool_and' || op === 'bool_or' ? 'bool' : 'bitmap',
    }))
  if (op === 'bool_not') return [{ key: 'value', value: value.value, expectedType: 'bool' }]
  if (op === 'exclude') return [{ key: 'value', value: value.value, expectedType: 'bitmap' }]
  if (op === 'if')
    return [
      { key: 'when/expr', value: expressionValue(value.when), expectedType: 'bool' },
      { key: 'then', value: value.then, expectedType: 'bitmap' },
      { key: 'else', value: value.else, expectedType: 'bitmap' },
    ]
  if (op === 'lookup_string')
    return [{ key: 'values/expr', value: expressionValue(value.values), expectedType: 'strings' }]
  if (op === 'lookup_uint64')
    return [{ key: 'values/expr', value: expressionValue(value.values), expectedType: 'uint64s' }]
  if (op === 'lookup_range')
    return [
      { key: 'min/expr', value: expressionValue(value.min), expectedType: 'int64' },
      { key: 'max/expr', value: expressionValue(value.max), expectedType: 'int64' },
    ]
  if (op === 'int64_step') return [{ key: 'input', value: value.input, expectedType: 'int64' }]
  if (op === 'int64_clamp')
    return [
      { key: 'value', value: value.value, expectedType: 'int64' },
      { key: 'min', value: value.min, expectedType: 'int64' },
      { key: 'max', value: value.max, expectedType: 'int64' },
    ]
  if (
    op === 'int64_add' ||
    op === 'int64_sub' ||
    op === 'int64_min' ||
    op === 'int64_max' ||
    (op.startsWith('int64_') && !op.endsWith('_literal') && !op.endsWith('_ref'))
  )
    return [
      { key: 'left', value: value.left, expectedType: 'int64' },
      { key: 'right', value: value.right, expectedType: 'int64' },
    ]
  if (op === 'strings_union' || op === 'uint64s_union')
    return list(value.items).map((item, index) => ({
      key: `items/${index}`,
      value: item,
      expectedType: op === 'strings_union' ? 'strings' : 'uint64s',
    }))
  if (op === 'strings_contains' || op === 'uint64s_contains')
    return [
      {
        key: 'values',
        value: value.values,
        expectedType: op === 'strings_contains' ? 'strings' : 'uint64s',
      },
    ]
  if (op.startsWith('strings_') && !op.endsWith('_literal') && !op.endsWith('_ref'))
    return [
      { key: 'values', value: value.values, expectedType: 'strings' },
      { key: 'other', value: value.other, expectedType: 'strings' },
    ]
  if (op.startsWith('uint64s_') && !op.endsWith('_literal') && !op.endsWith('_ref'))
    return [
      { key: 'values', value: value.values, expectedType: 'uint64s' },
      { key: 'other', value: value.other, expectedType: 'uint64s' },
    ]
  return []
}

function inputEntries(op: string, value: JsonObject): InputEntry[] {
  return astInputSlots(op, value)
}

function nodeTypeForOp(
  op: string,
  outputType: ValueType,
  source = '',
): RuleGraphNode['data']['nodeType'] {
  if (op === 'bool_literal') return 'literal.bool'
  if (op === 'int64_literal') return 'literal.int64'
  if (op === 'strings_literal') return 'literal.string'
  if (op === 'uint64s_literal') return 'literal.uint64'
  if (op.endsWith('_ref')) return source.includes('fact') ? 'source.fact' : 'source.attribute'
  if (
    op.startsWith('int64_') &&
    ['int64_eq', 'int64_neq', 'int64_lt', 'int64_lte', 'int64_gt', 'int64_gte'].includes(op)
  )
    return 'compare.int64'
  if (
    op.startsWith('strings_') &&
    !op.endsWith('_literal') &&
    !op.endsWith('_ref') &&
    !op.includes('union')
  )
    return 'compare.strings'
  if (
    op.startsWith('uint64s_') &&
    !op.endsWith('_literal') &&
    !op.endsWith('_ref') &&
    !op.includes('union')
  )
    return 'compare.uint64'
  if (op === 'bool_and') return 'logic.and'
  if (op === 'bool_or') return 'logic.or'
  if (op === 'bool_not') return 'logic.not'
  if (op === 'lookup_string' || op === 'lookup_uint64' || op === 'lookup_range')
    return 'prefilter.lookup'
  if (op === 'exclude') return 'prefilter.exclude'
  if (outputType === 'bitmap' || ['and', 'or', 'if', 'none'].includes(op))
    return 'prefilter.generic'
  return 'expression.generic'
}

function labelForOp(op: string): string {
  const labels: Record<string, string> = {
    bool_literal: 'Bool 常量',
    int64_literal: 'Int64 常量',
    strings_literal: 'Strings 常量',
    uint64s_literal: 'Uint64s 常量',
    bool_and: 'Bool AND',
    bool_or: 'Bool OR',
    bool_not: 'Bool NOT',
    lookup_string: 'Lookup String',
    lookup_uint64: 'Lookup Uint64',
    lookup_range: 'Lookup Range',
  }
  return labels[op] ?? op
}

/**
 * Converts the four JSON envelopes into a stable, path-addressable graph.
 * The graph is a view: `astPath` makes edits write back to the source JSON.
 */
export function buildRuleGraph(
  document: Pick<RuleDocument, 'contract' | 'prefilter' | 'evaluation'>,
): RuleGraphDocument {
  const nodes: RuleGraphNode[] = []
  const edges: RuleGraphEdge[] = []
  let sequence = 0
  const makeId = (path: string) => `ast-${path.replaceAll('/', '-').replaceAll('~', '_')}`

  const walk = (raw: unknown, path: string, expected: ValueType, depth: number): WalkResult => {
    const value = object(raw)
    const op = text(value.op) || 'unknown'
    const outputType = outputTypeForOp(op, expected)
    const children = inputEntries(op, value)
    const childResults = children.map((entry) => ({
      entry,
      result:
        entry.value === undefined
          ? undefined
          : walk(entry.value, `${path}/${entry.key}`, entry.expectedType, depth + 1),
    }))
    const nodeType = nodeTypeForOp(op, outputType, text(value.source))
    const id = makeId(path)
    const config = { ...value } as Record<string, JsonValue>
    if (op.endsWith('_ref')) {
      config.source = text(value.source)
      config.name = text(value.name)
    }
    const variadic =
      op === 'bool_and' ||
      op === 'bool_or' ||
      op === 'and' ||
      op === 'or' ||
      op === 'strings_union' ||
      op === 'uint64s_union'
    const inputTypes = children.map((entry) => entry.expectedType)
    if (variadic && inputTypes.length === 0)
      inputTypes.push(
        op === 'strings_union'
          ? 'strings'
          : op === 'uint64s_union'
            ? 'uint64s'
            : op === 'bool_and' || op === 'bool_or'
              ? 'bool'
              : 'bitmap',
      )
    nodes.push({
      id,
      type: 'rule',
      position: { x: depth * 245, y: sequence++ * 92 },
      data: {
        label: labelForOp(op),
        nodeType,
        outputType,
        inputTypes,
        maxInputs:
          op === 'bool_and' || op === 'bool_or' || op === 'and' || op === 'or'
            ? 16
            : children.length,
        requiredInputs: children.length,
        variadic,
        config,
        astPath: path,
      },
    })
    childResults.forEach(({ result }, index) => {
      if (!result) return
      edges.push({
        id: `edge-${result.id}-${id}-${index}`,
        source: result.id,
        target: id,
        targetHandle: `input-${index}`,
        type: 'smoothstep',
        data: { valueType: result.outputType },
      })
    })
    return { id, outputType }
  }

  const addRoot = (label: string, field: 'canJoin' | 'canComplete', raw: unknown, path: string) => {
    const expression = raw === undefined ? undefined : walk(raw, `${path}/expr`, 'bool', 0)
    const id = `root-${field}`
    nodes.push({
      id,
      type: 'rule',
      position: { x: 920, y: field === 'canJoin' ? 210 : 430 },
      data: {
        label,
        nodeType: field === 'canJoin' ? 'evaluation.join' : 'evaluation.complete',
        outputType: 'bool',
        inputTypes: ['bool'],
        maxInputs: 1,
        requiredInputs: 1,
        config: { field },
        astPath: undefined,
      },
    })
    if (expression)
      edges.push({
        id: `edge-${expression.id}-${id}`,
        source: expression.id,
        target: id,
        targetHandle: 'input-0',
        type: 'smoothstep',
        data: { valueType: expression.outputType },
      })
  }

  const prefilterExpr = object(object(object(document.prefilter).bitmap).expr)
  if (Object.keys(prefilterExpr).length > 0)
    walk(prefilterExpr, '/prefilter/bitmap/expr', 'bitmap', 0)
  const evaluation = document.evaluation as EvaluationDocument
  addRoot('Evaluation · canJoin', 'canJoin', evaluation.canJoin?.expr, '/evaluation/canJoin')
  addRoot(
    'Evaluation · canComplete',
    'canComplete',
    evaluation.canComplete?.expr,
    '/evaluation/canComplete',
  )
  return { nodes, edges }
}
