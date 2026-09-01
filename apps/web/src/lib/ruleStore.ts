import { create } from 'zustand'
import { astInputSlots, buildRuleGraph } from './graphBuilder'
import type {
  JsonObject,
  JsonValue,
  RuleDocument,
  RuleGraphEdge,
  RuleGraphNode,
  ValueType,
} from '../types'

type RulesTab = 'graph' | 'contract' | 'prefilter' | 'evaluation' | 'facts'

interface RuleEditorState {
  document?: RuleDocument
  selectedNodeId?: string
  activeTab: RulesTab
  dirty: boolean
  notice?: string
  setDocument: (document: RuleDocument) => void
  importDocument: (document: RuleDocument) => void
  setActiveTab: (activeTab: RulesTab) => void
  selectNode: (selectedNodeId?: string) => void
  setGraph: (nodes: RuleGraphNode[], edges: RuleGraphEdge[]) => void
  connectGraph: (
    sourceId: string,
    targetId: string,
    targetIndex: number,
    edge: RuleGraphEdge,
  ) => void
  removeGraphEdge: (edgeId: string) => void
  updateNodeData: (nodeId: string, data: Partial<RuleGraphNode['data']>) => void
  setEnvelope: (
    kind: 'contract' | 'prefilter' | 'evaluation' | 'tickFacts' | 'providerDescriptors',
    value: unknown,
  ) => void
  updateNodeConfig: (nodeId: string, config: Record<string, JsonValue>) => void
  addNode: (node: RuleGraphNode) => void
  removeNode: (nodeId: string) => void
  clearNotice: () => void
  resetDirty: () => void
}

