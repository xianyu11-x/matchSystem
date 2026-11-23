import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api } from './api'
import {
  bindRuleLibraryEditor,
  documentFromPortable,
  newPortableRule,
  parseRuleLibraryImport,
  ruleIdentity,
  useLibraryWorkspace,
  validateLibraryDocuments,
} from './ruleLibrary'
import { portableRuleDocument } from './ruleDocumentIO'
import { parseRuleIdentityJson } from './ruleIdentity'
import { useRuleStore } from './ruleStore'

const initial = () => ({
  revision: '1',
  rules: [newPortableRule({ namespace: '1', ruleId: 1 })],
})

beforeEach(() => {
  useLibraryWorkspace.setState({
    revision: undefined,
    rules: [],
    selected: -1,
    dirty: false,
    serverChanged: false,
    observedRevision: undefined,
    version: 0,
    loadToken: 0,
  })
})
afterEach(() => vi.unstubAllGlobals())

describe('独立规则库草稿', () => {
  it('编辑器更改立即写入当前草稿，切换规则后不会串写', () => {
    useLibraryWorkspace.getState().hydrate(initial())
    const unbind = bindRuleLibraryEditor()
    try {
      useRuleStore.getState().setDocument(useLibraryWorkspace.getState().rules[0])
      useRuleStore.getState().setEnvelope('scoring', { type: 'constant', params: { value: 7 } })
      useLibraryWorkspace.getState().add(true)
      useRuleStore.getState().setDocument(useLibraryWorkspace.getState().rules[1])
      useRuleStore.getState().setEnvelope('scoring', { type: 'constant', params: { value: 12 } })
      useLibraryWorkspace.getState().select(0)
      useRuleStore.getState().setDocument(useLibraryWorkspace.getState().rules[0])
      expect(useRuleStore.getState().document?.scoring.params).toEqual({ value: 7 })
      expect(useLibraryWorkspace.getState().rules[1].scoring.params).toEqual({ value: 12 })
    } finally {
      unbind()
      useRuleStore.getState().clearGraphSession()
    }
  })

  it('在规则切换、复制、删除时保留每个规则的编辑，不创建部署', () => {
    const workspace = useLibraryWorkspace.getState()
    workspace.hydrate(initial())
    const first = useLibraryWorkspace.getState().rules[0]
    useLibraryWorkspace
      .getState()
      .updateCurrent({ ...first, runtime: { ...first.runtime, maxPlayers: 11 } }, true)
    useLibraryWorkspace.getState().add(true)
    let current = useLibraryWorkspace.getState()
    expect(current.rules).toHaveLength(2)
    expect(current.rules[1].apiRule).toEqual({ namespace: '1', ruleId: '2' })
    expect(current.rules[1].runtime.maxPlayers).toBe(11)
    const second = current.rules[1]
    current.updateCurrent({ ...second, runtime: { ...second.runtime, maxPlayers: 20 } }, true)
    useLibraryWorkspace.getState().select(0)
    current = useLibraryWorkspace.getState()
    expect(current.rules[current.selected].runtime.maxPlayers).toBe(11)
    expect(current.rules.map(portableRuleDocument)).toEqual([
      expect.objectContaining({ runtime: expect.objectContaining({ maxPlayers: 11 }) }),
      expect.objectContaining({ runtime: expect.objectContaining({ maxPlayers: 20 }) }),
    ])
    expect(portableRuleDocument(current.rules[0])).not.toHaveProperty('physicalNodeId')
    current.remove()
    expect(useLibraryWorkspace.getState().rules[0].runtime.maxPlayers).toBe(20)
    expect(useLibraryWorkspace.getState().dirty).toBe(true)
  })

  it('刷新遇到新版本时保留草稿，保存期间的新编辑不被标记为已保存', () => {
    useLibraryWorkspace.getState().hydrate(initial())
    const first = useLibraryWorkspace.getState().rules[0]
    useLibraryWorkspace
      .getState()
      .updateCurrent({ ...first, runtime: { ...first.runtime, maxPlayers: 9 } }, true)
    const sentVersion = useLibraryWorkspace.getState().version
    useLibraryWorkspace.getState().hydrate({ revision: '2', rules: [] })
    expect(useLibraryWorkspace.getState()).toMatchObject({
      revision: '1',
      dirty: true,
      serverChanged: true,
    })
    expect(useLibraryWorkspace.getState().rules[0].runtime.maxPlayers).toBe(9)
    const pending = useLibraryWorkspace.getState().rules[0]
    useLibraryWorkspace
      .getState()
      .updateCurrent({ ...pending, runtime: { ...pending.runtime, maxPlayers: 10 } }, true)
    useLibraryWorkspace
      .getState()
      .saved({ revision: '2', rules: [portableRuleDocument(pending)] }, sentVersion)
    expect(useLibraryWorkspace.getState()).toMatchObject({
      revision: '2',
      dirty: true,
      serverChanged: false,
    })
    expect(useLibraryWorkspace.getState().rules[0].runtime.maxPlayers).toBe(10)
    useLibraryWorkspace.getState().hydrate(initial())
    expect(useLibraryWorkspace.getState().rules[0].runtime.maxPlayers).toBe(10)
  })

  it('验证重复身份及编辑中的非法规则 ID', () => {
    const first = documentFromPortable(initial().rules[0])
    const duplicate = documentFromPortable(initial().rules[0])
    expect(validateLibraryDocuments([first, duplicate])).toMatch(/重复/)
    expect(validateLibraryDocuments([{ ...first, apiRule: { ruleId: NaN } }])).toMatch(/uint64/)
  })

  it('同一 RuleID 的不同别名属于同一规则，相同别名可用于不同 RuleID', () => {
    const first = newPortableRule({ namespace: 'old/别名:0', ruleId: '7' })
    const renamed = { ...structuredClone(first), ruleKey: { namespace: 'new', ruleId: '7' } }
    const other = newPortableRule({ namespace: 'old/别名:0', ruleId: '8' })
    expect(ruleIdentity(first.ruleKey)).toBe(ruleIdentity(renamed.ruleKey))
    expect(ruleIdentity(first.ruleKey)).not.toBe(ruleIdentity(other.ruleKey))
    expect(
      validateLibraryDocuments([documentFromPortable(first), documentFromPortable(renamed)]),
    ).toMatch(/重复/)
    const scenario = {
      schemaVersion: 'simulator-scenario/v1',
      rules: [{ rule: first }, { rule: renamed }, { rule: other }],
    }
    const imported = parseRuleLibraryImport(scenario)
    expect(imported.rules.map((rule) => rule.ruleKey)).toEqual([first.ruleKey, other.ruleKey])
    expect(() => parseRuleLibraryImport({ rules: [first, renamed] })).toThrow(/重复/)
    renamed.runtime.maxPlayers = 9
    expect(() => parseRuleLibraryImport(scenario)).toThrow(/多个不同配置/)
  })

  it('旧版大整数规则身份在导入时保留原始十进制值', () => {
    const raw = JSON.stringify(
      newPortableRule({ namespace: '18446744073709551615', ruleId: '18446744073709551615' }),
    ).replace('"ruleId":"18446744073709551615"', '"ruleId":18446744073709551615')
    const imported = parseRuleLibraryImport(parseRuleIdentityJson(raw))
    expect(imported.rules[0].ruleKey).toEqual({
      namespace: '18446744073709551615',
      ruleId: '18446744073709551615',
    })
    expect(() =>
      parseRuleLibraryImport({ ...initial().rules[0], ruleKey: { ruleId: '0001' } }),
    ).toThrow(/ruleKey/)
  })

  it('保存成功后采用服务端规范身份，保留本地图草稿', () => {
    useLibraryWorkspace.getState().hydrate(initial())
    const current = useLibraryWorkspace.getState().rules[0]
    useLibraryWorkspace
      .getState()
      .updateCurrent(
        { ...current, apiRule: { namespace: '2', ruleId: '1' }, ruleKey: 'legacy/1' },
        true,
      )
    const version = useLibraryWorkspace.getState().version
    const saved = newPortableRule({ namespace: '18446744073709551615', ruleId: '1' })
    useLibraryWorkspace.getState().saved({ revision: '2', rules: [saved] }, version)
    expect(useLibraryWorkspace.getState().rules[0].apiRule).toEqual(saved.ruleKey)
    expect(useLibraryWorkspace.getState().rules[0].graph).toEqual(current.graph)
    expect(useLibraryWorkspace.getState().dirty).toBe(false)
  })

  it('导入单规则、整库和旧场景，并拒绝同身份的不同定义', () => {
    const first = initial().rules[0]
    const second = newPortableRule({ ruleId: 2 })
    const normalizedFirst = { ...first, ruleKey: { namespace: '1', ruleId: '1' } }
    const normalizedSecond = { ...second, ruleKey: { ruleId: '2' } }
    expect(parseRuleLibraryImport(first)).toEqual({ kind: 'single', rules: [normalizedFirst] })
    expect(
      parseRuleLibraryImport({ schemaVersion: 'match-rule-library/v1', rules: [first, second] })
        .rules,
    ).toEqual([normalizedFirst, normalizedSecond])
    const scenario = {
      schemaVersion: 'simulator-scenario/v1',
      rules: [
        { rule: first, physicalNodeId: '1' },
        { rule: structuredClone(first), physicalNodeId: '2' },
      ],
    }
    expect(parseRuleLibraryImport({ scenario })).toEqual({
      kind: 'library',
      rules: [normalizedFirst],
    })
    const changed = structuredClone(first)
    changed.runtime.maxPlayers = 19
    expect(() =>
      parseRuleLibraryImport({ ...scenario, rules: [{ rule: first }, { rule: changed }] }),
    ).toThrow(/多个不同配置/)
    expect(() => parseRuleLibraryImport({ rules: [first, first] })).toThrow(/重复/)
    expect(() =>
      parseRuleLibraryImport({ schemaVersion: 'match-rule-library/v2', rules: [] }),
    ).toThrow(/schemaVersion/)
    expect(() =>
      parseRuleLibraryImport({ schemaVersion: 'match-rule-library/v1', rules: [], extra: true }),
    ).toThrow(/不支持的字段/)
    expect(() => parseRuleLibraryImport({ ...first, unknown: true })).toThrow(/不支持的字段/)
  })

  it('区分相邻的最大 uint64 规则身份并拒绝无法精确表示的旧数字', () => {
    const maximum = newPortableRule({
      namespace: '18446744073709551615',
      ruleId: '18446744073709551615',
    })
    const previous = newPortableRule({
      namespace: '18446744073709551615',
      ruleId: '18446744073709551614',
    })
    const imported = parseRuleLibraryImport({
      schemaVersion: 'match-rule-library/v1',
      rules: [maximum, previous],
    })
    expect(imported.rules.map((rule) => rule.ruleKey.ruleId)).toEqual([
      '18446744073709551615',
      '18446744073709551614',
    ])
    expect(
      validateLibraryDocuments(imported.rules.map((rule) => documentFromPortable(rule))),
    ).toBeUndefined()
    expect(ruleIdentity({ ruleId: 1 })).toBe(ruleIdentity({ namespace: '0', ruleId: '1' }))
    expect(() =>
      parseRuleLibraryImport({ ...maximum, ruleKey: { ruleId: 9007199254740992 } }),
    ).toThrow(/ruleKey/)
    expect(() =>
      parseRuleLibraryImport({ ...maximum, ruleKey: { ruleId: '18446744073709551616' } }),
    ).toThrow(/ruleKey/)
  })
})

describe('规则库 API', () => {
  it('只请求独立规则库并携带乐观并发版本，冲突时不提交场景', async () => {
    const snapshot = initial()
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify(snapshot), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ...snapshot, revision: '2' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ error: { message: '冲突' } }), {
          status: 409,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
    vi.stubGlobal('fetch', fetch)
    expect(await api.getRuleLibrary()).toEqual(snapshot)
    expect(await api.replaceRuleLibrary(snapshot)).toEqual({ ...snapshot, revision: '2' })
    await expect(api.replaceRuleLibrary(snapshot)).rejects.toMatchObject({
      status: 409,
    } satisfies Partial<ApiError>)
    expect(fetch.mock.calls.map((call) => call[0])).toEqual([
      '/api/v1/rule-library',
      '/api/v1/rule-library',
      '/api/v1/rule-library',
    ])
    expect(fetch.mock.calls[1][1]).toMatchObject({
      method: 'PUT',
      body: JSON.stringify({
        ...snapshot,
        rules: [{ ...snapshot.rules[0], ruleKey: { namespace: '1', ruleId: '1' } }],
      }),
    })
  })
})
