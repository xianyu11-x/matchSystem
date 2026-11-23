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
    portable.ruleKey = { namespace: '2', ruleId: 2 }
    expect(() => importRuleDocument(portable, demoRule)).toThrow(/当前规则/)
  })

  it('允许同一 RuleID 使用不同别名，且不从展示文本反解身份', () => {
    const current = { ...demoRule, ruleKey: '1', apiRule: { namespace: '旧/别名', ruleId: '1' } }
    const imported = portableRuleDocument({
      ...current,
      apiRule: { namespace: '新:别名', ruleId: '1' },
    })
    expect(importRuleDocument(imported, current).apiRule).toEqual({
      namespace: '新:别名',
      ruleId: '1',
    })
    expect(() =>
      portableRuleDocument({ ...current, apiRule: undefined, ruleKey: '别名/1' }),
    ).toThrow(/RuleKey/)
    expect(portableRuleDocument({ ...current, apiRule: undefined, ruleKey: '1' }).ruleKey).toEqual({
      ruleId: '1',
    })
  })

  it('creates a filesystem-safe export name', () => {
    const document = { ...demoRule, apiRule: { namespace: '1', ruleId: 1 } }
    expect(portableRuleFileName(document)).toBe('matchscope-1-1.json')
  })
})