export const useRuleStore = create<RuleEditorState>((set) => ({
  activeTab: 'graph',
  dirty: false,
  setDocument: (document) =>
    set({
      document,
      selectedNodeId: document.graph.nodes.find((node) => node.data.astPath)?.id,
      dirty: false,
      notice: undefined,
    }),
  importDocument: (document) =>
    set({
      document,
      selectedNodeId: document.graph.nodes.find((node) => node.data.astPath)?.id,
      dirty: true,
      notice: 'JSON 已导入到当前规则；保存前仍会执行本地和 Go 双重校验。',
    }),
  setActiveTab: (activeTab) => set({ activeTab }),
  selectNode: (selectedNodeId) => set({ selectedNodeId, notice: undefined }),
  setGraph: (nodes, edges) =>
    set((state) =>
      state.document ? { document: { ...state.document, graph: { nodes, edges } } } : state,
    ),
  connectGraph: (sourceId, targetId, targetIndex, _edge) =>
    set((state) => {
      if (!state.document) return state
      const source = state.document.graph.nodes.find((node) => node.id === sourceId)
      const target = state.document.graph.nodes.find((node) => node.id === targetId)
      if (!source || !target || !source.data.astPath) {
        return { notice: '只有带 AST 路径的表达式节点可以作为连接源。' }
      }
      const rootPath = evaluationRootExpressionPath(target)
      const nextDocument = rootPath
        ? replaceAstAtPath(
            state.document,
            rootPath,
            structuredClone(getAstAtPath(state.document, source.data.astPath)) as JsonObject,
          )
        : target.data.astPath
          ? setAstChildAtPath(state.document, target.data.astPath, targetIndex, source.data.astPath)
          : state.document
      if (nextDocument === state.document)
        return { notice: '该连接无法写回 AST，请先补齐目标表达式结构。' }
      return {
        document: { ...nextDocument, graph: buildRuleGraph(nextDocument) },
        dirty: true,
        notice: undefined,
      }
    }),
  removeGraphEdge: (edgeId) =>
    set((state) => {
      if (!state.document) return state
      const edge = state.document.graph.edges.find((item) => item.id === edgeId)
      if (!edge) return state
      const target = state.document.graph.nodes.find((node) => node.id === edge.target)
      const targetIndex = handleIndex(edge.targetHandle)
      if (!target?.data.astPath || targetIndex === undefined)
        return { notice: '根节点连接由 Evaluation envelope 驱动，不能单独删除。' }
      const nextDocument = deleteAstChildAtPath(state.document, target.data.astPath, targetIndex)
      if (nextDocument === state.document) return { notice: '连接删除失败，AST 未发生改变。' }
      return {
        document: { ...nextDocument, graph: buildRuleGraph(nextDocument) },
        dirty: true,
        notice: '连接已从 AST 删除；请补回必需输入后再保存。',
      }
    }),
  updateNodeData: (nodeId, data) =>
    set((state) => {
      if (!state.document) return state
      const currentNode = state.document.graph.nodes.find((node) => node.id === nodeId)
      if (!currentNode) return state
      if (currentNode.data.astPath && data.config) {
        const nextDocument = updateAstAtPath(state.document, currentNode.data.astPath, data.config)
        return {
          document: { ...nextDocument, graph: buildRuleGraph(nextDocument) },
          dirty: true,
          notice: undefined,
        }
      }
      const nodes = state.document.graph.nodes.map((node) =>
        node.id === nodeId ? { ...node, data: { ...node.data, ...data } } : node,
      )
      return {
        document: { ...state.document, graph: { ...state.document.graph, nodes } },
        dirty: true,
        notice: undefined,
      }
    }),
  setEnvelope: (kind, value) =>
    set((state) => {
      if (!state.document) return state
      const nextDocument = { ...state.document, [kind]: value } as RuleDocument
      return {
        document: { ...nextDocument, graph: buildRuleGraph(nextDocument) },
        dirty: true,
        selectedNodeId: undefined,
        notice: undefined,
      }
    }),
  updateNodeConfig: (nodeId, config) =>
    set((state) => {
      if (!state.document) return state
      const node = state.document.graph.nodes.find((item) => item.id === nodeId)
      if (!node) return state
      if (node.data.astPath) {
        const nextDocument = updateAstAtPath(state.document, node.data.astPath, config)
        return {
          document: { ...nextDocument, graph: buildRuleGraph(nextDocument) },
          dirty: true,
          notice: undefined,
        }
      }
      return { notice: '该节点没有可写回的 AST 路径。' }
    }),
  addNode: (node) =>
    set((state) => {
      if (!state.document) return state
      const target = state.document.graph.nodes.find((item) => item.id === state.selectedNodeId)
      if (!target) return { notice: '请先选择一个表达式节点或 Evaluation 根出口。' }
      const ast = graphNodeToAst(node)
      if (!ast) return { notice: '该 palette 节点不是可写入 AST 的表达式节点。' }
      const rootPath = evaluationRootExpressionPath(target)
      if (rootPath) {
        const nextDocument = replaceAstAtPath(state.document, rootPath, ast)
        const nextGraph = buildRuleGraph(nextDocument)
        return {
          document: { ...nextDocument, graph: nextGraph },
          dirty: true,
          selectedNodeId: nextGraph.nodes.find((item) => item.data.astPath === rootPath)?.id,
          notice: undefined,
        }
      }
      if (!target.data.astPath)
        return { notice: '该节点不是可编辑的 AST 表达式或 Evaluation 根出口。' }
      const targetAst = getAstAtPath(state.document, target.data.astPath)
      if (!isObject(targetAst)) return { notice: '目标 AST 节点不存在，无法插入操作。' }
      const slot = findAvailableSlot(targetAst, node.data.outputType)
      if (!slot && target.data.outputType !== node.data.outputType)
        return { notice: '该操作与目标表达式的结果类型不匹配，未修改 AST。' }
      const nextDocument = slot
        ? setAstValueAtPath(state.document, target.data.astPath, slot.key, ast, slot.expectedType)
        : replaceAstAtPath(state.document, target.data.astPath, ast)
      if (nextDocument === state.document)
        return { notice: '该操作与目标输入类型不匹配，未修改 AST。' }
      return {
        document: { ...nextDocument, graph: buildRuleGraph(nextDocument) },
        dirty: true,
        selectedNodeId: target.id,
        notice: undefined,
      }
    }),
  removeNode: (nodeId) =>
    set((state) => {
      if (!state.document) return state
      const node = state.document.graph.nodes.find((item) => item.id === nodeId)
      if (!node?.data.astPath)
        return { notice: 'Evaluation 根节点是 envelope 的固定出口，不能从图中删除。' }
      const nextDocument = deleteAstAtPath(state.document, node.data.astPath)
      if (nextDocument === state.document) return { notice: '节点删除失败，AST 未发生改变。' }
      return {
        document: { ...nextDocument, graph: buildRuleGraph(nextDocument) },
        dirty: true,
        selectedNodeId: undefined,
        notice: '节点已从 AST 删除；父节点的必需输入可能需要重新连接。',
      }
    }),
  clearNotice: () => set({ notice: undefined }),
  resetDirty: () => set({ dirty: false, notice: undefined }),
}))

