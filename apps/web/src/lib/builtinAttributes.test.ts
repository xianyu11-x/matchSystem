import { createElement, isValidElement, type ReactNode } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { ContractEditor } from '../components/ContractEditor'
import { ExpressionEditor } from '../components/ExpressionEditor'
import { RuleSettingsEditor } from '../components/RuleSettingsEditor'
import type { BuiltinAttribute, JsonObject } from '../types'
import { capabilitiesFromWire } from './api'
import { capabilitiesForAction, omitReadonlyBuiltins } from './builtinAttributes'
import {
  documentFromPortable,
  newPortableRule,
  parseRuleLibraryImport,
  validateLibraryDocuments,
} from './ruleLibrary'
import { portableRuleDocument } from './ruleDocumentIO'
import { documentFromDraft, mergeRuleDraft } from './scenarioDraft'
import { validateContractSemantics, validateRuleDocument } from './validation'

const builtin: BuiltinAttribute = {
  name: 'createTime',
  type: 'int64',
  readOnly: true,
  source: 'ticket.createdAt',
  indexTypes: ['int64_range'],
}
const capabilities = capabilitiesFromWire({
  schemaVersions: ['logical-node-contract/v3'],
  builtinAttributes: [builtin],
})

function builtinRule() {
  const rule = newPortableRule({ namespace: 'test', ruleId: '1' })
  rule.contract.indexes = [{ type: 'int64_range', name: 'createTime' }]
  rule.prefilter.bitmap.expr = {
    op: 'lookup_nearest',
    index: 'createTime',
    target: {
      schemaVersion: 'expression-scalar/v3',
      resultType: 'int64',
      expr: { op: 'int64_ref', source: 'seed_attributes', name: 'createTime' },
    },
    direction: 'right',
    limit: 20,
  }
  rule.evaluation.canJoin.expr = {
    op: 'int64_gte',
    left: { op: 'int64_ref', source: 'candidate_attributes', name: 'createTime' },
    right: { op: 'int64_ref', source: 'seed_attributes', name: 'createTime' },
  }
  rule.seedSelection = {
    type: 'int64_priority',
    params: { field: 'createTime', direction: 'ascending' },
  }
  rule.scoring = { type: 'int64_field', params: { field: 'createTime', direction: 'ascending' } }
  return rule
}

