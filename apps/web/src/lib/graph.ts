import type { Connection } from '@xyflow/react'
import type {
  LogicalNodeContract,
  RuleGraphDocument,
  RuleGraphEdge,
  RuleGraphNode,
  ValidationIssue,
  ValueType,
} from '../types'

export interface GraphValidationResult {
  valid: boolean
  errors: ValidationIssue[]
}

const issue = (path: string, message: string): ValidationIssue => ({
  path,
  message,
  source: 'graph',
  severity: 'error',
})

const indexFromHandle = (handle?: string | null): number | undefined => {
  if (!handle?.startsWith('input-')) return undefined
  const value = Number(handle.slice('input-'.length))
  return Number.isInteger(value) && value >= 0 ? value : undefined
}

const findNode = (
  nodes: RuleGraphNode[],
  id: string | null | undefined,
): RuleGraphNode | undefined => nodes.find((node) => node.id === id)

/** Returns true when adding source -> target would make the directed graph cyclic. */
export function wouldCreateCycle(
  nodes: RuleGraphNode[],
  edges: RuleGraphEdge[],
  sourceId: string,
  targetId: string,
): boolean {
  if (sourceId === targetId) return true
  const adjacency = new Map<string, string[]>()
  for (const node of nodes) adjacency.set(node.id, [])
  for (const edge of edges) adjacency.get(edge.source)?.push(edge.target)
  adjacency.get(sourceId)?.push(targetId)

  const visited = new Set<string>()
  const stack = [targetId]
  while (stack.length > 0) {
    const current = stack.pop()
    if (!current || visited.has(current)) continue
    if (current === sourceId) return true
    visited.add(current)
    stack.push(...(adjacency.get(current) ?? []))
  }
  return false
}

export function isConnectionValid(
  connection: Connection,
  nodes: RuleGraphNode[],
  edges: RuleGraphEdge[],
): { valid: boolean; reason?: string } {
  const source = findNode(nodes, connection.source)
  const target = findNode(nodes, connection.target)
  if (!source || !target) return { valid: false, reason: '节点不存在' }
  if (source.id === target.id) return { valid: false, reason: '不能连接到自身' }

  const targetIndex = indexFromHandle(connection.targetHandle)
  const expectedType =
    targetIndex === undefined
      ? undefined
      : (target.data.inputTypes[targetIndex] ??
        (target.data.variadic ? target.data.inputTypes.at(-1) : undefined))
  if (targetIndex === undefined || !expectedType) {
    return { valid: false, reason: '目标端口不存在' }
  }
  if (source.data.outputType !== expectedType) {
    return {
      valid: false,
      reason: `类型不匹配：${source.data.outputType} → ${expectedType}`,
    }
  }

  const targetCapability = target.data.inputTypes
  const incoming = edges.filter((edge) => edge.target === target.id)
  const allowsMultiple =
    target.data.variadic === true ||
    (target.data.maxInputs ?? targetCapability.length) > targetCapability.length
  if (!allowsMultiple && incoming.some((edge) => edge.targetHandle === connection.targetHandle)) {
    return { valid: false, reason: '目标端口已有连接' }
  }
  if (
    incoming.length >= (target.data.maxInputs ?? targetCapability.length) &&
    targetCapability.length > 0
  ) {
    return { valid: false, reason: '目标节点已达到输入基数上限' }
  }
  if (wouldCreateCycle(nodes, edges, source.id, target.id)) {
    return { valid: false, reason: '连接会形成环' }
  }
  return { valid: true }
}