export type { RulesTab }

const isObject = (value: unknown): value is Record<string, any> =>
  Boolean(value && typeof value === 'object' && !Array.isArray(value))

const pathSegments = (path: string): string[] =>
  path
    .split('/')
    .slice(1)
    .map((segment) => segment.replaceAll('~1', '/').replaceAll('~0', '~'))

const handleIndex = (handle?: string | null): number | undefined => {
  if (!handle?.startsWith('input-')) return undefined
  const index = Number(handle.slice('input-'.length))
  return Number.isInteger(index) && index >= 0 ? index : undefined
}

function evaluationRootExpressionPath(node: RuleGraphNode): string | undefined {
  if (node.data.nodeType === 'evaluation.join') return '/evaluation/canJoin/expr'
  if (node.data.nodeType === 'evaluation.complete') return '/evaluation/canComplete/expr'
  return undefined
}

export function getAstAtPath(document: RuleDocument, path: string): unknown {
  let cursor: any = document
  for (const segment of pathSegments(path)) {
    if (!cursor || typeof cursor !== 'object') return undefined
    cursor = cursor[segment]
  }
  return cursor
}

function setPathValue(root: any, segments: string[], value: unknown): boolean {
  if (segments.length === 0) return false
  let cursor = root
  for (let index = 0; index < segments.length - 1; index += 1) {
    const segment = segments[index]
    if (!isObject(cursor[segment]) && !Array.isArray(cursor[segment])) cursor[segment] = {}
    cursor = cursor[segment]
  }
  cursor[segments.at(-1)!] = value
  return true
}

function updateAstAtPath(
  document: RuleDocument,
  path: string,
  config: Record<string, JsonValue>,
): RuleDocument {
  const current = getAstAtPath(document, path)
  if (!isObject(current)) return document
  const next = structuredClone(document)
  const target = getAstAtPath(next, path)
  if (!isObject(target)) return document
  Object.assign(target, config)
  return next
}

function replaceAstAtPath(document: RuleDocument, path: string, value: JsonObject): RuleDocument {
  const segments = pathSegments(path)
  if (segments.length === 0) return document
  const next = structuredClone(document)
  return setPathValue(next, segments, value) ? next : document
}

function deleteAstAtPath(document: RuleDocument, path: string): RuleDocument {
  const segments = pathSegments(path)
  if (segments.length === 0) return document
  const next = structuredClone(document)
  let cursor: any = next
  for (const segment of segments.slice(0, -1)) {
    if (!cursor || typeof cursor !== 'object') return document
    cursor = cursor[segment]
  }
  if (!cursor || typeof cursor !== 'object' || !(segments.at(-1)! in cursor)) return document
  if (Array.isArray(cursor)) cursor.splice(Number(segments.at(-1)), 1)
  else delete cursor[segments.at(-1)!]
  return next
}

