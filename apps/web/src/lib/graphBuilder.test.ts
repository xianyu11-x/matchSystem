import { describe, expect, it } from 'vitest'
import { demoRule } from './mockData'
import { astInputSlots, buildRuleGraph } from './graphBuilder'
import { useRuleStore } from './ruleStore'
import { scenarioPayload } from './api'
import type {
  JsonObject,
  JsonValue,
  RuleDocument,
  RuleGraphEdge,
  RuleGraphNode,
  Scenario,
} from '../types'

describe('AST and graph bridge', () => {
  it('creates typed nodes for envelope roots and preserves AST paths', () => {
    const graph = buildRuleGraph(demoRule)
    expect(graph.nodes.some((node) => node.id.includes('prefilter-bitmap-expr'))).toBe(true)
    expect(graph.nodes.some((node) => node.id === 'root-canJoin')).toBe(true)
    expect(graph.edges.every((edge) => edge.data?.valueType)).toBe(true)
  })

  it('keeps an unknown operation visible as a generic node', () => {
    const rule = structuredClone(demoRule)
    rule.evaluation.canJoin.expr = { op: 'future_scalar_op', value: 3 }
    const graph = buildRuleGraph(rule)
    const unknown = graph.nodes.find((node) => node.data.astPath === '/evaluation/canJoin/expr')
    expect(unknown?.data.nodeType).toBe('expression.generic')
    expect(unknown?.data.config.op).toBe('future_scalar_op')
  })

  it('writes inspector configuration back to the source AST', () => {
    const graph = buildRuleGraph(demoRule)
    const expression = graph.nodes.find((node) => node.data.astPath === '/evaluation/canJoin/expr')
    expect(expression).toBeDefined()
    useRuleStore.getState().setDocument({ ...structuredClone(demoRule), graph })
    useRuleStore
      .getState()
      .updateNodeData(expression!.id, { config: { op: 'bool_literal', value: false } })
    const next = useRuleStore.getState().document!
    expect(next.evaluation.canJoin.expr).toMatchObject({ op: 'bool_literal', value: false })
  })

  it('visualizes every int64_clamp operand and uses the uint64s operator prefix', () => {
    const rule = structuredClone(demoRule)
    rule.evaluation.canJoin.expr = {
      op: 'uint64s_eq',
      values: {
        op: 'uint64s_union',
        items: [
          { op: 'uint64s_literal', values: [1] },
          { op: 'uint64s_literal', values: [2] },
        ],
      },
      other: { op: 'uint64s_literal', values: [3] },
    }
    const graph = buildRuleGraph(rule)
    const uintCompare = graph.nodes.find((node) => node.data.astPath === '/evaluation/canJoin/expr')
    expect(uintCompare?.data.nodeType).toBe('compare.uint64')
    expect(uintCompare?.data.outputType).toBe('bool')

    rule.evaluation.canJoin.expr = {
      op: 'int64_gte',
      left: {
        op: 'int64_clamp',
        value: { op: 'int64_literal', value: 5 },
        min: { op: 'int64_literal', value: 0 },
        max: { op: 'int64_literal', value: 10 },
      },
      right: { op: 'int64_literal', value: 1 },
    }
    const clampGraph = buildRuleGraph(rule)
    const clamp = clampGraph.nodes.find(
      (node) => node.data.astPath === '/evaluation/canJoin/expr/left',
    )
    expect(clamp?.data.inputTypes).toEqual(['int64', 'int64', 'int64'])
    expect(
      clampGraph.edges.filter((edge) => edge.target === clamp?.id).map((edge) => edge.targetHandle),
    ).toEqual(['input-0', 'input-1', 'input-2'])
  })

  it('round-trips a graph edit into the real PUT /scenario JSON shape', () => {
    const rule = structuredClone(demoRule)
    rule.ruleKey = 'ranked/1'
    rule.apiRule = { namespace: 'ranked', ruleId: 1 }
    const graph = buildRuleGraph(rule)
    const document = { ...rule, graph }
    useRuleStore.getState().setDocument(document)
    const expression = graph.nodes.find((node) => node.data.astPath === '/evaluation/canJoin/expr')!
    useRuleStore.getState().updateNodeData(expression.id, {
      config: { op: 'bool_literal', value: false },
    })
    const edited = useRuleStore.getState().document!
    const scenario: Scenario = {
      scenarioId: 'scenario-real',
      name: 'real',
      updatedAt: '',
      activeRuleKey: edited.ruleKey,
      rules: [
        {
          ruleKey: edited.ruleKey,
          placementId: edited.placementId,
          displayName: edited.ruleKey,
          enabled: true,
          contract: edited.contract,
          prefilter: edited.prefilter,
          evaluation: rule.evaluation,
        },
      ],
      tickFacts: {},
      rawScenario: {
        schemaVersion: 'simulator-scenario/v1',
        physicalNodes: [],
        rules: [
          {
            logicalNode: {
              rule: { namespace: 'ranked', ruleId: 1 },
              placementId: edited.placementId,
            },
            enabled: true,
            rule: {
              schemaVersion: 'match-rule/v1',
              ruleKey: { namespace: 'ranked', ruleId: 1 },
              contract: edited.contract,
              prefilter: edited.prefilter,
              evaluation: rule.evaluation,
              scoring: edited.scoring,
              seedSelection: edited.seedSelection,
              runtime: edited.runtime,
            },
          } as unknown as JsonValue,
        ],
      },
    }
    const payload = scenarioPayload(scenario, edited) as Record<string, unknown>
    const rules = payload.rules as Array<Record<string, unknown>>
    const aggregate = rules[0].rule as Record<string, unknown>
    expect(aggregate.evaluation as Record<string, unknown>).toMatchObject({
      canJoin: { expr: { op: 'bool_literal', value: false } },
    })
    expect(
      edited.graph.nodes.some((node) => node.data.astPath === '/evaluation/canJoin/expr'),
    ).toBe(true)
  })

  it('does not leave palette add or graph delete as graph-only state', () => {
    const rule = structuredClone(demoRule)
    const graph = buildRuleGraph(rule)
    useRuleStore.getState().setDocument({ ...rule, graph })
    const expression = graph.nodes.find((node) => node.data.astPath === '/evaluation/canJoin/expr')!
    useRuleStore.getState().selectNode(expression.id)
    const paletteNode: RuleGraphNode = {
      id: 'palette-bool',
      type: 'rule',
      position: { x: 0, y: 0 },
      data: {
        label: 'bool',
        nodeType: 'literal.bool',
        outputType: 'bool',
        inputTypes: [],
        config: { value: true },
      },
    }
    useRuleStore.getState().addNode(paletteNode)
    const added = useRuleStore.getState().document!
    expect(added.evaluation.canJoin.expr).toMatchObject({ op: 'bool_literal', value: true })
    expect(added.graph.nodes.some((node) => node.id === paletteNode.id)).toBe(false)

    const addedNode = added.graph.nodes.find(
      (node) => node.data.astPath === '/evaluation/canJoin/expr',
    )!
    useRuleStore.getState().removeNode(addedNode.id)
    const removed = useRuleStore.getState().document!
    expect(
      (removed.evaluation.canJoin as RuleDocument['evaluation']['canJoin']).expr,
    ).toBeUndefined()
    expect(removed.graph.edges.some((edge) => edge.target === 'root-canJoin')).toBe(false)
  })

  it('allows a palette node to replace an Evaluation root expression', () => {
    const rule = structuredClone(demoRule)
    const graph = buildRuleGraph(rule)
    useRuleStore.getState().setDocument({ ...rule, graph })
    useRuleStore.getState().selectNode('root-canJoin')
    const paletteNode: RuleGraphNode = {
      id: 'palette-root-bool',
      type: 'rule',
      position: { x: 0, y: 0 },
      data: {
        label: 'bool',
        nodeType: 'literal.bool',
        outputType: 'bool',
        inputTypes: [],
        config: { value: false },
      },
    }
    useRuleStore.getState().addNode(paletteNode)
    const next = useRuleStore.getState()
    expect(next.document?.evaluation.canJoin.expr).toMatchObject({
      op: 'bool_literal',
      value: false,
    })
    expect(next.selectedNodeId).toBe('ast--evaluation-canJoin-expr')
  })

  it('does not mark React Flow visual changes as a rule edit', () => {
    const rule = structuredClone(demoRule)
    const graph = buildRuleGraph(rule)
    useRuleStore.getState().setDocument({ ...rule, graph })
    useRuleStore.getState().setGraph(
      graph.nodes.map((node) => ({ ...node, selected: node.id === graph.nodes[0].id })),
      graph.edges,
    )
    expect(useRuleStore.getState().dirty).toBe(false)
  })

  it('adds a visible Prefilter final output and lays each tree left-to-right', () => {
    const graph = buildRuleGraph(demoRule)
    const output = graph.nodes.find((node) => node.id === 'root-prefilter')
    const expression = graph.nodes.find((node) => node.data.astPath === '/prefilter/bitmap/expr')
    expect(output?.data.nodeType).toBe('prefilter.output')
    expect(graph.edges.some((edge) => edge.target === output?.id)).toBe(true)
    expect(expression?.position.x).toBeLessThan(output?.position.x ?? 0)
  })

  it('keeps palette nodes independent until an explicit connection', () => {
    const rule = structuredClone(demoRule)
    const graph = buildRuleGraph(rule)
    useRuleStore.getState().setDocument({ ...rule, graph })
    const source: RuleGraphNode = {
      id: 'standalone-prefilter-source',
      type: 'rule',
      position: { x: 30, y: 30 },
      data: {
        label: 'Bitmap source',
        nodeType: 'prefilter.generic',
        outputType: 'bitmap',
        inputTypes: [],
        config: { op: 'none' },
        comment: '独立配置后再接入最终结果',
      },
    }
    const exclude: RuleGraphNode = {
      id: 'standalone-prefilter-exclude',
      type: 'rule',
      position: { x: 280, y: 30 },
      data: {
        label: 'Exclude',
        nodeType: 'prefilter.exclude',
        outputType: 'bitmap',
        inputTypes: ['bitmap'],
        maxInputs: 1,
        config: { op: 'exclude' },
      },
    }
    useRuleStore.getState().addNode(source, { standalone: true })
    useRuleStore.getState().addNode(exclude, { standalone: true })
    expect(useRuleStore.getState().document?.prefilter.bitmap.expr).toMatchObject({
      op: 'lookup_string',
    })
    useRuleStore.getState().connectGraph(source.id, exclude.id, 0, {
      id: 'standalone-source-exclude',
      source: source.id,
      target: exclude.id,
      sourceHandle: 'output',
      targetHandle: 'input-0',
      data: { valueType: 'bitmap' },
    } as RuleGraphEdge)
    expect(
      useRuleStore
        .getState()
        .document?.graph.edges.some(
          (edge) => edge.source === source.id && edge.target === exclude.id,
        ),
    ).toBe(true)
    useRuleStore.getState().connectGraph(exclude.id, 'root-prefilter', 0, {
      id: 'standalone-exclude-output',
      source: exclude.id,
      target: 'root-prefilter',
      sourceHandle: 'output',
      targetHandle: 'input-0',
      data: { valueType: 'bitmap' },
    } as RuleGraphEdge)
    expect(rule.prefilter.bitmap.expr).toMatchObject({ op: 'lookup_string' })
    expect(useRuleStore.getState().document?.prefilter.bitmap.expr).toMatchObject({
      op: 'exclude',
      value: { op: 'none' },
    })
    expect(
      useRuleStore.getState().document?.graph.nodes.some((node) => node.id === exclude.id),
    ).toBe(false)
  })

  it('preserves node positions and comments across document refreshes', () => {
    const rule = structuredClone(demoRule)
    const graph = buildRuleGraph(rule)
    const target = graph.nodes.find((node) => node.data.astPath === '/evaluation/canJoin/expr')!
    useRuleStore.getState().setDocument({ ...rule, graph })
    useRuleStore.getState().setGraph(
      graph.nodes.map((node) =>
        node.id === target.id
          ? { ...node, position: { x: 777, y: 222 }, data: { ...node.data, comment: '保留布局' } }
          : node,
      ),
      graph.edges,
    )
    useRuleStore.getState().setDocument({ ...rule, graph: buildRuleGraph(rule) })
    const refreshed = useRuleStore
      .getState()
      .document?.graph.nodes.find((node) => node.id === target.id)
    expect(refreshed?.position).toEqual({ x: 777, y: 222 })
    expect(refreshed?.data.comment).toBe('保留布局')
  })

  it('truncates sparse variadic arrays and appends only at the next port', () => {
    const rule = structuredClone(demoRule)
    rule.evaluation.canJoin.expr = {
      op: 'bool_and',
      children: [{ op: 'bool_literal', value: true }, null, { op: 'bool_literal', value: false }],
    } as unknown as JsonObject
    expect(
      astInputSlots('bool_and', rule.evaluation.canJoin.expr as Record<string, JsonValue>),
    ).toHaveLength(1)
    const graph = buildRuleGraph(rule)
    const andNode = graph.nodes.find((node) => node.data.astPath === '/evaluation/canJoin/expr')!
    expect(andNode.data.inputTypes).toEqual(['bool'])
    expect(graph.nodes.filter((node) => node.data.astPath?.includes('/children/')).length).toBe(1)

    useRuleStore.getState().setDocument({ ...rule, graph })
    const source: RuleGraphNode = {
      id: 'append-bool',
      type: 'rule',
      position: { x: 0, y: 0 },
      data: {
        label: 'Bool',
        nodeType: 'literal.bool',
        outputType: 'bool',
        inputTypes: [],
        config: { value: false },
      },
    }
    useRuleStore.getState().addNode(source, { standalone: true })
    useRuleStore.getState().connectGraph(source.id, andNode.id, 1, {
      id: 'append-bool-edge',
      source: source.id,
      target: andNode.id,
      sourceHandle: 'output',
      targetHandle: 'input-1',
      data: { valueType: 'bool' },
    } as RuleGraphEdge)
    const next = useRuleStore.getState().document!
    const children = (next.evaluation.canJoin.expr as { children: unknown[] }).children
    expect(children).toHaveLength(2)
    expect(children.every((child) => child !== null && child !== undefined)).toBe(true)
  })

  it('keeps scalar config fields out of graph inputs and preserves their arity', () => {
    expect(
      astInputSlots('strings_contains', {
        op: 'strings_contains',
        values: { op: 'strings_literal', values: ['x'] },
        needle: 'x',
      }),
    ).toMatchObject([{ key: 'values', expectedType: 'strings' }])
    expect(
      astInputSlots('uint64s_contains', {
        op: 'uint64s_contains',
        values: { op: 'uint64s_literal', values: [1] },
        needle: 1,
      }),
    ).toMatchObject([{ key: 'values', expectedType: 'uint64s' }])
    expect(
      astInputSlots('strings_is_empty', {
        op: 'strings_is_empty',
        values: { op: 'strings_literal', values: [] },
      }),
    ).toHaveLength(1)
    expect(
      astInputSlots('uint64s_is_empty', {
        op: 'uint64s_is_empty',
        values: { op: 'uint64s_literal', values: [] },
      }),
    ).toHaveLength(1)
    expect(
      astInputSlots('int64_step', {
        op: 'int64_step',
        input: { op: 'int64_literal', value: 1 },
        steps: [{ at: 0, value: 1 }],
      }),
    ).toMatchObject([{ key: 'input', expectedType: 'int64' }])

    const rule = structuredClone(demoRule)
    rule.evaluation.canJoin.expr = {
      op: 'strings_eq',
      values: { op: 'strings_literal', values: ['x'] },
      other: { op: 'strings_literal', values: ['y'] },
    }
    const graph = buildRuleGraph(rule)
    const compare = graph.nodes.find((node) => node.data.astPath === '/evaluation/canJoin/expr')!
    useRuleStore.getState().setDocument({ ...rule, graph })
    useRuleStore.getState().updateNodeData(compare.id, {
      config: {
        op: 'strings_is_empty',
        values: { op: 'strings_literal', values: ['x'] },
      },
    })
    expect(useRuleStore.getState().document?.evaluation.canJoin.expr).not.toHaveProperty('other')
    expect(
      useRuleStore
        .getState()
        .document?.graph.nodes.find((node) => node.data.astPath === compare.data.astPath)?.data
        .inputTypes,
    ).toEqual(['strings'])
  })

  it('grows a standalone variadic node one contiguous handle at a time', () => {
    const rule = structuredClone(demoRule)
    const graph = buildRuleGraph(rule)
    useRuleStore.getState().setDocument({ ...rule, graph })
    const makeBool = (id: string): RuleGraphNode => ({
      id,
      type: 'rule',
      position: { x: 0, y: 0 },
      data: {
        label: id,
        nodeType: 'literal.bool',
        outputType: 'bool',
        inputTypes: [],
        config: { value: true },
      },
    })
    const andNode: RuleGraphNode = {
      id: 'standalone-and',
      type: 'rule',
      position: { x: 100, y: 100 },
      data: {
        label: 'AND',
        nodeType: 'logic.and',
        outputType: 'bool',
        inputTypes: [],
        maxInputs: 16,
        variadic: true,
        variadicInputType: 'bool',
        config: { op: 'bool_and', children: [] },
      },
    }
    const first = makeBool('standalone-bool-1')
    const second = makeBool('standalone-bool-2')
    useRuleStore.getState().addNode(andNode, { standalone: true })
    useRuleStore.getState().addNode(first, { standalone: true })
    useRuleStore.getState().addNode(second, { standalone: true })
    const connect = (source: RuleGraphNode, index: number, id: string) =>
      useRuleStore.getState().connectGraph(source.id, andNode.id, index, {
        id,
        source: source.id,
        target: andNode.id,
        sourceHandle: 'output',
        targetHandle: `input-${index}`,
        data: { valueType: 'bool' },
      } as RuleGraphEdge)
    connect(first, 0, 'standalone-and-0')
    expect(
      useRuleStore.getState().document?.graph.nodes.find((node) => node.id === andNode.id)?.data
        .inputTypes,
    ).toEqual(['bool'])
    connect(second, 1, 'standalone-and-1')
    expect(
      useRuleStore.getState().document?.graph.nodes.find((node) => node.id === andNode.id)?.data
        .inputTypes,
    ).toEqual(['bool', 'bool'])
    connect(second, 3, 'standalone-and-3')
    expect(useRuleStore.getState().notice).toContain('不能跳过')
    expect(
      useRuleStore.getState().document?.graph.edges.some((edge) => edge.id === 'standalone-and-3'),
    ).toBe(false)
    useRuleStore.getState().removeGraphEdge('standalone-and-0')
    const remaining = useRuleStore
      .getState()
      .document?.graph.edges.find((edge) => edge.target === andNode.id)
    expect(remaining?.targetHandle).toBe('input-0')
    expect(
      useRuleStore.getState().document?.graph.nodes.find((node) => node.id === andNode.id)?.data
        .inputTypes,
    ).toEqual(['bool'])
  })

  it('keeps cache entries distinct for slash-containing rule identities', () => {
    const first = structuredClone(demoRule)
    first.ruleKey = 'alpha/beta'
    first.placementId = 'gamma'
    const second = structuredClone(demoRule)
    second.ruleKey = 'alpha'
    second.placementId = 'beta/gamma'
    const firstGraph = buildRuleGraph(first)
    const secondGraph = buildRuleGraph(second)
    const firstNode = firstGraph.nodes.find((node) => node.data.astPath)!
    const secondNode = secondGraph.nodes.find((node) => node.data.astPath)!

    useRuleStore.getState().setDocument({ ...first, graph: firstGraph })
    useRuleStore.getState().setGraph(
      firstGraph.nodes.map((node) =>
        node.id === firstNode.id ? { ...node, position: { x: 111, y: 222 } } : node,
      ),
      firstGraph.edges,
    )
    useRuleStore.getState().setDocument({ ...second, graph: secondGraph })
    useRuleStore.getState().setGraph(
      secondGraph.nodes.map((node) =>
        node.id === secondNode.id ? { ...node, position: { x: 333, y: 444 } } : node,
      ),
      secondGraph.edges,
    )
    useRuleStore.getState().setDocument({ ...first, graph: firstGraph })
    const restored = useRuleStore
      .getState()
      .document?.graph.nodes.find((node) => node.id === firstNode.id)
    expect(restored?.position).toEqual({ x: 111, y: 222 })
  })
})