const checkNodeConfig = (
  node: RuleGraphNode,
  contract?: LogicalNodeContract,
): ValidationIssue[] => {
  const config = node.data.config
  const errors: ValidationIssue[] = []
  const path = `/graph/nodes/${node.id}/config`
  const name = typeof config.name === 'string' ? config.name.trim() : ''

  if (node.data.nodeType === 'source.attribute' || node.data.nodeType === 'source.fact') {
    if (!name) errors.push(issue(`${path}/name`, '引用节点必须选择名称'))
    const source = typeof config.source === 'string' ? config.source : ''
    if (node.data.nodeType === 'source.attribute') {
      if (source !== 'seed_attributes' && source !== 'candidate_attributes')
        errors.push(issue(`${path}/source`, 'Attribute 只能读取 *_attributes source'))
    }
    if (name && contract && node.data.nodeType === 'source.attribute') {
      if (!contract.attributes.some((item) => item.name === name))
        errors.push(issue(`${path}/name`, `未在 Contract 中找到「${name}」`))
    } else if (name && contract && node.data.nodeType === 'source.fact') {
      const field = contract.facts.find((item) => item.name === name)
      if (!field) errors.push(issue(`${path}/name`, `未在 Contract 中找到「${name}」`))
      else {
        const allowedSources =
          field.scope === 'tick'
            ? ['tick_facts']
            : field.scope === 'object'
              ? ['seed_facts', 'candidate_facts']
              : ['match_facts']
        if (!allowedSources.includes(source))
          errors.push(issue(`${path}/source`, `Fact source 必须匹配 ${field.scope}-scope`))
      }
    }
  }
  if (node.data.nodeType === 'literal.string' || node.data.nodeType === 'literal.uint64') {
    if (!Array.isArray(config.values)) errors.push(issue(`${path}/values`, '常量必须是数组'))
  }
  if (node.data.nodeType === 'literal.int64' && typeof config.value !== 'number') {
    errors.push(issue(`${path}/value`, 'Int64 常量必须是数字'))
  }
  if (node.data.nodeType === 'literal.bool' && typeof config.value !== 'boolean') {
    errors.push(issue(`${path}/value`, 'Bool 常量必须是 true/false'))
  }
  if (node.data.nodeType === 'prefilter.lookup' && typeof config.index !== 'string') {
    errors.push(issue(`${path}/index`, 'Lookup 必须选择索引'))
  }
  return errors
}

export function validateGraph(
  graph: RuleGraphDocument,
  contract?: LogicalNodeContract,
): GraphValidationResult {
  const errors: ValidationIssue[] = []
  const nodeIds = new Set<string>()
  for (const node of graph.nodes) {
    if (nodeIds.has(node.id)) errors.push(issue(`/graph/nodes/${node.id}`, '节点 ID 必须唯一'))
    nodeIds.add(node.id)
    errors.push(...checkNodeConfig(node, contract))
    if (!node.data.outputType) errors.push(issue(`/graph/nodes/${node.id}`, '节点缺少输出类型'))
  }

  const seenHandles = new Set<string>()
  for (const edge of graph.edges) {
    const source = findNode(graph.nodes, edge.source)
    const target = findNode(graph.nodes, edge.target)
    if (!source || !target) {
      errors.push(issue(`/graph/edges/${edge.id}`, '边引用了不存在的节点'))
      continue
    }
    const connection = isConnectionValid(
      {
        source: edge.source,
        target: edge.target,
        sourceHandle: edge.sourceHandle ?? null,
        targetHandle: edge.targetHandle ?? null,
      },
      graph.nodes,
      graph.edges.filter((candidate) => candidate.id !== edge.id),
    )
    if (!connection.valid)
      errors.push(issue(`/graph/edges/${edge.id}`, connection.reason ?? '连接无效'))
    const targetHandle = `${edge.target}:${edge.targetHandle ?? ''}`
    const targetNode = findNode(graph.nodes, edge.target)
    const allowsMultiple = targetNode
      ? targetNode.data.variadic === true ||
        (targetNode.data.maxInputs ?? targetNode.data.inputTypes.length) >
          targetNode.data.inputTypes.length
      : false
    if (seenHandles.has(targetHandle) && !allowsMultiple)
      errors.push(issue(`/graph/edges/${edge.id}`, '目标端口只能有一条连接'))
    seenHandles.add(targetHandle)
  }

  for (const node of graph.nodes) {
    const requiredInputs = node.data.requiredInputs ?? node.data.inputTypes.length
    if (requiredInputs === 0) continue
    const incoming = graph.edges.filter((edge) => edge.target === node.id)
    for (let index = 0; index < requiredInputs; index += 1) {
      if (!incoming.some((edge) => edge.targetHandle === `input-${index}`))
        errors.push(issue(`/graph/nodes/${node.id}/input-${index}`, '节点缺少输入连接'))
    }
  }
  return { valid: errors.length === 0, errors }
}

export const typeLabel: Record<ValueType, string> = {
  bool: 'Bool',
  int64: 'Int64',
  strings: 'Strings',
  uint64s: 'Uint64s',
  bitmap: 'Bitmap',
}