function scalarEnvelope(type: Exclude<ValueType, 'bitmap'>, expr: JsonObject): JsonObject {
  return { schemaVersion: 'expression-scalar/v3', resultType: type, expr }
}

function neutralAst(type: ValueType): JsonObject {
  if (type === 'bitmap') return { op: 'none' }
  if (type === 'bool') return { op: 'bool_literal', value: false }
  if (type === 'int64') return { op: 'int64_literal', value: 0 }
  if (type === 'uint64s') return { op: 'uint64s_literal', values: [] }
  return { op: 'strings_literal', values: [] }
}

function astOperationForType(type: ValueType): string {
  if (type === 'int64') return 'int64_literal'
  if (type === 'uint64s') return 'uint64s_literal'
  if (type === 'bool') return 'bool_literal'
  if (type === 'bitmap') return 'none'
  return 'strings_literal'
}

function graphNodeToAst(node: RuleGraphNode): JsonObject | undefined {
  const config = structuredClone(node.data.config) as Record<string, any>
  const type = node.data.nodeType
  if (type === 'evaluation.join' || type === 'evaluation.complete') return undefined
  if (type === 'source.attribute' || type === 'source.fact') {
    config.op = astOperationForType(node.data.outputType).replace('_literal', '_ref')
    return config as JsonObject
  }
  if (type === 'literal.string') {
    config.op = 'strings_literal'
    if (!Array.isArray(config.values)) config.values = []
  } else if (type === 'literal.int64') {
    config.op = 'int64_literal'
    if (typeof config.value !== 'number') config.value = 0
  } else if (type === 'literal.uint64') {
    config.op = 'uint64s_literal'
    if (!Array.isArray(config.values)) config.values = []
  } else if (type === 'literal.bool') {
    config.op = 'bool_literal'
    if (typeof config.value !== 'boolean') config.value = false
  } else if (type === 'compare.uint64') {
    if (typeof config.op !== 'string' || !config.op.startsWith('uint64s_')) config.op = 'uint64s_eq'
  } else if (type === 'compare.int64') {
    if (typeof config.op !== 'string') config.op = 'int64_eq'
  } else if (type === 'compare.strings') {
    if (typeof config.op !== 'string') config.op = 'strings_eq'
  } else if (type === 'logic.and') {
    config.op = 'bool_and'
    if (!Array.isArray(config.children)) config.children = []
  } else if (type === 'logic.or') {
    config.op = 'bool_or'
    if (!Array.isArray(config.children)) config.children = []
  } else if (type === 'logic.not') {
    config.op = 'bool_not'
  } else if (type === 'prefilter.lookup') {
    const valueType =
      config.valueType === 'uint64s'
        ? 'uint64s'
        : config.valueType === 'int64'
          ? 'int64'
          : 'strings'
    config.op =
      typeof config.op === 'string' &&
      ['lookup_string', 'lookup_uint64', 'lookup_range'].includes(config.op)
        ? config.op
        : valueType === 'uint64s'
          ? 'lookup_uint64'
          : valueType === 'int64'
            ? 'lookup_range'
            : 'lookup_string'
    delete config.valueType
    if (config.op === 'lookup_range') {
      if (!isObject(config.min)) config.min = scalarEnvelope('int64', neutralAst('int64'))
      if (!isObject(config.max)) config.max = scalarEnvelope('int64', neutralAst('int64'))
    } else if (!isObject(config.values)) {
      const scalarType = config.op === 'lookup_uint64' ? 'uint64s' : 'strings'
      config.values = scalarEnvelope(scalarType, neutralAst(scalarType))
    }
  } else if (type === 'prefilter.exclude') {
    config.op = 'exclude'
    if (!isObject(config.value)) config.value = neutralAst('bitmap')
  } else if (type === 'prefilter.combine') {
    config.op = config.op === 'or' ? 'or' : 'and'
    if (!Array.isArray(config.children)) config.children = []
  } else if (type === 'prefilter.generic' || type === 'expression.generic') {
    if (typeof config.op !== 'string' || !config.op) return undefined
  }
  return config as JsonObject
}

