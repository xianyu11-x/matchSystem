import { describe, expect, it } from 'vitest'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { ScenarioSettingsEditor } from '../components/ScenarioSettingsEditor'
import {
  appendRuleDraft,
  documentFromDraft,
  draftObject,
  draftRules,
  mergeRuleDraft,
  replaceRuleDraft,
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
        id: '1',
        endpoint: 'inproc://1',
        enabled: true,
        selector: 'round_robin',
      },
    ])
    const first = draftRules(next)[0]
    expect(draftObject(first.logicalNode)).toEqual({ rule: { ruleId: '1' }, placementId: '1' })
    expect(first.physicalNodeId).toBe('1')
  })

  it('保留相邻的大身份并为新部署选取未使用的十进制字符串', () => {
    let draft = appendRuleDraft(appendRuleDraft({ physicalNodes: [], rules: [] }))
    const rows = draftRules(draft)
    rows[0].logicalNode = {
      rule: { namespace: '18446744073709551615', ruleId: '9007199254740992' },
      placementId: '9007199254740992',
    }
    rows[1].logicalNode = {
      rule: { namespace: '18446744073709551615', ruleId: '9007199254740993' },
      placementId: '9007199254740993',
    }
    draft.rules = rows
    draft = mergeRuleDraft(draft, -1)
    expect(draftObject(draftRules(draft)[0].rule).ruleKey).toEqual(
      draftObject(draftRules(draft)[0].logicalNode).rule,
    )
    expect(draftObject(draftRules(draft)[1].rule).ruleKey).toEqual(
      draftObject(draftRules(draft)[1].logicalNode).rule,
    )
    const copied = appendRuleDraft(draft, 0)
    const copiedKey = draftObject(draftObject(draftRules(copied)[2].logicalNode).rule)
    expect(copiedKey).toEqual({ namespace: '18446744073709551615', ruleId: '1' })
    expect(draftObject(draftRules(copied)[2].logicalNode).placementId).toBe('1')
    expect(draftObject(draftObject(draftRules(copied)[0].logicalNode).rule).ruleId).toBe(
      '9007199254740992',
    )
    expect(draftObject(draftObject(draftRules(copied)[1].logicalNode).rule).ruleId).toBe(
      '9007199254740993',
    )
  })

  it('无效的前导零身份不占用新编号', () => {
    let draft = appendRuleDraft({ physicalNodes: [], rules: [] })
    const row = draftRules(draft)[0]
    row.logicalNode = { rule: { ruleId: '01' }, placementId: '01' }
    draft.rules = [row]
    draft = appendRuleDraft(draft)
    const next = draftObject(draftRules(draft)[1].logicalNode)
    expect(draftObject(next.rule).ruleId).toBe('1')
    expect(next.placementId).toBe('1')
  })

  it('设置页以文本显示相邻大编号，并识别安全数值与字符串的物理节点引用', () => {
    let draft = appendRuleDraft(appendRuleDraft({ physicalNodes: [], rules: [] }))
    const rows = draftRules(draft)
    rows[0].logicalNode = { rule: { ruleId: '9007199254740992' }, placementId: '1' }
    rows[1].logicalNode = { rule: { ruleId: '9007199254740993' }, placementId: '1' }
    rows[0].physicalNodeId = 1
    draft = { ...draft, rules: rows }
    const html = renderToStaticMarkup(
      createElement(ScenarioSettingsEditor, { draft, onChange: () => undefined }),
    )
    expect(html).toMatch(/规则 ID<input class="text-input"[^>]*value="9007199254740992"/)
    expect(html).toMatch(/规则 ID<input class="text-input"[^>]*value="9007199254740993"/)
    expect(html).not.toContain('规则与部署标识组合重复')
    expect(html).toMatch(/<button[^>]*disabled=""[^>]*>删除未被规则引用的节点/)
  })

  it('部署重复只取决于 RuleID 和 PlacementID，不受别名影响', () => {
    const draft = appendRuleDraft(appendRuleDraft({ physicalNodes: [], rules: [] }))
    const rows = draftRules(draft)
    rows[0].logicalNode = { rule: { namespace: '旧/别名', ruleId: '7' }, placementId: '1' }
    rows[1].logicalNode = { rule: { namespace: '新:别名', ruleId: '7' }, placementId: '1' }
    draft.rules = rows
    const render = () =>
      renderToStaticMarkup(
        createElement(ScenarioSettingsEditor, { draft, onChange: () => undefined }),
      )
    expect(render()).toContain('规则与部署标识组合重复')
    rows[1].logicalNode = { rule: { namespace: '旧/别名', ruleId: '8' }, placementId: '1' }
    draft.rules = rows
    expect(render()).not.toContain('规则与部署标识组合重复')
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
      rule: { namespace: '4', ruleId: 90 },
      placementId: '3',
    }
    rows[1].weight = 12
    draft.matchHistoryLimit = 123
    const saved = mergeRuleDraft(draft, 0, first)
    const documents = draftRules(saved).map(documentFromDraft)
    expect(documents[0]).toMatchObject({
      ruleKey: '90',
      placementId: '3',
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

  it('keeps the Python provider binding across rule edits without descriptor fields', () => {
    let draft = appendRuleDraft({ physicalNodes: [], rules: [] })
    const row = draftRules(draft)[0]
    row.pythonProvider = { script: 'provider.py', timeoutMs: 125 }
    draft = mergeRuleDraft(draft, 0, documentFromDraft(row))
    const saved = draftRules(draft)[0]
    expect(documentFromDraft(saved).pythonProvider).toEqual({
      script: 'provider.py',
      timeoutMs: 125,
    })
    expect(saved.pythonProvider).toEqual({ script: 'provider.py', timeoutMs: 125 })
    expect(saved.factProviderDescriptor).toBeUndefined()
    expect(saved.objectFactProviderDescriptor).toBeUndefined()
    expect(saved.matchFactProviderDescriptor).toBeUndefined()
  })

  it('replaces a complete scenario row so cleared provider fields cannot be merged back', () => {
    let draft = appendRuleDraft({ physicalNodes: [], rules: [] })
    const row = draftRules(draft)[0]
    row.pythonProvider = { script: 'provider.py', timeoutMs: 125 }
    draft = replaceRuleDraft(draft, 0, row)

    const cleared = { ...row }
    delete cleared.pythonProvider
    const replaced = replaceRuleDraft(draft, 0, cleared)
    const saved = draftRules(replaced)[0]

    expect('pythonProvider' in saved).toBe(false)
    expect('factProviderDescriptor' in saved).toBe(false)
    expect(documentFromDraft(saved).pythonProvider).toBeUndefined()
  })

  it('keeps Python provider bindings without descriptor source fields', () => {
    let draft = appendRuleDraft({ physicalNodes: [], rules: [] })
    const row = draftRules(draft)[0]
    row.pythonProvider = { script: 'describe.py' }
    draft = replaceRuleDraft(draft, 0, row)

    const document = documentFromDraft(draftRules(draft)[0])
    expect(document.pythonProvider).toEqual({
      script: 'describe.py',
    })

    const saved = mergeRuleDraft(draft, 0, document)
    expect(draftRules(saved)[0].pythonProvider).toEqual({
      script: 'describe.py',
    })
    expect(draftRules(saved)[0].factProviderDescriptor).toBeUndefined()
  })
})
