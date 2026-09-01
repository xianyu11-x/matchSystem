import { create } from 'zustand'
import { astInputSlots, buildRuleGraph } from './graphBuilder'
import { isConnectionValid } from './graph'
import type {
  JsonObject,
  JsonValue,
  RuleDocument,
  RuleGraphEdge,
  RuleGraphDocument,
  RuleGraphNode,
  ValueType,
} from '../types'

type RulesTab = 'graph' | 'contract' | 'prefilter' | 'evaluation' | 'facts'

interface RuleEditorState {
  document?: RuleDocument
  /** In-memory graph snapshots survive Rules page unmounts and rule switches. */
  graphCache: Record<string, RuleGraphDocument>
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
  addNode: (node: RuleGraphNode, options?: { standalone?: boolean }) => void
  removeNode: (nodeId: string) => void
  clearNotice: () => void
  resetDirty: () => void
}

export const useRuleStore = create<RuleEditorState>((set) => ({
  graphCache: {},
  activeTab: 'graph',
  dirty: false,
  setDocument: (document) =>
    set((state) => {
      const normalizedDocument = normalizeVariadicDocument(document)
      const key = editorKey(normalizedDocument)
      const sameRule = state.document !== undefined && editorKey(state.document) === key
      const previousGraph = sameRule ? state.document?.graph : state.graphCache[key]
      const graph = buildRuleGraph(normalizedDocument, previousGraph)
      const nextDocument = { ...normalizedDocument, graph }
      const selectedNodeId =
        sameRule &&
        state.selectedNodeId &&
        graph.nodes.some((node) => node.id === state.selectedNodeId)
          ? state.selectedNodeId
          : (graph.nodes.find((node) => node.data.astPath)?.id ?? graph.nodes[0]?.id)
      return {
        document: nextDocument,
        graphCache: { ...state.graphCache, [key]: graph },
        selectedNodeId,
        dirty: false,
        notice: undefined,
      }
    }),
  importDocument: (document) =>
    set((state) => {
      const normalizedDocument = normalizeVariadicDocument(document)
      const key = editorKey(normalizedDocument)
      const previousGraph = state.graphCache[key]
      const graph = buildRuleGraph(normalizedDocument, previousGraph)
      const nextDocument = { ...normalizedDocument, graph }
      return {
        document: nextDocument,
        graphCache: { ...state.graphCache, [key]: graph },
        selectedNodeId: graph.nodes.find((node) => node.data.astPath)?.id ?? graph.nodes[0]?.id,
        dirty: true,
        notice: 'JSON 已导入到当前规则；保存前仍会执行本地和 Go 双重校验。',
      }
    }),
  setActiveTab: (activeTab) => set({ activeTab }),
  selectNode: (selectedNodeId) => set({ selectedNodeId, notice: undefined }),
  setGraph: (nodes, edges) =>
    set((state) => {
      if (!state.document) return state
      const graph = syncStandaloneVariadicInputs({ nodes, edges })
      return {
        document: { ...state.document, graph },
        graphCache: { ...state.graphCache, [editorKey(state.document)]: graph },
      }
    }),
  connectGraph: (sourceId, targetId, targetIndex, edge) =>
    set((state) => {
      if (!state.document) return state
      const source = state.document.graph.nodes.find((node) => node.id === sourceId)
      const target = state.document.graph.nodes.find((node) => node.id === targetId)
      if (!source || !target) {
        return { notice: '连接引用了不存在的节点。' }
      }
      const connection = isConnectionValid(
        {
          source: sourceId,
          target: targetId,
          sourceHandle: edge.sourceHandle ?? 'output',
          targetHandle: edge.targetHandle ?? `input-${targetIndex}`,
        },
        state.document.graph.nodes,
        state.document.graph.edges.filter((item) => item.id !== edge.id),
      )
      if (!connection.valid) return { notice: connection.reason ?? '连接无效。' }
      const sourceAst = source.data.astPath
        ? getAstAtPath(state.document, source.data.astPath)
        : graphNodeToAst(state.document, source, state.document.graph)
      if (!isObject(sourceAst)) return { notice: '该节点尚未形成可写入规则的表达式。' }
      const rootPath = evaluationRootExpressionPath(target)
      const nextDocument = rootPath
        ? replaceAstAtPath(state.document, rootPath, structuredClone(sourceAst) as JsonObject)
        : target.data.astPath
          ? setAstChildValueAtPath(
              state.document,
              target.data.astPath,
              targetIndex,
              structuredClone(sourceAst) as JsonObject,
            )
          : undefined
      if (nextDocument === undefined) {
        const graph = syncStandaloneVariadicInputs({
          nodes: state.document.graph.nodes,
          edges: [...state.document.graph.edges.filter((item) => item.id !== edge.id), edge],
        })
        return {
          document: { ...state.document, graph },
          graphCache: { ...state.graphCache, [editorKey(state.document)]: graph },
          dirty: true,
          selectedNodeId: target.id,
          notice: undefined,
        }
      }
      if (nextDocument === state.document)
        return { notice: '该连接无法写回 AST，请先补齐目标表达式结构。' }
      const previousGraph =
        source.data.astPath === undefined
          ? graphWithoutNodes(
              state.document.graph,
              standaloneAncestors(state.document.graph, source.id),
            )
          : state.document.graph
      const graph = buildRuleGraph(nextDocument, previousGraph)
      const selectedNodeId = rootPath
        ? graph.nodes.find((node) => node.data.astPath === rootPath)?.id
        : target.data.astPath
          ? graph.nodes.find((node) => node.data.astPath === target.data.astPath)?.id
          : target.id
      return {
        document: { ...nextDocument, graph },
        graphCache: { ...state.graphCache, [editorKey(nextDocument)]: graph },
        dirty: true,
        selectedNodeId,
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
      const rootPath = target ? evaluationRootExpressionPath(target) : undefined
      if ((!target?.data.astPath && !rootPath) || targetIndex === undefined) {
        const graph = syncStandaloneVariadicInputs({
          nodes: state.document.graph.nodes,
          edges: state.document.graph.edges.filter((item) => item.id !== edgeId),
        })
        return {
          document: { ...state.document, graph },
          graphCache: { ...state.graphCache, [editorKey(state.document)]: graph },
          dirty: true,
          notice: undefined,
        }
      }
      const nextDocument = rootPath
        ? deleteAstAtPath(state.document, rootPath)
        : deleteAstChildAtPath(state.document, target!.data.astPath!, targetIndex)
      if (nextDocument === state.document) return { notice: '连接删除失败，AST 未发生改变。' }
      const graph = buildRuleGraph(nextDocument, {
        nodes: state.document.graph.nodes,
        edges: state.document.graph.edges.filter((item) => item.id !== edgeId),
      })
      return {
        document: { ...nextDocument, graph },
        graphCache: { ...state.graphCache, [editorKey(nextDocument)]: graph },
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
        const previousGraph = graphWithNodeData(state.document.graph, nodeId, data)
        const graph = buildRuleGraph(nextDocument, previousGraph)
        return {
          document: { ...nextDocument, graph },
          graphCache: { ...state.graphCache, [editorKey(nextDocument)]: graph },
          dirty: true,
          notice: undefined,
        }
      }
      const nodes = state.document.graph.nodes.map((node) =>
        node.id === nodeId ? { ...node, data: { ...node.data, ...data } } : node,
      )
      const graph = { ...state.document.graph, nodes }
      return {
        document: { ...state.document, graph },
        graphCache: { ...state.graphCache, [editorKey(state.document)]: graph },
        dirty: true,
        notice: undefined,
      }
    }),
  setEnvelope: (kind, value) =>
    set((state) => {
      if (!state.document) return state
      const nextDocument = normalizeVariadicDocument({
        ...state.document,
        [kind]: value,
      } as RuleDocument)
      const graph = buildRuleGraph(nextDocument, state.document.graph)
      return {
        document: { ...nextDocument, graph },
        graphCache: { ...state.graphCache, [editorKey(nextDocument)]: graph },
        dirty: true,
        selectedNodeId:
          state.selectedNodeId && graph.nodes.some((node) => node.id === state.selectedNodeId)
            ? state.selectedNodeId
            : (graph.nodes.find((node) => node.data.astPath)?.id ?? graph.nodes[0]?.id),
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
        const previousGraph = graphWithNodeData(state.document.graph, nodeId, { config })
        const graph = buildRuleGraph(nextDocument, previousGraph)
        return {
          document: { ...nextDocument, graph },
          graphCache: { ...state.graphCache, [editorKey(nextDocument)]: graph },
          dirty: true,
          notice: undefined,
        }
      }
      return { notice: '该节点没有可写回的 AST 路径。' }
    }),
  addNode: (node, options) =>
    set((state) => {
      if (!state.document) return state
      // Palette drops create an independent node first. Connecting it to an
      // AST/output node is a separate, explicit action on the canvas.
      const standalone = options?.standalone === true || state.selectedNodeId === undefined
      if (standalone) {
        if (state.document.graph.nodes.some((item) => item.id === node.id))
          return { notice: '节点 ID 已存在，请重新拖入该节点。' }
        const graph = {
          nodes: [
            ...state.document.graph.nodes,
            { ...node, data: { ...node.data, comment: node.data.comment ?? '' } },
          ],
          edges: state.document.graph.edges,
        }
        return {
          document: { ...state.document, graph },
          graphCache: { ...state.graphCache, [editorKey(state.document)]: graph },
          dirty: true,
          selectedNodeId: node.id,
          notice: undefined,
        }
      }
      const target = state.document.graph.nodes.find((item) => item.id === state.selectedNodeId)
      if (!target) return { notice: '请先选择一个表达式节点或 Evaluation 根出口。' }
      const ast = graphNodeToAst(state.document, node, state.document.graph)
      if (!ast) return { notice: '该 palette 节点不是可写入 AST 的表达式节点。' }
      const rootPath = evaluationRootExpressionPath(target)
      if (rootPath) {
        const nextDocument = replaceAstAtPath(state.document, rootPath, ast)
        const nextGraph = buildRuleGraph(nextDocument, state.document.graph)
        return {
          document: { ...nextDocument, graph: nextGraph },
          graphCache: { ...state.graphCache, [editorKey(nextDocument)]: nextGraph },
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
      const nextGraph = buildRuleGraph(nextDocument, state.document.graph)
      return {
        document: { ...nextDocument, graph: nextGraph },
        graphCache: { ...state.graphCache, [editorKey(nextDocument)]: nextGraph },
        dirty: true,
        selectedNodeId: target.id,
        notice: undefined,
      }
    }),
  removeNode: (nodeId) =>
    set((state) => {
      if (!state.document) return state
      const node = state.document.graph.nodes.find((item) => item.id === nodeId)
      if (!node) return state
      if (!node.data.astPath && !isFixedOutputNode(node)) {
        const graph = syncStandaloneVariadicInputs({
          nodes: state.document.graph.nodes.filter((item) => item.id !== nodeId),
          edges: state.document.graph.edges.filter(
            (edge) => edge.source !== nodeId && edge.target !== nodeId,
          ),
        })
        return {
          document: { ...state.document, graph },
          graphCache: { ...state.graphCache, [editorKey(state.document)]: graph },
          dirty: true,
          selectedNodeId: undefined,
          notice: undefined,
        }
      }
      if (!node.data.astPath)
        return { notice: 'Evaluation 根节点是 envelope 的固定出口，不能从图中删除。' }
      const nextDocument = deleteAstAtPath(state.document, node.data.astPath)
      if (nextDocument === state.document) return { notice: '节点删除失败，AST 未发生改变。' }
      const graph = buildRuleGraph(nextDocument, state.document.graph)
      return {
        document: { ...nextDocument, graph },
        graphCache: { ...state.graphCache, [editorKey(nextDocument)]: graph },
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
  if (node.data.nodeType === 'prefilter.output') return '/prefilter/bitmap/expr'
  return undefined
}

const editorKey = (document: Pick<RuleDocument, 'ruleKey' | 'placementId'>): string =>
  JSON.stringify([document.ruleKey, document.placementId])

function graphWithNodeData(
  graph: RuleGraphDocument,
  nodeId: string,
  data: Partial<RuleGraphNode['data']>,
): RuleGraphDocument {
  return {
    ...graph,
    nodes: graph.nodes.map((node) =>
      node.id === nodeId ? { ...node, data: { ...node.data, ...data } } : node,
    ),
  }
}

function graphWithoutNodes(graph: RuleGraphDocument, nodeIds: Set<string>): RuleGraphDocument {
  return {
    nodes: graph.nodes.filter((node) => !nodeIds.has(node.id)),
    edges: graph.edges.filter((edge) => !nodeIds.has(edge.source) && !nodeIds.has(edge.target)),
  }
}

function syncStandaloneVariadicInputs(graph: RuleGraphDocument): RuleGraphDocument {
  const standaloneVariadicIds = new Set(
    graph.nodes.filter((node) => !node.data.astPath && node.data.variadic).map((node) => node.id),
  )
  const normalizedHandleByEdge = new Map<string, string>()
  for (const nodeId of standaloneVariadicIds) {
    graph.edges
      .filter((edge) => edge.target === nodeId && handleIndex(edge.targetHandle) !== undefined)
      .sort((left, right) => handleIndex(left.targetHandle)! - handleIndex(right.targetHandle)!)
      .forEach((edge, index) => normalizedHandleByEdge.set(edge.id, `input-${index}`))
  }
  return {
    ...graph,
    edges: graph.edges.map((edge) => {
      const targetHandle = normalizedHandleByEdge.get(edge.id)
      return targetHandle ? { ...edge, targetHandle } : edge
    }),
    nodes: graph.nodes.map((node) => {
      if (node.data.astPath || !node.data.variadic) return node
      const count = graph.edges.filter(
        (edge) => edge.target === node.id && handleIndex(edge.targetHandle) !== undefined,
      ).length
      const inputType = node.data.variadicInputType ?? node.data.inputTypes.at(-1)
      return inputType
        ? {
            ...node,
            data: {
              ...node.data,
              inputTypes: Array.from({ length: count }, () => inputType),
            },
          }
        : node
    }),
  }
}

function standaloneAncestors(graph: RuleGraphDocument, nodeId: string): Set<string> {
  const result = new Set<string>([nodeId])
  const pending = [nodeId]
  while (pending.length > 0) {
    const target = pending.pop()!
    for (const edge of graph.edges) {
      if (edge.target !== target || result.has(edge.source)) continue
      const source = graph.nodes.find((node) => node.id === edge.source)
      if (!source?.data.astPath) {
        result.add(edge.source)
        pending.push(edge.source)
      }
    }
  }
  return result
}

function isFixedOutputNode(node: RuleGraphNode): boolean {
  return (
    node.data.nodeType === 'prefilter.output' ||
    node.data.nodeType === 'evaluation.join' ||
    node.data.nodeType === 'evaluation.complete'
  )
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
    const nextSegment = segments[index + 1]
    if (Array.isArray(cursor)) {
      const arrayIndex = Number(segment)
      if (!Number.isInteger(arrayIndex) || arrayIndex < 0 || arrayIndex > cursor.length)
        return false
      const current = cursor[arrayIndex]
      if (!isObject(current) && !Array.isArray(current))
        cursor[arrayIndex] = /^\d+$/.test(nextSegment) ? [] : {}
      cursor = cursor[arrayIndex]
    } else {
      const current = cursor[segment]
      if (!isObject(current) && !Array.isArray(current))
        cursor[segment] = /^\d+$/.test(nextSegment) ? [] : {}
      cursor = cursor[segment]
    }
  }
  const finalSegment = segments.at(-1)!
  if (Array.isArray(cursor)) {
    const index = Number(finalSegment)
    if (!Number.isInteger(index) || index < 0 || index > cursor.length) return false
    cursor[index] = value
  } else cursor[finalSegment] = value
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
  for (const key of Object.keys(target)) delete target[key]
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

function contiguousList(value: unknown): unknown[] {
  if (!Array.isArray(value)) return []
  const result: unknown[] = []
  for (const item of value) {
    if (item === undefined || item === null) break
    result.push(item)
  }
  return result
}

function normalizeVariadicValue(value: unknown): unknown {
  if (Array.isArray(value)) return contiguousList(value).map(normalizeVariadicValue)
  if (!isObject(value)) return value
  const result = value
  const op = typeof result.op === 'string' ? result.op : ''
  if (isVariadic(op)) {
    const key = variadicKey(op)
    result[key] = contiguousList(result[key])
  }
  for (const [key, child] of Object.entries(result)) {
    if (key === 'children' || key === 'items') {
      result[key] = Array.isArray(child) ? child.map(normalizeVariadicValue) : child
    } else if (child && typeof child === 'object') {
      result[key] = normalizeVariadicValue(child)
    }
  }
  return result
}

function normalizeVariadicDocument(document: RuleDocument): RuleDocument {
  const result = structuredClone(document)
  return normalizeVariadicValue(result) as RuleDocument
}

function variadicKey(op: string): 'children' | 'items' {
  return op === 'strings_union' || op === 'uint64s_union' ? 'items' : 'children'
}

function setRawAstSlot(
  target: Record<string, any>,
  relativePath: string,
  value: JsonObject,
  expectedType: ValueType,
): boolean {
  const segments = relativePath.split('/')
  if (
    segments.length > 1 &&
    segments.at(-1) === 'expr' &&
    !isObject(target[segments[0]]) &&
    ['when', 'min', 'max', 'values'].includes(segments[0])
  ) {
    if (expectedType === 'bitmap') return false
    target[segments[0]] = scalarEnvelope(expectedType, neutralAst(expectedType))
  }
  return setPathValue(target, segments, value)
}

function graphNodeToAst(
  document: RuleDocument,
  node: RuleGraphNode,
  graph: RuleGraphDocument,
  visiting = new Set<string>(),
): JsonObject | undefined {
  const config = structuredClone(node.data.config) as Record<string, any>
  const type = node.data.nodeType
  if (type === 'prefilter.output' || type === 'evaluation.join' || type === 'evaluation.complete')
    return undefined
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

  const op = typeof config.op === 'string' ? config.op : ''
  if (isVariadic(op)) config[variadicKey(op)] = contiguousList(config[variadicKey(op)])
  if (op === 'strings_contains' && typeof config.needle !== 'string') config.needle = ''
  if (op === 'uint64s_contains' && !Number.isInteger(config.needle)) config.needle = 0
  if (op === 'int64_step' && !Array.isArray(config.steps)) config.steps = [{ at: 0, value: 0 }]

  if (visiting.has(node.id)) return undefined
  visiting.add(node.id)
  let invalidInput = false
  const incoming = graph.edges
    .filter((edge) => edge.target === node.id)
    .filter((edge) => handleIndex(edge.targetHandle) !== undefined)
    .sort((left, right) => handleIndex(left.targetHandle)! - handleIndex(right.targetHandle)!)
  for (const edge of incoming) {
    const targetIndex = handleIndex(edge.targetHandle)
    if (targetIndex === undefined) continue
    const source = graph.nodes.find((candidate) => candidate.id === edge.source)
    if (!source) continue
    const sourceAst = source.data.astPath
      ? getAstAtPath(document, source.data.astPath)
      : graphNodeToAst(document, source, graph, visiting)
    if (!isObject(sourceAst)) continue
    const slots = astInputSlots(op, config)
    let slot = slots[targetIndex]
    if (!slot && isVariadic(op) && targetIndex === slots.length)
      slot = {
        key: `${variadicKey(op)}/${targetIndex}`,
        expectedType: expectedVariadicType(op),
        value: undefined,
      }
    if (
      !slot ||
      !setRawAstSlot(config, slot.key, structuredClone(sourceAst) as JsonObject, slot.expectedType)
    )
      invalidInput = true
  }
  visiting.delete(node.id)
  if (invalidInput) return undefined
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
      key: `${variadicKey(op)}/${slots.length}`,
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
  const op = typeof target.op === 'string' ? target.op : ''
  if (isVariadic(op)) target[variadicKey(op)] = contiguousList(target[variadicKey(op)])
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

function setAstChildValueAtPath(
  document: RuleDocument,
  targetPath: string,
  targetIndex: number,
  sourceExpression: JsonObject,
): RuleDocument {
  const targetExpression = getAstAtPath(document, targetPath)
  if (!isObject(targetExpression)) return document
  const op = typeof targetExpression.op === 'string' ? targetExpression.op : ''
  const slots = astInputSlots(op, targetExpression)
  let slot = slots[targetIndex]
  if (!slot && isVariadic(op) && targetIndex === slots.length)
    slot = {
      key: `${variadicKey(op)}/${targetIndex}`,
      expectedType: expectedVariadicType(op),
      value: undefined,
    }
  if (!slot) return document
  return setAstValueAtPath(
    document,
    targetPath,
    slot.key,
    structuredClone(sourceExpression) as JsonObject,
    slot.expectedType,
  )
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
  if (!slot && isVariadic(op) && targetIndex === slots.length)
    slot = {
      key: `${variadicKey(op)}/${targetIndex}`,
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
  if (isVariadic(op)) {
    const key = variadicKey(op)
    targetClone[key] = contiguousList(targetClone[key])
  }
  return next
}
