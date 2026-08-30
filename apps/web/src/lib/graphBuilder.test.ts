import { describe, expect, it } from 'vitest'
import { demoRule } from './mockData'
import { buildRuleGraph } from './graphBuilder'
import { useRuleStore } from './ruleStore'
import { scenarioPayload } from './api'
import type { JsonValue, RuleDocument, RuleGraphNode, Scenario } from '../types'

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
})
