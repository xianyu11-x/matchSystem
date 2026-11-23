import { create } from 'zustand'
import { buildRuleGraph } from './graphBuilder'
import { portableRuleDocument } from './ruleDocumentIO'
import { useRuleStore } from './ruleStore'
import { validateRuleDocument } from './validation'
import { parseRuleKey, ruleIdentity, ruleIdentityLabel } from './ruleIdentity'
import type { ApiRuleKey, MatchRuleDocument, RuleDocument } from '../types'

export { ruleIdentity, ruleIdentityLabel } from './ruleIdentity'

export interface RuleLibrarySnapshot {
  revision: string
  rules: MatchRuleDocument[]
}

const object = (value: unknown): Record<string, unknown> | undefined =>
  value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined

function sameJson(left: unknown, right: unknown): boolean {
  if (Object.is(left, right)) return true
  if (Array.isArray(left) || Array.isArray(right))
    return (
      Array.isArray(left) &&
      Array.isArray(right) &&
      left.length === right.length &&
      left.every((item, index) => sameJson(item, right[index]))
    )
  const first = object(left)
  const second = object(right)
  if (!first || !second) return false
  const keys = Object.keys(first)
  return (
    keys.length === Object.keys(second).length &&
    keys.every((key) => key in second && sameJson(first[key], second[key]))
  )
}

export function availableRuleKey(rules: RuleDocument[], namespace: string = ''): ApiRuleKey {
  const used = new Set(rules.map((rule) => ruleIdentity(rule.apiRule ?? { ruleId: 0 })))
  let ruleId = 1
  const identity = namespace === '' ? {} : { namespace }
  while (used.has(ruleIdentity({ ...identity, ruleId }))) ruleId++
  return parseRuleKey({ ...identity, ruleId })!
}

export function newPortableRule(key: ApiRuleKey): MatchRuleDocument {
  return {
    schemaVersion: 'match-rule/v1',
    ruleKey: key,
    contract: { schemaVersion: 'logical-node-contract/v3', attributes: [], facts: [], indexes: [] },
    prefilter: {
      schemaVersion: 'prefilter/v3',
      bitmap: { resultType: 'bitmap', expr: { op: 'none' } },
    },
    evaluation: {
      schemaVersion: 'evaluation/v3',
      canJoin: {
        schemaVersion: 'expression-scalar/v3',
        resultType: 'bool',
        expr: { op: 'bool_literal', value: false },
      },
      canComplete: {
        schemaVersion: 'expression-scalar/v3',
        resultType: 'bool',
        expr: { op: 'bool_literal', value: false },
      },
    },
    scoring: { type: 'constant', params: { value: 0 } },
    seedSelection: { type: 'arrival', params: {} },
    runtime: {
      maxPlayers: 8,
      candidateScoringLimitPerSeed: 500,
      candidateLimitPerSeed: 50,
      attemptLimitPerProduceMatch: 500,
      attemptLimitPerMatchRound: 500,
    },
  }
}

let nextEditorId = 0

export function documentFromPortable(rule: MatchRuleDocument, editorId?: string): RuleDocument {
  const copy = structuredClone(rule)
  const document: RuleDocument = {
    ...copy,
    ruleKey: ruleIdentity(copy.ruleKey),
    apiRule: copy.ruleKey,
    // 仅用于隔离编辑器的图缓存，不写入规则库或部署配置。
    placementId: editorId ?? `library-editor-${++nextEditorId}`,
    graph: { nodes: [], edges: [] },
  }
  document.graph = buildRuleGraph(document)
  return document
}

export function validateLibraryDocuments(rules: RuleDocument[]): string | undefined {
  const identities = new Set<string>()
  for (const [index, document] of rules.entries()) {
    const key = document.apiRule
    if (!key || !parseRuleKey(key))
      return `规则 ${index + 1}：规则 ID 必须为无前导零的正 uint64 十进制数，别名须为文本。`
    const identity = ruleIdentity(key)
    if (identities.has(identity))
      return `规则 ${index + 1}：规则身份 ${ruleIdentityLabel(key)} 重复。`
    identities.add(identity)
    const validation = validateRuleDocument(document)
    if (!validation.valid)
      return `规则 ${index + 1}：${validation.errors[0]?.message ?? '配置无效'}`
  }
  return undefined
}