function isVariadic(op: string): boolean {
  return (
    op === 'bool_and' ||
    op === 'bool_or' ||
    op === 'and' ||
    op === 'or' ||
    op === 'strings_union' ||
    op === 'uint64s_union'
  )
}

function expectedVariadicType(op: string): ValueType {
  if (op === 'strings_union') return 'strings'
  if (op === 'uint64s_union') return 'uint64s'
  if (op === 'bool_and' || op === 'bool_or') return 'bool'
  return 'bitmap'
}

function findAvailableSlot(
  target: Record<string, any>,
  sourceOutput: ValueType,
): { key: string; expectedType: ValueType } | undefined {
  const op = typeof target.op === 'string' ? target.op : ''
  const slots = astInputSlots(op, target)
  const available = slots.find(
    (slot) => slot.value === undefined && slot.expectedType === sourceOutput,
  )
  if (available) return { key: available.key, expectedType: available.expectedType }
  if (isVariadic(op) && expectedVariadicType(op) === sourceOutput)
    return {
      key: `${op === 'strings_union' || op === 'uint64s_union' ? 'items' : 'children'}/${slots.length}`,
      expectedType: sourceOutput,
    }
  return undefined
}

function setAstValueAtPath(
  document: RuleDocument,
  targetPath: string,
  relativePath: string,
  value: JsonObject,
  expectedType: ValueType,
): RuleDocument {
  const next = structuredClone(document)
  const target = getAstAtPath(next, targetPath)
  if (!isObject(target)) return document
  const segments = relativePath.split('/')
  if (
    segments.length > 1 &&
    segments.at(-1) === 'expr' &&
    !isObject(target[segments[0]]) &&
    ['when', 'min', 'max', 'values'].includes(segments[0])
  ) {
    if (expectedType === 'bitmap') return document
    target[segments[0]] = scalarEnvelope(expectedType, neutralAst(expectedType))
  }
  return setPathValue(target, segments, value) ? next : document
}

function setAstChildAtPath(
  document: RuleDocument,
  targetPath: string,
  targetIndex: number,
  sourcePath: string,
): RuleDocument {
  const sourceExpression = getAstAtPath(document, sourcePath)
  if (!isObject(sourceExpression)) return document
  const targetExpression = getAstAtPath(document, targetPath)
  if (!isObject(targetExpression)) return document
  const op = typeof targetExpression.op === 'string' ? targetExpression.op : ''
  const slots = astInputSlots(op, targetExpression)
  let slot = slots[targetIndex]
  if (!slot && isVariadic(op))
    slot = {
      key: `${op === 'strings_union' || op === 'uint64s_union' ? 'items' : 'children'}/${targetIndex}`,
      expectedType: expectedVariadicType(op),
      value: undefined,
    }
  if (!slot) return document
  const sourceClone = structuredClone(sourceExpression) as JsonObject
  return setAstValueAtPath(document, targetPath, slot.key, sourceClone, slot.expectedType)
}

function deleteAstChildAtPath(
  document: RuleDocument,
  targetPath: string,
  targetIndex: number,
): RuleDocument {
  const target = getAstAtPath(document, targetPath)
  if (!isObject(target)) return document
  const op = typeof target.op === 'string' ? target.op : ''
  const slot = astInputSlots(op, target)[targetIndex]
  if (!slot) return document
  const next = structuredClone(document)
  const targetClone = getAstAtPath(next, targetPath) as Record<string, any>
  const segments = slot.key.split('/')
  let cursor: any = targetClone
  for (const segment of segments.slice(0, -1)) {
    if (!cursor || typeof cursor !== 'object') return document
    cursor = cursor[segment]
  }
  if (!cursor || typeof cursor !== 'object') return document
  const final = segments.at(-1)!
  if (Array.isArray(cursor)) cursor.splice(Number(final), 1)
  else delete cursor[final]
  return next
}
