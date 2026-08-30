import { describe, expect, it } from 'vitest'
import { demoRule } from './mockData'
import { importRuleDocument, portableRuleDocument, portableRuleFileName } from './ruleDocumentIO'

describe('rule JSON import and export', () => {
  it('exports only the portable rule envelopes and rebuilds graph on import', () => {
    const current = structuredClone(demoRule)
    const portable = portableRuleDocument(current)
    expect(portable.schemaVersion).toBe('match-rule/v1')
    expect(portable).not.toHaveProperty('graph')
    expect(portable).not.toHaveProperty('placementId')
    portable.evaluation.canJoin.expr = { op: 'bool_literal', value: false }

    const imported = importRuleDocument(portable, current)
    expect(imported.evaluation.canJoin.expr).toMatchObject({ value: false })
    expect(
      imported.graph.nodes.some((node) => node.data.astPath === '/evaluation/canJoin/expr'),
    ).toBe(true)
  })

  it('rejects another logical node identity', () => {
    const portable = portableRuleDocument(demoRule)
    portable.ruleKey = { namespace: 'other', ruleId: 2 }
    expect(() => importRuleDocument(portable, demoRule)).toThrow(/当前规则/)
  })

  it('creates a filesystem-safe export name', () => {
    const document = { ...demoRule, apiRule: { namespace: 'demo', ruleId: 1 } }
    expect(portableRuleFileName(document)).toBe('matchscope-demo-1.json')
  })
})