function parsePortable(value: unknown): MatchRuleDocument {
  const item = object(value)
  const key = object(item?.ruleKey)
  if (item?.schemaVersion !== 'match-rule/v1' || !key || !parseRuleKey(key))
    throw new Error('规则必须为 match-rule/v1，且包含有效的 ruleKey。')
  const allowedFields = new Set([
    'schemaVersion',
    'ruleKey',
    'contract',
    'prefilter',
    'evaluation',
    'scoring',
    'seedSelection',
    'runtime',
  ])
  const unknownField = Object.keys(item).find((name) => !allowedFields.has(name))
  const unknownKey = Object.keys(key).find((name) => name !== 'namespace' && name !== 'ruleId')
  if (unknownField || unknownKey)
    throw new Error(`规则包含不支持的字段：${unknownField ?? `ruleKey.${unknownKey}`}。`)
  for (const name of ['contract', 'prefilter', 'evaluation', 'scoring', 'seedSelection', 'runtime'])
    if (!object(item[name])) throw new Error(`规则缺少 ${name} 配置。`)
  try {
    const document = documentFromPortable(item as unknown as MatchRuleDocument)
    const problem = validateLibraryDocuments([document])
    if (problem) throw new Error(problem)
    return portableRuleDocument(document)
  } catch (error) {
    throw new Error(`规则文件无效：${error instanceof Error ? error.message : '配置格式错误'}`)
  }
}

export function parseRuleLibraryImport(value: unknown): {
  kind: 'single' | 'library'
  rules: MatchRuleDocument[]
} {
  const root = object(value)
  if (!root) throw new Error('JSON 根节点必须是对象。')
  if (root.schemaVersion === 'match-rule/v1')
    return { kind: 'single', rules: [parsePortable(root)] }
  if (
    root.schemaVersion !== undefined &&
    root.schemaVersion !== 'match-rule-library/v1' &&
    root.schemaVersion !== 'simulator-scenario/v1'
  )
    throw new Error('不支持的规则库或场景 schemaVersion。')
  if (
    root.schemaVersion === 'match-rule-library/v1' &&
    Object.keys(root).some((key) => key !== 'schemaVersion' && key !== 'rules')
  )
    throw new Error('规则库包含不支持的字段。')
  const scenario = object(root.scenario) ?? root
  const legacyScenario = scenario.schemaVersion === 'simulator-scenario/v1'
  const source = legacyScenario ? scenario.rules : root.rules
  if (!Array.isArray(source)) throw new Error('请选择规则、规则库或 simulator-scenario/v1 文件。')
  const items = source.map((item) => parsePortable(legacyScenario ? object(item)?.rule : item))
  const unique = new Map<string, MatchRuleDocument>()
  for (const rule of items) {
    const identity = ruleIdentity(rule.ruleKey)
    const existing = unique.get(identity)
    if (existing && !legacyScenario)
      throw new Error(`规则身份 ${ruleIdentityLabel(rule.ruleKey)} 重复。`)
    if (existing) {
      if (
        !sameJson(
          { ...existing, ruleKey: { ruleId: identity } },
          { ...rule, ruleKey: { ruleId: identity } },
        )
      )
        throw new Error(`规则身份 ${ruleIdentityLabel(rule.ruleKey)} 对应多个不同配置。`)
      continue
    }
    unique.set(identity, rule)
  }
  return { kind: 'library', rules: [...unique.values()] }
}

interface LibraryWorkspace {
  revision?: string
  rules: RuleDocument[]
  selected: number
  dirty: boolean
  serverChanged: boolean
  observedRevision?: string
  version: number
  loadToken: number
  hydrate: (snapshot: RuleLibrarySnapshot, force?: boolean) => void
  select: (index: number) => void
  updateCurrent: (document: RuleDocument, changed: boolean) => void
  add: (copy?: boolean) => void
  remove: () => void
  importRules: (value: ReturnType<typeof parseRuleLibraryImport>) => void
  saved: (snapshot: RuleLibrarySnapshot, sentVersion: number) => void
}