describe('内建只读属性目录', () => {
  it('uses capability metadata for graph validation, import and scenario round trip', () => {
    const rule = builtinRule()
    const document = documentFromPortable(rule)
    expect(rule.contract.attributes).toEqual([])
    expect(validateRuleDocument(document, capabilities)).toEqual({ valid: true, errors: [] })
    expect(validateLibraryDocuments([document], capabilities)).toBeUndefined()
    expect(validateRuleDocument(document).valid).toBe(false)
    expect(() => parseRuleLibraryImport(rule)).toThrow()
    const imported = parseRuleLibraryImport(rule, capabilities)
    expect(imported.rules[0]).toEqual(portableRuleDocument(document))
    expect(imported.rules[0].contract.attributes).toEqual([])

    const draft: JsonObject = {
      schemaVersion: 'simulator-scenario/v1',
      rules: [
        {
          logicalNode: { rule: rule.ruleKey as unknown as JsonObject, placementId: '1' },
          rule: rule as unknown as JsonObject,
        },
      ],
    }
    const scenarioImport = parseRuleLibraryImport(draft, capabilities)
    expect(scenarioImport.rules[0].contract.attributes).toEqual([])
    const draftDocument = documentFromDraft((draft.rules as JsonObject[])[0])
    expect(validateRuleDocument(draftDocument, capabilities).valid).toBe(true)
    const merged = mergeRuleDraft(draft, 0, draftDocument)
    expect(((merged.rules as JsonObject[])[0].rule as JsonObject).contract).toEqual(rule.contract)
  })

  it('keeps old services unchanged while reporting reserved name collisions on new ones', () => {
    const contract = builtinRule().contract
    contract.attributes.push({ name: 'createTime', type: 'int64' })
    expect(validateContractSemantics(contract)).toEqual([])
    expect(
      validateContractSemantics(contract, [builtin]).some(
        (error) => error.path === '/contract/attributes/0/name',
      ),
    ).toBe(true)
    contract.attributes = []
    contract.facts.push({ name: 'createTime', type: 'int64', scope: 'object' })
    expect(
      validateContractSemantics(contract, [builtin]).some(
        (error) => error.path === '/contract/facts/0/name',
      ),
    ).toBe(true)
  })

  it('makes the builtin selectable and indexes it without writing an attribute declaration', () => {
    const rule = builtinRule()
    rule.contract.indexes = []
    let changed = structuredClone(rule.contract)
    const tree = ContractEditor({
      contract: rule.contract,
      builtinAttributes: [builtin],
      onChange: (next) => {
        changed = next
      },
    })
    const clickIndex = (node: ReactNode): boolean => {
      if (Array.isArray(node)) return node.some(clickIndex)
      if (!isValidElement(node)) return false
      const props = node.props as { children?: ReactNode; onClick?: () => void }
      if (node.type === 'button' && props.children === '+ 增加 Index') {
        props.onClick!()
        return true
      }
      return clickIndex(props.children)
    }
    expect(clickIndex(tree)).toBe(true)
    expect(changed.attributes).toEqual([])
    expect(changed.indexes).toEqual([{ type: 'int64_range', name: 'createTime' }])
    expect(renderToStaticMarkup(tree)).toContain('内建只读')

    const expression = renderToStaticMarkup(
      createElement(ExpressionEditor, {
        value: { op: 'int64_ref', source: 'seed_attributes', name: 'createTime' },
        type: 'int64',
        contract: rule.contract,
        builtinAttributes: [builtin],
        onChange: () => {},
      }),
    )
    expect(expression).toContain('value="createTime"')
    expect(expression).not.toContain('请选择有效选项')
    const settings = renderToStaticMarkup(
      createElement(RuleSettingsEditor, {
        document: documentFromPortable(builtinRule()),
        builtinAttributes: [builtin],
        onChange: () => {},
      }),
    )
    expect(settings).toContain('value="createTime"')
    expect(settings).not.toContain('请选择有效选项')
  })

  it('omits readonly values and stale generators only when the server advertises them', () => {
    const values = { createTime: 123, level: 9 }
    expect(omitReadonlyBuiltins(values, [builtin])).toEqual({ level: 9 })
    expect(omitReadonlyBuiltins(values, [])).toEqual(values)
    const generators = { createTime: { type: 'int64' }, level: { type: 'int64' } }
    expect(omitReadonlyBuiltins(generators, [builtin])).toEqual({ level: { type: 'int64' } })
  })

  it('waits for the in-flight capabilities before parsing and accepts a successful legacy response', async () => {
    let finish!: (value: { data: typeof capabilities }) => void
    const pending = new Promise<{ data: typeof capabilities }>((resolve) => {
      finish = resolve
    })
    const refetch = vi.fn(() => pending)
    const parse = vi.fn((value: typeof capabilities) =>
      parseRuleLibraryImport(builtinRule(), value),
    )
    const action = capabilitiesForAction({ refetch }).then(parse)
    expect(refetch).toHaveBeenCalledOnce()
    expect(parse).not.toHaveBeenCalled()
    finish({ data: capabilities })
    expect((await action).rules[0].contract.attributes).toEqual([])
    const legacy = capabilitiesFromWire({ schemaVersions: [] })
    expect(await capabilitiesForAction({ refetch: async () => ({ data: legacy }) })).toBe(legacy)
    await expect(capabilitiesForAction({ refetch: async () => ({}) })).rejects.toThrow(
      '读取服务能力失败',
    )
  })
})
