import { describe, expect, it } from 'vitest'
import { demoRule, demoRuleSummary } from './mockData'
import { validateRuleDocument } from './validation'

describe('rule document schema validation', () => {
  it('keeps demo runtime Tick values separate from its handshake Descriptor', () => {
    expect(demoRuleSummary.tickFacts).toEqual(demoRule.tickFacts)
    expect(demoRuleSummary.tickFacts).toEqual({ waitingCount: 742 })
    expect(demoRuleSummary.providerDescriptors?.tick).toMatchObject({
      id: 'demo.tick-facts',
      version: 'v1',
    })
    expect(demoRuleSummary.providerDescriptors?.tick?.facts).toEqual([
      { name: 'waitingCount', type: 'int64', scope: 'tick' },
    ])
    expect(demoRuleSummary.providerDescriptors?.object?.facts).toContainEqual({
      name: 'waitingTime',
      type: 'int64',
      scope: 'object',
    })
    expect(demoRuleSummary.providerDescriptors?.tick?.facts).not.toContainEqual(
      expect.objectContaining({ name: 'queueDepth' }),
    )
    for (const name of ['waitTime', 'waiting-time', 'wait-time']) {
      expect(demoRuleSummary.providerDescriptors?.object?.facts).not.toContainEqual(
        expect.objectContaining({ name }),
      )
    }
    expect(demoRuleSummary.providerDescriptors?.match?.facts).toContainEqual({
      name: 'memberCount',
      type: 'int64',
      scope: 'match',
    })
    expect(demoRuleSummary.providerDescriptors?.tick?.facts).not.toBe(
      demoRuleSummary.tickFacts,
    )
  })

  it('accepts the demo v3 envelopes and graph', () => {
    const result = validateRuleDocument(demoRule)
    expect(result.valid).toBe(true)
    expect(result.errors).toHaveLength(0)
  })

  it('accepts transport identity and tick facts without schema false positives', () => {
    const document = structuredClone(demoRule)
    document.apiRule = { namespace: 'demo', ruleId: 1 }
    document.tickFacts = { waitingCount: 3, labels: ['ready'], ids: [1, 2], empty: [] }
    const result = validateRuleDocument(document)
    expect(result.valid).toBe(true)
    expect(result.errors).toHaveLength(0)
  })

  it('reports an invalid contract and node reference', () => {
    const broken = structuredClone(demoRule)
    broken.contract.schemaVersion = 'bad' as typeof broken.contract.schemaVersion
    broken.graph.nodes[0].data.config.name = 'missing-fact'
    const result = validateRuleDocument(broken)
    expect(result.valid).toBe(false)
    expect(result.errors.some((error) => error.source === 'schema')).toBe(true)
    expect(result.errors.some((error) => error.message.includes('Contract'))).toBe(true)
  })

  it('rejects duplicate names and indexes that do not reference an attribute', () => {
    const broken = structuredClone(demoRule)
    broken.contract.facts.push({
      name: broken.contract.attributes[0].name,
      type: 'strings',
      scope: 'object',
      maxValues: 1,
    })
    broken.contract.indexes.push({ type: 'int64_range', name: 'missing' })
    const result = validateRuleDocument(broken)
    expect(result.valid).toBe(false)
    expect(result.errors.some((error) => error.message.includes('已被 Attribute 使用'))).toBe(true)
    expect(result.errors.some((error) => error.message.includes('已声明的 Attribute'))).toBe(true)
  })
})
