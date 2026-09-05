import { createElement, isValidElement, type ReactNode } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { ExpressionEditor } from '../components/ExpressionEditor'
import { ContractEditor } from '../components/ContractEditor'
import { RuleSettingsEditor } from '../components/RuleSettingsEditor'
import { ProviderDescriptorsEditor, TickFactsEditor } from '../components/RuleFactsEditor'
import { scenarioSettingsPayload } from '../components/ScenarioSettingsEditor'
import { createExpression, expressionDefinitions, expressionFieldType } from './expressionForm'
import { demoRule } from './mockData'
import { scenarioPayload } from './api'
import { useRuleStore } from './ruleStore'
import { validateRuleDocument } from './validation'
import type { JsonObject, RuleDocument, Scenario } from '../types'

describe('complete rule forms', () => {
  it('renames indexed attributes without resetting custom index limits', () => {
    let updated = demoRule.contract
    const tree = ContractEditor({
      contract: structuredClone(demoRule.contract),
      onChange: (next) => {
        updated = next
      },
    })
    const findName = (node: ReactNode): boolean => {
      if (Array.isArray(node)) return node.some(findName)
      if (!isValidElement(node)) return false
      const props = node.props as {
        children?: ReactNode
        'aria-label'?: string
        onChange?: (event: { target: { value: string } }) => void
      }
      if (props['aria-label'] === 'Attribute 1 名称') {
        props.onChange!({ target: { value: 'new-region' } })
        return true
      }
      return findName(props.children)
    }
    expect(findName(tree)).toBe(true)
    expect(updated.indexes[0]).toEqual({ ...demoRule.contract.indexes[0], name: 'new-region' })
  })
  it('renders every schema operation with typed controls and no JSON editor', () => {
    expect(expressionDefinitions).toHaveLength(46)
    for (const definition of expressionDefinitions) {
      const html = renderToStaticMarkup(
        createElement(ExpressionEditor, {
          value: createExpression(definition.op),
          type: definition.resultType,
          contract: demoRule.contract,
          onChange: () => {},
        }),
      )
      expect(html, definition.op).toContain(definition.op)
      expect(html, definition.op).not.toContain('textarea')
      expect(html, definition.op).not.toContain('JSON 编辑')
    }
    const empty = expressionDefinitions.find((item) => item.op === 'uint64s_is_empty')!
    expect(expressionFieldType(empty.fields.values, empty.op)?.type).toBe('uint64s')
    expect(createExpression('lookup_range')).toMatchObject({
      min: { schemaVersion: 'expression-scalar/v3', resultType: 'int64' },
      max: { resultType: 'int64' },
    })
  })

  it('preserves commas, empty strings, nested branches and independent runtime fields when editing settings', () => {
    const rule = structuredClone(demoRule)
    rule.prefilter.bitmap.expr = {
      op: 'if',
      when: {
        schemaVersion: 'expression-scalar/v3',
        resultType: 'bool',
        expr: { op: 'bool_literal', value: true },
      },
      then: rule.prefilter.bitmap.expr,
      else: { op: 'none' },
    }
    rule.tickFacts = { tags: ['', 'a,b', ' spaced '] }
    useRuleStore.getState().setDocument(rule)
    const scoring: RuleDocument['scoring'] = {
      type: 'int64_field',
      params: { field: 'playerLevel', direction: 'ascending', weight: 2.5, missingScore: -100 },
    }
    const seed: RuleDocument['seedSelection'] = {
      type: 'int64_priority',
      params: { field: 'playerLevel', direction: 'descending' },
    }
    useRuleStore.getState().setEnvelope('scoring', scoring)
    useRuleStore.getState().setEnvelope('seedSelection', seed)
    useRuleStore
      .getState()
      .setEnvelope('runtime', {
        ...rule.runtime,
        candidateScoringLimitPerSeed: 7,
        candidateLimitPerSeed: 20,
      })
    const next = useRuleStore.getState().document!
    expect(next.prefilter).toEqual(rule.prefilter)
    expect(next.tickFacts).toEqual(rule.tickFacts)
    expect(next.scoring).toEqual(scoring)
    expect(next.seedSelection).toEqual(seed)
    // A retained limit greater than the scoring pool is legal in the core.
    expect(validateRuleDocument(next).valid).toBe(true)
    const html = renderToStaticMarkup(
      createElement(RuleSettingsEditor, { document: next, onChange: () => {} }),
    )
    for (const label of [
      '种子选择算法',
      '候选评分算法',
      '评分权重',
      '缺失属性',
      '单次成局',
      '整轮',
    ])
      expect(html).toContain(label)
  })

  it('keeps empty Facts editable and preserves provider extras', () => {
    const descriptors = {
      tick: {
        id: 'custom',
        version: 'v2',
        facts: [
          {
            name: 'extra',
            scope: 'tick' as const,
            type: 'strings' as const,
            maxValues: 10,
            description: '额外能力',
          },
        ],
      },
    }
    const html = renderToStaticMarkup(
      createElement(ProviderDescriptorsEditor, { value: descriptors, onChange: () => {} }),
    )
    expect(html).toContain('extra')
    expect(html).toContain('额外能力')
    const tick = renderToStaticMarkup(
      createElement(TickFactsEditor, {
        value: {},
        fields: [{ name: 'x', type: 'int64', scope: 'tick' }],
        onChange: () => {},
      }),
    )
    expect(tick).toContain('提供本轮值')
  })

  it('merges rule drafts with changed deployment identities and preserves every unrelated field', () => {
    const rule = {
      ...structuredClone(demoRule),
      ruleKey: 'demo/1',
      apiRule: { namespace: 'demo', ruleId: 1 },
    }
    const raw = scenarioPayload({ rawScenario: {} } as Scenario, rule)
    const first = (raw.rules as JsonObject[])[0]
    first.physicalNodeId = 'node1'
    first.weight = 7
    first.enabled = true
    raw.physicalNodes = [
      { id: 'node1', endpoint: 'inproc://1', enabled: true, selector: 'oldest_waiting' },
    ]
    const scenario = { rawScenario: raw } as Scenario
    const draft = structuredClone(raw)
    const target = (draft.rules as JsonObject[])[0]
    target.logicalNode = {
      rule: { namespace: 'renamed', ruleId: 99 },
      placementId: 'new-placement',
    }
    target.weight = 9
    draft.matchHistoryLimit = 123
    rule.scoring = { type: 'constant', params: { value: 3.5 } }
    rule.seedSelection = { type: 'random', params: { randomSeed: 57 } }
    const result = scenarioSettingsPayload(scenario, draft, rule)
    const saved = (result.rules as JsonObject[])[0]
    expect(saved).toMatchObject({
      weight: 9,
      physicalNodeId: 'node1',
      logicalNode: { rule: { namespace: 'renamed', ruleId: 99 } },
      rule: {
        ruleKey: { namespace: 'renamed', ruleId: 99 },
        scoring: rule.scoring,
        seedSelection: rule.seedSelection,
      },
    })
    expect(result.matchHistoryLimit).toBe(123)
    expect(result.physicalNodes).toEqual(raw.physicalNodes)
    expect(saved.factProviderDescriptor).toEqual(first.factProviderDescriptor)
  })

  it('rejects invalid numeric drafts instead of saving NaN as null', () => {
    const rule = structuredClone(demoRule)
    rule.scoring = { type: 'constant', params: { value: NaN } }
    expect(validateRuleDocument(rule).valid).toBe(false)
    rule.scoring.params.value = 1
    rule.tickFacts = { invalid: NaN }
    expect(
      validateRuleDocument(rule).errors.some((issue) => issue.path === '/tickFacts/invalid'),
    ).toBe(true)
  })
})
