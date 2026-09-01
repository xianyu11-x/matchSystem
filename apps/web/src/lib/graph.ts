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
  if (
    source.data.nodeType === 'prefilter.output' ||
    source.data.nodeType === 'evaluation.join' ||
    source.data.nodeType === 'evaluation.complete'
  )
    return { valid: false, reason: '最终输出节点不能作为连接源' }

  const targetIndex = indexFromHandle(connection.targetHandle)
  const targetInputTypes = target.data.inputTypes
  const variadic = target.data.variadic === true
  const incoming = edges.filter((edge) => edge.target === target.id)
  if (targetIndex === undefined) return { valid: false, reason: '目标端口不存在' }
  if (variadic && targetIndex > targetInputTypes.length)
    return { valid: false, reason: '变长输入必须按顺序连接，不能跳过前面的端口' }
  if (variadic) {
    const occupied = new Set(
      incoming
        .map((edge) => indexFromHandle(edge.targetHandle))
        .filter((index): index is number => index !== undefined),
    )
    let nextIndex = 0
    while (occupied.has(nextIndex)) nextIndex += 1
    if (targetIndex > nextIndex)
      return { valid: false, reason: '变长输入必须按顺序连接，不能跳过前面的端口' }
  }
  const expectedType =
    targetInputTypes[targetIndex] ??
    (variadic && targetIndex === targetInputTypes.length
      ? (target.data.variadicInputType ?? targetInputTypes.at(-1))
      : undefined)
  if (!expectedType) return { valid: false, reason: '目标端口不存在' }
  if (source.data.outputType !== expectedType) {
    return {
      valid: false,
      reason: `类型不匹配：${source.data.outputType} → ${expectedType}`,
    }
  }

  const maxInputs = target.data.maxInputs ?? (variadic ? 16 : targetInputTypes.length)
  const replacesFixedOutput =
    target.data.nodeType === 'prefilter.output' ||
    target.data.nodeType === 'evaluation.join' ||
    target.data.nodeType === 'evaluation.complete'
  if (
    !replacesFixedOutput &&
    incoming.some((edge) => edge.targetHandle === connection.targetHandle)
  ) {
    return { valid: false, reason: '目标端口已有连接' }
  }
  if (!replacesFixedOutput && incoming.length >= maxInputs && maxInputs > 0) {
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
  if (node.data.nodeType === 'prefilter.lookup') {
    if (typeof config.index !== 'string' || !config.index) {
      errors.push(issue(`${path}/index`, 'Lookup 必须选择索引'))
    } else if (contract && !contract.indexes.some((index) => index.name === config.index)) {
      errors.push(issue(`${path}/index`, `未在 Contract 中找到索引「${config.index}」`))
    }
  }
  const op = typeof config.op === 'string' ? config.op : ''
  if (op === 'strings_contains' && typeof config.needle !== 'string')
    errors.push(issue(`${path}/needle`, 'needle 必须是字符串'))
  if (
    op === 'uint64s_contains' &&
    (typeof config.needle !== 'number' || !Number.isInteger(config.needle) || config.needle < 0)
  )
    errors.push(issue(`${path}/needle`, 'needle 必须是非负整数'))
  if (op === 'int64_step') {
    if (!Array.isArray(config.steps) || config.steps.length === 0) {
      errors.push(issue(`${path}/steps`, 'int64_step 至少需要一个 steps 项'))
    } else {
      config.steps.forEach((step, index) => {
        const candidate =
          step && typeof step === 'object' && !Array.isArray(step)
            ? (step as Record<string, unknown>)
            : undefined
        if (!candidate || !Number.isInteger(candidate.at) || !Number.isInteger(candidate.value))
          errors.push(issue(`${path}/steps/${index}`, '每个 step 必须包含整数 at 和 value'))
      })
    }
  }
  if (op === 'strings_ref' || op === 'int64_ref' || op === 'uint64s_ref') {
    const source = typeof config.source === 'string' ? config.source : ''
    const expectedType = op === 'strings_ref' ? 'strings' : op === 'int64_ref' ? 'int64' : 'uint64s'
    const isAttributeSource = source.endsWith('_attributes')
    const fields = isAttributeSource
      ? (contract?.attributes.filter((field) => field.type === expectedType) ?? [])
      : (contract?.facts.filter((field) => {
          const scope =
            source === 'tick_facts' ? 'tick' : source === 'match_facts' ? 'match' : 'object'
          return field.type === expectedType && field.scope === scope
        }) ?? [])
    if (!source || (!isAttributeSource && !source.endsWith('_facts')))
      errors.push(issue(`${path}/source`, '引用节点的 source 必须是 Attributes 或 Facts source'))
    if (typeof config.name !== 'string' || !config.name)
      errors.push(issue(`${path}/name`, '引用节点必须选择名称'))
    else if (contract && !fields.some((field) => field.name === config.name))
      errors.push(
        issue(`${path}/name`, `未找到与 ${source} / ${expectedType} 匹配的字段「${config.name}」`),
      )
  }
  const schemaInputTypes: Record<string, ValueType[]> = {
    strings_is_empty: ['strings'],
    strings_contains: ['strings'],
    uint64s_is_empty: ['uint64s'],
    uint64s_contains: ['uint64s'],
    int64_step: ['int64'],
  }
  const expectedInputTypes = schemaInputTypes[op]
  if (expectedInputTypes) {
    const variadic =
      op === 'bool_and' ||
      op === 'bool_or' ||
      op === 'and' ||
      op === 'or' ||
      op === 'strings_union' ||
      op === 'uint64s_union'
    if (
      !variadic &&
      (node.data.variadic ||
        node.data.inputTypes.length !== expectedInputTypes.length ||
        node.data.inputTypes.some((type, index) => type !== expectedInputTypes[index]))
    )
      errors.push(
        issue(`${path}/inputs`, `${op} 必须严格使用 ${expectedInputTypes.length} 个指定类型输入`),
      )
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
    if (seenHandles.has(targetHandle))
      errors.push(issue(`/graph/edges/${edge.id}`, '目标端口只能有一条连接'))
    seenHandles.add(targetHandle)
  }

  for (const node of graph.nodes) {
    if (!node.data.variadic) continue
    const indexes = graph.edges
      .filter((edge) => edge.target === node.id)
      .map((edge) => indexFromHandle(edge.targetHandle))
      .filter((index): index is number => index !== undefined)
      .sort((left, right) => left - right)
    for (let index = 0; index < indexes.length; index += 1) {
      if (indexes[index] !== index) {
        errors.push(
          issue(`/graph/nodes/${node.id}`, '变长输入端口必须连续，不能存在跳号或重复端口连接'),
        )
        break
      }
    }
  }

  for (const edge of graph.edges) {
    if (
      graph.nodes.some(
        (node) =>
          node.id === edge.source &&
          node.id.startsWith('root-') &&
          (node.data.nodeType === 'prefilter.output' ||
            node.data.nodeType === 'evaluation.join' ||
            node.data.nodeType === 'evaluation.complete'),
      )
    )
      errors.push(issue(`/graph/edges/${edge.id}`, '最终输出节点不能作为连接源'))
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

  // A palette node is a temporary workspace node until it reaches one of the
  // fixed outputs. Do not silently accept an orphan that would be omitted from
  // the portable AST on save.
  const outputIds = new Set(
    graph.nodes
      .filter(
        (node) =>
          node.id.startsWith('root-') &&
          (node.data.nodeType === 'prefilter.output' ||
            node.data.nodeType === 'evaluation.join' ||
            node.data.nodeType === 'evaluation.complete'),
      )
      .map((node) => node.id),
  )
  const incomingByTarget = new Map<string, string[]>()
  for (const edge of graph.edges) {
    const incoming = incomingByTarget.get(edge.target) ?? []
    incoming.push(edge.source)
    incomingByTarget.set(edge.target, incoming)
  }
  const reachesOutput = new Set(outputIds)
  const pending = [...outputIds]
  while (pending.length > 0) {
    const target = pending.pop()!
    for (const source of incomingByTarget.get(target) ?? []) {
      if (reachesOutput.has(source)) continue
      reachesOutput.add(source)
      pending.push(source)
    }
  }
  if (outputIds.size > 0)
    for (const node of graph.nodes) {
      if (!node.data.astPath && !outputIds.has(node.id) && !reachesOutput.has(node.id))
        errors.push(issue(`/graph/nodes/${node.id}`, '临时节点尚未连接到最终输出'))
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
