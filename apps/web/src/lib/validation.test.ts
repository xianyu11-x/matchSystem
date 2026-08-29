import { describe, expect, it } from 'vitest'
import { demoRule } from './mockData'
import { validateRuleDocument } from './validation'

describe('rule document schema validation', () => {
  it('accepts the demo v3 envelopes and graph', () => {
    const result = validateRuleDocument(demoRule)
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
})
