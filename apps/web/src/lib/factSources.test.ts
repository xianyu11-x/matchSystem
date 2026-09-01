import { describe, expect, it } from 'vitest'
import { resolveRuleFactSources } from './factSources'

describe('Contract Fact source synchronization', () => {
  it('prefers the current edited Contract over stale metadata', () => {
    const metadata = {
      contractFacts: [
        { name: 'objectFact', type: 'strings' as const, scope: 'object' as const },
      ],
      facts: [
        { name: 'legacyFact', type: 'int64' as const, scope: 'match' as const },
      ],
    }
    const edited = [{ name: 'matchFact', type: 'int64' as const, scope: 'match' as const }]
    expect(resolveRuleFactSources({ contractFacts: edited }, metadata).contractFacts).toEqual(edited)
  })

  it('keeps an explicit empty Contract Fact list instead of falling back', () => {
    expect(
      resolveRuleFactSources({ contractFacts: [] }, {
        contractFacts: [{ name: 'old', type: 'int64', scope: 'tick' }],
      }).contractFacts,
    ).toEqual([])
  })

  it('keeps empty Provider and Runtime documents instead of stale metadata', () => {
    const metadata = {
      contractFacts: [{ name: 'oldContract', type: 'int64' as const, scope: 'tick' as const }],
      providerDescriptors: {
        tick: {
          id: 'old-provider',
          version: 'v1',
          facts: [{ name: 'oldProvider', type: 'int64' as const, scope: 'tick' as const }],
        },
      },
      runtimeFacts: { tick: { oldRuntime: 1 } },
    }
    const resolved = resolveRuleFactSources(
      { contractFacts: [], providerDescriptors: {}, runtimeFacts: { tick: {} } },
      metadata,
    )
    expect(resolved.contractFacts).toEqual([])
    expect(resolved.providerDescriptors).toEqual({})
    expect(resolved.runtimeFacts).toEqual({ tick: {} })
  })

  it('uses current metadata only while a selected rule document is unavailable', () => {
    const currentMetadata = {
      contractFacts: [{ name: 'currentContract', type: 'strings' as const, scope: 'object' as const }],
      providerDescriptors: {
        object: {
          id: 'current-provider',
          version: 'v2',
          facts: [{ name: 'currentProvider', type: 'strings' as const, scope: 'object' as const }],
        },
      },
      runtimeFacts: { tick: { currentRuntime: 7 } },
    }
    const resolved = resolveRuleFactSources(undefined, currentMetadata)
    expect(resolved.contractFacts[0].name).toBe('currentContract')
    expect(resolved.providerDescriptors.object?.id).toBe('current-provider')
    expect(resolved.runtimeFacts.tick).toEqual({ currentRuntime: 7 })
  })

  it('makes an imported document authoritative over pre-import metadata', () => {
    const imported = {
      contractFacts: [{ name: 'importedContract', type: 'uint64s' as const, scope: 'match' as const }],
      providerDescriptors: {
        match: {
          id: 'imported-provider',
          version: 'v3',
          facts: [{ name: 'importedProvider', type: 'uint64s' as const, scope: 'match' as const }],
        },
      },
      runtimeFacts: { tick: { importedRuntime: 9 } },
    }
    const resolved = resolveRuleFactSources(imported, {
      contractFacts: [{ name: 'beforeImport', type: 'int64', scope: 'tick' }],
      providerDescriptors: { tick: { id: 'before-import', version: 'v1', facts: [] } },
      runtimeFacts: { tick: { beforeImport: 1 } },
    })
    expect(resolved.contractFacts[0].name).toBe('importedContract')
    expect(resolved.providerDescriptors.match?.id).toBe('imported-provider')
    expect(resolved.runtimeFacts.tick).toEqual({ importedRuntime: 9 })
  })
})
