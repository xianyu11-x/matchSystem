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
/**
 * Variadic JSON arrays are append-only.  A sparse/null entry is the first
 * missing port, so values after it must not become addressable graph ports.
 * Keeping this normalization in the AST adapter also prevents React Flow
 * from ever rendering a handle for a hole in `children`/`items`.
 */
const list = (value: unknown): unknown[] => {
  if (!Array.isArray(value)) return []
  const result: unknown[] = []
  for (const item of value) {
    if (item === undefined || item === null) break
    result.push(item)
  }
  return result
}
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
  if (op === 'strings_is_empty')
    return [{ key: 'values', value: value.values, expectedType: 'strings' }]
  if (op === 'uint64s_is_empty')
    return [{ key: 'values', value: value.values, expectedType: 'uint64s' }]
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
  if (op === 'and' || op === 'or') return 'prefilter.combine'
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
  previousGraph?: RuleGraphDocument,
): RuleGraphDocument {
  const nodes: RuleGraphNode[] = []
  const edges: RuleGraphEdge[] = []
  const generatedNodeIds = new Set<string>()
  const generatedEdgeIds = new Set<string>()
  const fixedNodeIds = new Set<string>()
  const layoutRoots: string[] = []
  const previousNodes = previousGraph?.nodes ?? []
  const makeId = (path: string) => `ast-${path.replaceAll('/', '-').replaceAll('~', '_')}`

  const previousNodeFor = (id: string, astPath?: string): RuleGraphNode | undefined =>
    previousNodes.find(
      (node) => node.id === id || (astPath !== undefined && node.data.astPath === astPath),
    )

  const walk = (raw: unknown, path: string, expected: ValueType): WalkResult => {
    const value = object(raw)
    const op = text(value.op) || 'unknown'
    const outputType = outputTypeForOp(op, expected)
    const children = inputEntries(op, value)
    const childResults = children.map((entry) => ({
      entry,
      result:
        entry.value === undefined || entry.value === null
          ? undefined
          : walk(entry.value, `${path}/${entry.key}`, entry.expectedType),
    }))
    const nodeType = nodeTypeForOp(op, outputType, text(value.source))
    const id = makeId(path)
    const previous = previousNodeFor(id, path)
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
    const variadicInputType = variadic
      ? op === 'strings_union'
        ? 'strings'
        : op === 'uint64s_union'
          ? 'uint64s'
          : op === 'bool_and' || op === 'bool_or'
            ? 'bool'
            : 'bitmap'
      : undefined
    if (variadic) {
      const listKey = op === 'strings_union' || op === 'uint64s_union' ? 'items' : 'children'
      config[listKey] = list(value[listKey]) as JsonValue
    }
    nodes.push({
      id,
      type: 'rule',
      position: previous?.position ?? { x: 0, y: 0 },
      data: {
        label: labelForOp(op),
        nodeType,
        outputType,
        inputTypes,
        variadicInputType,
        maxInputs: variadic ? 16 : children.length,
        requiredInputs: children.length,
        variadic,
        config,
        comment: previous?.data.comment ?? '',
        astPath: path,
      },
    })
    generatedNodeIds.add(id)
    childResults.forEach(({ result }, index) => {
      if (!result) return
      const edge: RuleGraphEdge = {
        id: `edge-${result.id}-${id}-${index}`,
        source: result.id,
        target: id,
        targetHandle: `input-${index}`,
        type: 'smoothstep',
        data: { valueType: result.outputType },
      }
      edges.push(edge)
      generatedEdgeIds.add(edge.id)
    })
    return { id, outputType }
  }

  const addOutputRoot = (
    id: string,
    label: string,
    nodeType: RuleGraphNode['data']['nodeType'],
    field: string,
    expression: WalkResult | undefined,
  ) => {
    const previous = previousNodeFor(id)
    nodes.push({
      id,
      type: 'rule',
      position: previous?.position ?? { x: 0, y: 0 },
      data: {
        label,
        nodeType,
        outputType: nodeType === 'prefilter.output' ? 'bitmap' : 'bool',
        inputTypes: [nodeType === 'prefilter.output' ? 'bitmap' : 'bool'],
        maxInputs: 1,
        requiredInputs: 1,
        config: { field } as Record<string, JsonValue>,
        comment: previous?.data.comment ?? '',
        astPath: undefined,
      },
    })
    generatedNodeIds.add(id)
    fixedNodeIds.add(id)
    layoutRoots.push(id)
    if (expression)
      edges.push({
        id: `edge-${expression.id}-${id}`,
        source: expression.id,
        target: id,
        targetHandle: 'input-0',
        type: 'smoothstep',
        data: { valueType: expression.outputType },
      })
    if (expression) generatedEdgeIds.add(`edge-${expression.id}-${id}`)
  }

  const prefilterExpr = object(object(object(document.prefilter).bitmap).expr)
  const prefilterExpression =
    Object.keys(prefilterExpr).length > 0
      ? walk(prefilterExpr, '/prefilter/bitmap/expr', 'bitmap')
      : undefined
  addOutputRoot(
    'root-prefilter',
    'Prefilter · final result',
    'prefilter.output',
    'bitmap',
    prefilterExpression,
  )
  const evaluation = document.evaluation as EvaluationDocument
  const canJoinExpression = evaluation.canJoin?.expr
    ? walk(evaluation.canJoin.expr, '/evaluation/canJoin/expr', 'bool')
    : undefined
  const canCompleteExpression = evaluation.canComplete?.expr
    ? walk(evaluation.canComplete.expr, '/evaluation/canComplete/expr', 'bool')
    : undefined
  addOutputRoot(
    'root-canJoin',
    'Evaluation · canJoin',
    'evaluation.join',
    'canJoin',
    canJoinExpression,
  )
  addOutputRoot(
    'root-canComplete',
    'Evaluation · canComplete',
    'evaluation.complete',
    'canComplete',
    canCompleteExpression,
  )

  // Place leaves on the left and fixed output sinks on the right. Existing
  // positions win, so rebuilding an AST never makes a user's graph jump.
  const childrenByTarget = new Map<string, RuleGraphEdge[]>()
  for (const edge of edges) {
    const children = childrenByTarget.get(edge.target) ?? []
    children.push(edge)
    childrenByTarget.set(edge.target, children)
  }
  const nodeById = new Map(nodes.map((node) => [node.id, node]))
  const depthCache = new Map<string, number>()
  const treeDepth = (id: string): number => {
    const cached = depthCache.get(id)
    if (cached !== undefined) return cached
    const children = childrenByTarget.get(id) ?? []
    const depth =
      children.length === 0 ? 0 : 1 + Math.max(...children.map((edge) => treeDepth(edge.source)))
    depthCache.set(id, depth)
    return depth
  }
  let nextLeafY = 0
  const assignTree = (id: string, depth: number, rootDepth: number): number => {
    const children = childrenByTarget.get(id) ?? []
    const y =
      children.length === 0
        ? (() => {
            const value = nextLeafY
            nextLeafY += 1
            return value * 132
          })()
        : children.reduce((sum, edge) => sum + assignTree(edge.source, depth + 1, rootDepth), 0) /
          children.length
    const node = nodeById.get(id)
    if (node && !previousNodeFor(node.id, node.data.astPath)?.position)
      node.position = { x: (rootDepth - depth) * 250, y }
    return y
  }
  const maxRootDepth = Math.max(0, ...layoutRoots.map((rootId) => treeDepth(rootId)))
  for (const rootId of layoutRoots) assignTree(rootId, 0, maxRootDepth)

  // Palette nodes are in-memory graph workspaces until connected. Preserve
  // them and their still-free edges across AST rebuilds.
  const customNodes = previousNodes.filter(
    (node) => !node.data.astPath && !generatedNodeIds.has(node.id) && !fixedNodeIds.has(node.id),
  )
  const customIds = new Set(customNodes.map((node) => node.id))
  const allNodeIds = new Set([...generatedNodeIds, ...customIds])
  const customEdges = (previousGraph?.edges ?? []).filter(
    (edge) =>
      !generatedEdgeIds.has(edge.id) &&
      (customIds.has(edge.source) || customIds.has(edge.target)) &&
      allNodeIds.has(edge.source) &&
      allNodeIds.has(edge.target),
  )
  return { nodes: [...nodes, ...customNodes], edges: [...edges, ...customEdges] }
}
