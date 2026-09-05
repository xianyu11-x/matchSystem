import { describe, expect, it } from 'vitest'
import {
  appendRuleDraft,
  documentFromDraft,
  draftObject,
  draftRules,
  mergeRuleDraft,
  removeRuleDraft,
} from './scenarioDraft'
import { validateRuleDocument } from './validation'
import { useRuleStore } from './ruleStore'
import type { JsonObject } from '../types'

describe('scenario rule lifecycle drafts', () => {
  it('creates a complete nonmatching rule from an empty scene without JSON', () => {
    const next = appendRuleDraft({
      schemaVersion: 'simulator-scenario/v1',
      physicalNodes: [],
      rules: [],
    })
    const document = documentFromDraft(draftRules(next)[0])
    expect(validateRuleDocument(document).valid).toBe(true)
    expect(document.evaluation.canComplete.expr).toEqual({ op: 'bool_literal', value: false })
    expect(document.evaluation.canJoin.expr).toEqual({ op: 'bool_literal', value: false })
    expect(next.physicalNodes).toEqual([
      {
        id: 'simulator-1',
        endpoint: 'inproc://simulator-1',
        enabled: true,
        selector: 'round_robin',
      },
    ])
  })

  it('retains edits across two rule selections and saves both with deployment changes', () => {
    let draft = appendRuleDraft(appendRuleDraft({ physicalNodes: [], rules: [] }))
    let first = documentFromDraft(draftRules(draft)[0])
    first.scoring = { type: 'constant', params: { value: 17 } }
    draft = mergeRuleDraft(draft, 0, first)
    const second = documentFromDraft(draftRules(draft)[1])
    second.seedSelection = { type: 'random', params: { randomSeed: 37 } }
    second.runtime.maxPlayers = 9
    draft = mergeRuleDraft(draft, 1, second)
    first = documentFromDraft(draftRules(draft)[0])
    expect(first.scoring).toEqual({ type: 'constant', params: { value: 17 } })
    first.runtime.maxPlayers = 5
    const rows = draftRules(draft)
    rows[0].logicalNode = {
      rule: { namespace: 'renamed', ruleId: 90 },
      placementId: 'new-placement',
    }
    rows[1].weight = 12
    draft.matchHistoryLimit = 123
    const saved = mergeRuleDraft(draft, 0, first)
    const documents = draftRules(saved).map(documentFromDraft)
    expect(documents[0]).toMatchObject({
      ruleKey: 'renamed/90',
      placementId: 'new-placement',
      runtime: { maxPlayers: 5 },
      scoring: { params: { value: 17 } },
    })
    expect(documents[1]).toMatchObject({
      seedSelection: { params: { randomSeed: 37 } },
      runtime: { maxPlayers: 9 },
    })
    expect(draftRules(saved)[1].weight).toBe(12)
    expect(saved.matchHistoryLimit).toBe(123)
    expect(documents.every((document) => validateRuleDocument(document).valid)).toBe(true)
  })

  it('copies the latest unsaved expressions and independent provider/value layers to a new identity', () => {
    let draft = appendRuleDraft({ physicalNodes: [], rules: [] })
    const original = documentFromDraft(draftRules(draft)[0])
    original.contract.facts = [{ name: 'threshold', type: 'int64', scope: 'tick' }]
    original.providerDescriptors = {
      tick: { id: 'threshold-provider', version: 'v2', facts: original.contract.facts },
    }
    original.tickFacts = { threshold: 42 }
    original.evaluation.canComplete.expr = {
      op: 'int64_gt',
      left: { op: 'int64_ref', source: 'tick_facts', name: 'threshold' },
      right: { op: 'int64_literal', value: 10 },
    }
    draft = appendRuleDraft(mergeRuleDraft(draft, 0, original), 0)
    const copy = documentFromDraft(draftRules(draft)[1])
    expect(copy.ruleKey).not.toBe(original.ruleKey)
    expect(copy.placementId).not.toBe(original.placementId)
    expect(copy.evaluation).toEqual(original.evaluation)
    expect(copy.tickFacts).toEqual(original.tickFacts)
    expect(copy.providerDescriptors).toEqual(original.providerDescriptors)
    copy.tickFacts!.threshold = 99
    const saved = mergeRuleDraft(draft, 1, copy)
    expect(documentFromDraft(draftRules(saved)[0]).tickFacts!.threshold).toBe(42)
    expect(documentFromDraft(draftRules(saved)[1]).tickFacts!.threshold).toBe(99)
  })

  it('deletes before or at the selected row without overwriting remaining drafts', () => {
    let draft = appendRuleDraft(appendRuleDraft(appendRuleDraft({ physicalNodes: [], rules: [] })))
    const third = documentFromDraft(draftRules(draft)[2])
    third.runtime.maxPlayers = 19
    draft = mergeRuleDraft(draft, 2, third)
    let removed = removeRuleDraft(draft, 0, 2)
    expect(removed.selected).toBe(1)
    expect(documentFromDraft(draftRules(removed.draft)[1]).runtime.maxPlayers).toBe(19)
    removed = removeRuleDraft(removed.draft, 1, 1)
    expect(removed.selected).toBe(0)
    expect(draftRules(removed.draft)).toHaveLength(1)
    removed = removeRuleDraft(removed.draft, 0, 0)
    expect(removed.selected).toBe(-1)
    expect(draftRules(removed.draft)).toEqual([])
    const recreated = appendRuleDraft(removed.draft)
    expect(validateRuleDocument(documentFromDraft(draftRules(recreated)[0])).valid).toBe(true)
  })

  it('serializes drafts without editor metadata or accidental rule resurrection', () => {
    let draft = appendRuleDraft({
      schemaVersion: 'simulator-scenario/v1',
      physicalNodes: [],
      rules: [],
    })
    const document = documentFromDraft(draftRules(draft)[0])
    draft = removeRuleDraft(draft, 0, 0).draft
    expect(draftRules(mergeRuleDraft(draft, -1, document))).toEqual([])
    const saved = appendRuleDraft(draft)
    const raw = draftRules(saved)[0]
    expect(draftObject(raw.rule).graph).toBeUndefined()
    expect(draftObject(raw.rule).ruleKey).toEqual(draftObject(raw.logicalNode).rule)
    expect(Object.keys(raw).sort()).toEqual([
      'enabled',
      'logicalNode',
      'physicalNodeId',
      'rule',
      'weight',
    ])
    useRuleStore.getState().clearGraphSession()
    expect(useRuleStore.getState().activeTab).toBe('settings')
  })
})
