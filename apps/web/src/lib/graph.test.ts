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

  it('only accepts the contiguous next handle for variadic nodes', () => {
    const source = makeNode('source', 'bool')
    const variadic: RuleGraphNode = {
      id: 'variadic',
      type: 'rule',
      position: { x: 0, y: 0 },
      data: {
        label: 'AND',
        nodeType: 'logic.and',
        outputType: 'bool',
        inputTypes: [],
        variadic: true,
        variadicInputType: 'bool',
        maxInputs: 16,
        config: { op: 'bool_and', children: [] },
      },
    }
    expect(
      isConnectionValid(
        { source: source.id, target: variadic.id, sourceHandle: 'output', targetHandle: 'input-1' },
        [source, variadic],
        [],
      ),
    ).toMatchObject({ valid: false, reason: expect.stringContaining('不能跳过') })
    expect(
      isConnectionValid(
        { source: source.id, target: variadic.id, sourceHandle: 'output', targetHandle: 'input-0' },
        [source, variadic],
        [],
      ).valid,
    ).toBe(true)

    const firstEdge = {
      id: 'first',
      source: source.id,
      target: variadic.id,
      sourceHandle: 'output',
      targetHandle: 'input-0',
      data: { valueType: 'bool' as const },
    }
    expect(
      isConnectionValid(
        { source: source.id, target: variadic.id, sourceHandle: 'output', targetHandle: 'input-2' },
        [source, variadic],
        [firstEdge],
      ),
    ).toMatchObject({ valid: false, reason: expect.stringContaining('不能跳过') })
  })

  it('validates operation-specific scalar configuration and arity', () => {
    const contains = makeNode('contains', 'bool', ['strings'])
    contains.data.config = { op: 'strings_contains' }
    const empty = makeNode('empty', 'bool', ['strings', 'strings'])
    empty.data.config = { op: 'strings_is_empty' }
    const step = makeNode('step', 'int64', ['int64'])
    step.data.config = { op: 'int64_step', steps: [{ at: 0, value: 1.5 }] }
    const result = validateGraph({ nodes: [contains, empty, step], edges: [] })
    expect(result.errors.some((error) => error.path.endsWith('/needle'))).toBe(true)
    expect(result.errors.some((error) => error.path.endsWith('/inputs'))).toBe(true)
    expect(result.errors.some((error) => error.path.includes('/steps/0'))).toBe(true)
  })
})