export const useLibraryWorkspace = create<LibraryWorkspace>((set) => ({
  rules: [],
  selected: -1,
  dirty: false,
  serverChanged: false,
  version: 0,
  loadToken: 0,
  hydrate: (snapshot, force = false) =>
    set((state) => {
      if (!force && state.revision === snapshot.revision) return state
      if (!force && state.dirty) return { serverChanged: true, observedRevision: snapshot.revision }
      const previous = state.rules[state.selected]?.apiRule
      const rules = snapshot.rules.map((rule) => documentFromPortable(rule))
      const same = previous
        ? rules.findIndex((rule) => ruleIdentity(rule.apiRule!) === ruleIdentity(previous))
        : -1
      return {
        revision: snapshot.revision,
        rules,
        selected: same >= 0 ? same : rules.length ? 0 : -1,
        dirty: false,
        serverChanged: false,
        observedRevision: undefined,
        loadToken: state.loadToken + 1,
      }
    }),
  select: (selected) =>
    set((state) =>
      selected >= 0 && selected < state.rules.length && selected !== state.selected
        ? { selected, loadToken: state.loadToken + 1 }
        : state,
    ),
  updateCurrent: (document, changed) =>
    set((state) => {
      if (!state.rules[state.selected] || state.rules[state.selected] === document) return state
      const rules = state.rules.slice()
      rules[state.selected] = document
      return { rules, dirty: state.dirty || changed, version: state.version + (changed ? 1 : 0) }
    }),
  add: (copy = false) =>
    set((state) => {
      const source = copy ? state.rules[state.selected] : undefined
      const key = availableRuleKey(state.rules, source?.apiRule?.namespace ?? '')
      const rule = source ? { ...portableRuleDocument(source), ruleKey: key } : newPortableRule(key)
      const rules = [...state.rules, documentFromPortable(rule)]
      return {
        rules,
        selected: rules.length - 1,
        dirty: true,
        version: state.version + 1,
        loadToken: state.loadToken + 1,
      }
    }),
  remove: () =>
    set((state) => {
      if (state.selected < 0) return state
      const rules = state.rules.filter((_, index) => index !== state.selected)
      return {
        rules,
        selected: rules.length ? Math.min(state.selected, rules.length - 1) : -1,
        dirty: true,
        version: state.version + 1,
        loadToken: state.loadToken + 1,
      }
    }),
  importRules: ({ kind, rules: imported }) =>
    set((state) => {
      if (kind === 'library') {
        const rules = imported.map((rule) => documentFromPortable(rule))
        return {
          rules,
          selected: rules.length ? 0 : -1,
          dirty: true,
          version: state.version + 1,
          loadToken: state.loadToken + 1,
        }
      }
      const rule = imported[0]
      const index = state.rules.findIndex(
        (item) => ruleIdentity(item.apiRule!) === ruleIdentity(rule.ruleKey),
      )
      const rules = state.rules.slice()
      const selected = index < 0 ? rules.length : index
      rules[selected] = documentFromPortable(rule)
      return {
        rules,
        selected,
        dirty: true,
        version: state.version + 1,
        loadToken: state.loadToken + 1,
      }
    }),
  saved: (snapshot, sentVersion) =>
    set((state) => {
      const dirty = state.version !== sentVersion
      let changedIdentity = false
      const rules =
        dirty || snapshot.rules.length !== state.rules.length
          ? state.rules
          : state.rules.map((document, index) => {
              const key = parseRuleKey(snapshot.rules[index].ruleKey)
              if (
                !key ||
                (ruleIdentity(document.apiRule!) === ruleIdentity(key) &&
                  document.apiRule?.namespace === key.namespace &&
                  document.ruleKey === ruleIdentity(key))
              )
                return document
              changedIdentity = true
              return { ...document, apiRule: key, ruleKey: ruleIdentity(key) }
            })
      return {
        revision: snapshot.revision,
        dirty,
        rules,
        loadToken: state.loadToken + (changedIdentity ? 1 : 0),
        serverChanged: Boolean(
          state.observedRevision && state.observedRevision !== snapshot.revision,
        ),
        observedRevision:
          state.observedRevision === snapshot.revision ? undefined : state.observedRevision,
      }
    }),
}))

export function bindRuleLibraryEditor(): () => void {
  return useRuleStore.subscribe((state, previous) => {
    if (state.document && state.document !== previous.document)
      useLibraryWorkspace.getState().updateCurrent(state.document, state.dirty)
  })
}
