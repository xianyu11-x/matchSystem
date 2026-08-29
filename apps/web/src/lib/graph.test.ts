import { describe, expect, it } from 'vitest'
import { isConnectionValid, validateGraph, wouldCreateCycle } from './graph'
import type { RuleGraphDocument, RuleGraphNode } from '../types'

const makeNode = (
  id: string,
  outputType: RuleGraphNode['data']['outputType'],
  inputTypes: RuleGraphNode['data']['inputTypes'] = [],
): RuleGraphNode => ({
  id,
  type: 'rule',
  position: { x: 0, y: 0 },
  data: {
    label: id,
    nodeType: inputTypes.length > 0 ? 'compare.strings' : 'literal.string',
    outputType,
    inputTypes,
    config: inputTypes.length > 0 ? { op: 'strings_eq' } : { values: ['x'] },
  },
})

describe('rule graph validation', () => {
  it('rejects a mismatched typed port', () => {
    const nodes = [makeNode('source', 'strings'), makeNode('target', 'bool', ['int64'])]
    const result = isConnectionValid(
      { source: 'source', target: 'target', sourceHandle: 'output', targetHandle: 'input-0' },
      nodes,
      [],
    )
    expect(result.valid).toBe(false)
    expect(result.reason).toContain('类型不匹配')
  })

  it('rejects an edge that closes a cycle', () => {
    const nodes = [makeNode('a', 'bool', ['bool']), makeNode('b', 'bool', ['bool'])]
    const edges = [
      {
        id: 'a-b',
        source: 'a',
        target: 'b',
        targetHandle: 'input-0',
        data: { valueType: 'bool' as const },
      },
    ]
    expect(wouldCreateCycle(nodes, edges, 'b', 'a')).toBe(true)
    expect(
      isConnectionValid(
        { source: 'b', target: 'a', sourceHandle: 'output', targetHandle: 'input-0' },
        nodes,
        edges,
      ).valid,
    ).toBe(false)
  })

  it('accepts a typed acyclic graph and catches disconnected inputs', () => {
    const nodes = [makeNode('source', 'strings'), makeNode('compare', 'bool', ['strings'])]
    const graph: RuleGraphDocument = {
      nodes,
      edges: [
        {
          id: 'source-compare',
          source: 'source',
          target: 'compare',
          targetHandle: 'input-0',
          data: { valueType: 'strings' },
        },
      ],
    }
    expect(validateGraph(graph).valid).toBe(true)
    expect(
      validateGraph({ nodes: [makeNode('compare', 'bool', ['strings'])], edges: [] }).errors[0]
        .message,
    ).toContain('缺少输入')
  })
})
