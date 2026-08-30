import {
  demoCapabilities,
  demoMatches,
  demoRule,
  demoScenario,
  demoTickets,
  demoTicketsPage,
  demoTopology,
} from './mockData'
import { buildRuleGraph } from './graphBuilder'
import type {
  ApiRuleKey,
  BatchGeneratorResponse,
  BatchGeneratorSpec,
  CapabilityNode,
  Capabilities,
  CandidateScoringConfig,
  FactSnapshot,
  JsonObject,
  JsonValue,
  MatchRecord,
  MatchRuleDocument,
  RuleDocument,
  RuleRuntimeConfig,
  RuleSummary,
  RunRoundInput,
  RunRoundResponse,
  Scenario,
  SeedSelectionConfig,
  Ticket,
  TicketInput,
  TicketsPage,
  Topology,
  ValidationResponse,
  ValueType,
} from '../types'

const runtimeApiBase =
  typeof window !== 'undefined'
    ? (window.__MATCH_API_BASE_URL__ ??
      new URLSearchParams(window.location.search).get('apiBase') ??
      undefined)
    : undefined
const apiBase = (runtimeApiBase ?? import.meta.env.VITE_API_BASE_URL ?? '/api/v1').replace(
  /\/$/,
  '',
)
export const isDemoMode = import.meta.env.VITE_DEMO_MODE === 'true'

type WireObject = Record<string, unknown>
type WireTypedValues = {
  stringLists?: Record<string, string[]>
  uint64Lists?: Record<string, number[]>
  int64Values?: Record<string, number>
}
type WireRuleKey = { namespace?: string; ruleId: number }
type WireTicket = WireTypedValues & { ticketId: number; createdAt: number }
type WireFacts = WireTypedValues
type WireOwner = { physicalNodeId: string; logicalNode?: WireObject }
type WireTicketView = {
  ticket: WireTicket
  facts?: WireFacts
  owner?: WireOwner
  route?: { owner?: WireOwner }
  state: string
}
type WireMatch = {
  matchId?: string
  id?: string
  logicalNode: WireObject
  tickets: WireTicket[]
  facts?: WireFacts
  createdAt?: number
}
type WireScenarioResponse = { revision?: string; scenario: unknown }
type WireCapabilitiesResponse = {
  schemaVersions: string[]
  selectors?: string[]
  candidateScorers?: string[]
  seedSelections?: string[]
  factTypes?: string[]
  expressionOps?: string[]
  bitmapOps?: string[]
  scalarOperators?: WireOperatorCapability[]
  bitmapOperators?: WireOperatorCapability[]
  indexTypes?: string[]
  factScopes?: string[]
  limits?: Record<string, number>
}
type WireOperatorCapability = {
  name: string
  resultType: string
  inputs?: string[]
  fields: string[]
}
type WireTopologyResponse = {
  physicalNodes: Array<{
    physicalNodeId: string
    endpoint?: string
    enabled: boolean
    logicalNodes?: Array<{ key: WireObject; state: string; ticketCount: number }>
  }>
}
type WireBatchResponse = {
  accepted: number
  rejected?: number
  generatorId?: string
  seed?: number
  tickets?: WireTicketView[]
  issues?: Array<{ path?: string; code: string; message: string; severity?: string }>
}
type WireRoundResponse = {
  roundId?: string
  produced: number
  matches?: WireMatch[]
  topology?: WireTopologyResponse
}
type WireMatchPage = { items: WireMatch[]; nextCursor?: string; total?: number }

const asObject = (value: unknown): WireObject =>
  value && typeof value === 'object' && !Array.isArray(value) ? (value as WireObject) : {}
const valueAt = (object: WireObject | undefined, ...keys: string[]): unknown => {
  for (const key of keys) if (object && key in object) return object[key]
  return undefined
}
const asNumber = (value: unknown, fallback = 0): number => {
  const parsed = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(parsed) ? parsed : fallback
}
const asString = (value: unknown, fallback = '') =>
  typeof value === 'string'
    ? value
    : value === undefined || value === null
      ? fallback
      : String(value)

function toApiRuleKey(value: ApiRuleKey | undefined, display = ''): WireRuleKey {
  if (value && Number.isInteger(value.ruleId) && value.ruleId > 0)
    return { namespace: value.namespace, ruleId: value.ruleId }
  const parts = display.split(/[/:#]/).filter(Boolean)
  const ruleId = asNumber(parts.at(-1), 1)
  return {
    namespace: parts.length > 1 ? parts.slice(0, -1).join('/') : undefined,
    ruleId: Math.max(1, Math.trunc(ruleId)),
  }
}

function fromApiRuleKey(value: unknown): ApiRuleKey {
  const object = asObject(value)
  const namespaceValue = valueAt(object, 'namespace')
  return {
    namespace:
      namespaceValue === undefined || namespaceValue === null
        ? undefined
        : asString(namespaceValue) || undefined,
    ruleId: Math.max(1, Math.trunc(asNumber(valueAt(object, 'ruleId'), 1))),
  }
}

function ruleKeyText(rule: ApiRuleKey): string {
  return rule.namespace ? `${rule.namespace}/${rule.ruleId}` : String(rule.ruleId)
}

function timestampToIso(value: unknown): string {
  const numeric = asNumber(value)
  if (!numeric) return new Date().toISOString()
  const milliseconds = numeric > 1e15 ? numeric / 1e6 : numeric > 1e12 ? numeric : numeric * 1000
  return new Date(milliseconds).toISOString()
}

function typedAttributes(value: WireTypedValues | undefined) {
  return {
    strings: value?.stringLists ?? {},
    uint64s: value?.uint64Lists ?? {},
    int64: value?.int64Values ?? {},
  }
}

function wireFacts(value: WireFacts | undefined): FactSnapshot {
  const facts: FactSnapshot = {}
  for (const [name, values] of Object.entries(value?.stringLists ?? {})) facts[name] = values
  for (const [name, values] of Object.entries(value?.uint64Lists ?? {})) facts[name] = values
  for (const [name, item] of Object.entries(value?.int64Values ?? {})) facts[name] = item
  return facts
}

function factSnapshot(value: unknown): FactSnapshot {
  const object = asObject(value)
  const stringLists = valueAt(object, 'stringLists', 'strings')
  const uint64Lists = valueAt(object, 'uint64Lists', 'uint64s')
  const int64Values = valueAt(object, 'int64Values', 'int64s')
  return wireFacts({
    stringLists: asObject(stringLists) as Record<string, string[]>,
    uint64Lists: asObject(uint64Lists) as Record<string, number[]>,
    int64Values: asObject(int64Values) as Record<string, number>,
  })
}

function ownerFromWire(owner: WireOwner | undefined) {
  if (!owner) return undefined
  const logical = asObject(owner.logicalNode)
  const rule = fromApiRuleKey(valueAt(logical, 'rule'))
  const placementId = asString(valueAt(logical, 'placementId'), 'default')
  return {
    physicalNodeId: owner.physicalNodeId,
    logicalNodeKey: `${ruleKeyText(rule)}/${placementId}`,
    ruleKey: ruleKeyText(rule),
    placementId,
  }
}

function ticketFromWire(value: WireTicketView): Ticket {
  const routeOwner = ownerFromWire(value.owner ?? value.route?.owner)
  const state = value.state === 'removed' ? 'expired' : value.state
  return {
    ticketId: String(value.ticket.ticketId),
    createdAt: timestampToIso(value.ticket.createdAt),
    attributes: typedAttributes(value.ticket),
    facts: wireFacts(value.facts),
    routeDecision: routeOwner ? { status: 'routed', owner: routeOwner } : { status: 'pending' },
    status:
      state === 'waiting' || state === 'matched' || state === 'expired' || state === 'rejected'
        ? state
        : 'waiting',
    matchId: undefined,
  }
}

function matchFromWire(value: WireMatch): MatchRecord {
  const logical = asObject(value.logicalNode)
  const rule = fromApiRuleKey(valueAt(logical, 'rule'))
  const placementId = asString(valueAt(logical, 'placementId'), 'default')
  return {
    matchId: value.matchId ?? value.id ?? `match-${value.createdAt ?? Date.now()}`,
    createdAt: timestampToIso(value.createdAt),
    roundId: '',
    ruleKey: ruleKeyText(rule),
    placementId,
    ticketIds: (value.tickets ?? []).map((ticket) => String(ticket.ticketId)),
    memberCount: value.tickets?.length ?? 0,
    facts: factSnapshot(value.facts),
  }
}

function matchRuleFromDocument(document: RuleDocument): MatchRuleDocument {
  return {
    schemaVersion: 'match-rule/v1',
    ruleKey: toApiRuleKey(document.apiRule, document.ruleKey),
    contract: structuredClone(document.contract),
    prefilter: structuredClone(document.prefilter),
    evaluation: structuredClone(document.evaluation),
    scoring: structuredClone(document.scoring),
    seedSelection: structuredClone(document.seedSelection),
    runtime: structuredClone(document.runtime),
  }
}

const defaultScoring: CandidateScoringConfig = {
  type: 'created_at',
  params: { direction: 'descending' },
}
const defaultSeedSelection: SeedSelectionConfig = { type: 'arrival', params: {} }
const defaultRuntime: RuleRuntimeConfig = {
  candidateLimitPerSeed: 128,
  maxPlayers: 8,
  attemptLimitPerProduceMatch: 500,
  attemptLimitPerMatchRound: 500,
}

function scenarioFromWire(response: WireScenarioResponse): Scenario {
  const source = asObject(response.scenario)
  const rules = Array.isArray(source.rules) ? source.rules : []
  const summaries = rules.map((raw) => {
    const item = asObject(raw)
    const logical = asObject(valueAt(item, 'logicalNode'))
    const aggregate = asObject(valueAt(item, 'rule'))
    const apiRule = fromApiRuleKey(valueAt(aggregate, 'ruleKey'))
    const placementId = asString(valueAt(logical, 'placementId'), 'default')
    const ruleKey = ruleKeyText(apiRule)
    const scoring = valueAt(aggregate, 'scoring') as CandidateScoringConfig | undefined
    const seedSelection = valueAt(aggregate, 'seedSelection') as SeedSelectionConfig | undefined
    const runtime = valueAt(aggregate, 'runtime') as RuleRuntimeConfig | undefined
    return {
      ruleKey,
      apiRule,
      placementId,
      displayName: `${ruleKey} · ${placementId}`,
      enabled: valueAt(item, 'enabled') !== false,
      contract: asObject(valueAt(aggregate, 'contract')) as unknown as RuleSummary['contract'],
      prefilter: asObject(valueAt(aggregate, 'prefilter')) as unknown as RuleSummary['prefilter'],
      evaluation: asObject(valueAt(aggregate, 'evaluation')) as unknown as RuleSummary['evaluation'],
      scoring: scoring ?? defaultScoring,
      seedSelection: seedSelection ?? defaultSeedSelection,
      runtime: runtime ?? defaultRuntime,
      tickFacts: factSnapshot(valueAt(item, 'tickFacts')),
    }
  })
  return {
    scenarioId: asString(valueAt(source, 'scenarioId', 'id', 'ID'), 'scenario-v1'),
    name: asString(valueAt(source, 'name', 'Name'), '当前模拟场景'),
    updatedAt: response.revision || new Date().toISOString(),
    activeRuleKey: summaries[0]?.ruleKey ?? '',
    rules: summaries,
    tickFacts: summaries[0]?.tickFacts ?? {},
    rawScenario: source as Scenario['rawScenario'],
    revision: response.revision,
  }
}

function ruleDocumentFromSummary(summary: Scenario['rules'][number]): RuleDocument {
  return {
    schemaVersion: 'match-rule/v1',
    ruleKey: summary.ruleKey,
    placementId: summary.placementId,
    apiRule: summary.apiRule,
    contract: summary.contract,
    prefilter: summary.prefilter,
    evaluation: summary.evaluation,
    scoring: summary.scoring ?? defaultScoring,
    seedSelection: summary.seedSelection ?? defaultSeedSelection,
    runtime: summary.runtime ?? defaultRuntime,
    tickFacts: summary.tickFacts,
    graph: buildRuleGraph(summary),
  }
}

function capabilitiesFromWire(response: WireCapabilitiesResponse): Capabilities {
  const scalarCatalog = response.scalarOperators ?? []
  const bitmapCatalog = response.bitmapOperators ?? []
  const legacyExpressionOps = response.expressionOps ?? []
  const legacyBitmapOps = response.bitmapOps ?? []
  const expressionOps =
    legacyExpressionOps.length > 0
      ? legacyExpressionOps
      : scalarCatalog.map((operator) => operator.name)
  const bitmapOps =
    legacyBitmapOps.length > 0 ? legacyBitmapOps : bitmapCatalog.map((operator) => operator.name)
  const hasCatalog = scalarCatalog.length > 0 || bitmapCatalog.length > 0
  const has = (names: string[]) => !hasCatalog || names.some((name) => expressionOps.includes(name))
  const baseNodeTypes = demoCapabilities.nodeTypes.filter((node) => {
    if (node.type === 'prefilter.generic' || node.type === 'expression.generic') return !hasCatalog
    if (node.type === 'prefilter.lookup')
      return !hasCatalog || bitmapOps.some((op) => op.startsWith('lookup_'))
    if (node.type === 'prefilter.exclude') return !hasCatalog || bitmapOps.includes('exclude')
    if (node.type === 'prefilter.combine')
      return !hasCatalog || bitmapOps.includes('and') || bitmapOps.includes('or')
    if (node.type === 'literal.string') return has(['strings_literal'])
    if (node.type === 'literal.int64') return has(['int64_literal'])
    if (node.type === 'literal.uint64') return has(['uint64s_literal'])
    if (node.type === 'literal.bool') return has(['bool_literal'])
    if (node.type === 'compare.int64') return has(['int64_eq', 'int64_neq', 'int64_lt'])
    if (node.type === 'compare.strings') return has(['strings_eq', 'strings_contains_any'])
    if (node.type === 'logic.and') return has(['bool_and'])
    if (node.type === 'logic.or') return has(['bool_or'])
    if (node.type === 'logic.not') return has(['bool_not'])
    return true
  })
  const genericNodeTypes = [
    ...scalarCatalog.map((operator) => ({ operator, bitmap: false })),
    ...bitmapCatalog.map((operator) => ({ operator, bitmap: true })),
  ].map(({ operator, bitmap }) => {
    const inputTypes = inputTypesForCapabilityOp(operator.name, operator.inputs)
    return {
      type: (bitmap ? 'prefilter.generic' : 'expression.generic') as CapabilityNode['type'],
      op: operator.name,
      label: operator.name,
      description: `服务端 capability：${operator.name}`,
      category: (bitmap ? 'prefilter' : 'expression') as CapabilityNode['category'],
      outputType: bitmap
        ? ('bitmap' as const)
        : outputTypeForCapabilityOp(operator.name, operator.resultType),
      inputTypes,
      maxInputs: operator.inputs?.some((input) => input.endsWith('[]')) ? 16 : inputTypes.length,
      fields: operator.fields,
    }
  })
  const legacyOperators: Array<{ op: string; bitmap: boolean }> = [
    ...(scalarCatalog.length === 0 ? expressionOps.map((op) => ({ op, bitmap: false })) : []),
    ...(bitmapCatalog.length === 0 ? bitmapOps.map((op) => ({ op, bitmap: true })) : []),
  ]
  const legacyGenericNodeTypes = legacyOperators.map(({ op, bitmap }) => ({
    type: (bitmap ? 'prefilter.generic' : 'expression.generic') as CapabilityNode['type'],
    op,
    label: op,
    description: `服务端 capability：${op}`,
    category: (bitmap ? 'prefilter' : 'expression') as CapabilityNode['category'],
    outputType: bitmap ? ('bitmap' as const) : outputTypeForCapabilityOp(op),
    inputTypes: inputTypesForCapabilityOp(op),
    maxInputs: 16,
  }))
  const nodeTypes = [...baseNodeTypes, ...genericNodeTypes, ...legacyGenericNodeTypes]
  return {
    schemaVersions: response.schemaVersions ?? [],
    candidateScorers: response.candidateScorers ?? [],
    seedSelections: response.seedSelections ?? [],
    sources: [
      'seed_attributes',
      'seed_facts',
      'tick_facts',
      'candidate_attributes',
      'candidate_facts',
      'match_facts',
    ],
    nodeTypes,
    limits: response.limits ?? {},
    expressionOps,
    bitmapOps,
    indexTypes: response.indexTypes ?? [],
    factTypes: response.factTypes ?? ['strings', 'int64', 'uint64s'],
    scalarOperators: scalarCatalog,
    bitmapOperators: bitmapCatalog,
  }
}

function outputTypeForCapabilityOp(op: string, declaredType?: string): ValueType {
  if (
    declaredType === 'bool' ||
    declaredType === 'int64' ||
    declaredType === 'strings' ||
    declaredType === 'uint64s'
  )
    return declaredType
  if (
    op.startsWith('bool_') ||
    op.includes('_eq') ||
    op.includes('_neq') ||
    op.includes('_lt') ||
    op.includes('_lte') ||
    op.includes('_gt') ||
    op.includes('_gte') ||
    op.endsWith('_is_empty') ||
    op.includes('contains') ||
    op.includes('intersects')
  )
    return 'bool'
  if (op.startsWith('int64_')) return 'int64'
  if (op.startsWith('uint64s_')) return 'uint64s'
  if (op.startsWith('strings_')) return 'strings'
  return 'bool'
}

function inputTypesForCapabilityOp(op: string, declaredInputs?: string[]): ValueType[] {
  if (declaredInputs) {
    const types = declaredInputs.flatMap((input) => {
      const type = input.replace(/\[\]$/, '')
      return type === 'bool' ||
        type === 'int64' ||
        type === 'strings' ||
        type === 'uint64s' ||
        type === 'bitmap'
        ? [type as ValueType]
        : []
    })
    if (types.length > 0) return types
  }
  if (op === 'exclude') return ['bitmap']
  if (op === 'and' || op === 'or') return ['bitmap', 'bitmap']
  if (op === 'if') return ['bool', 'bitmap', 'bitmap']
  if (op === 'lookup_string') return ['strings']
  if (op === 'lookup_uint64') return ['uint64s']
  if (op === 'lookup_range') return ['int64', 'int64']
  if (op === 'bool_not' || op.endsWith('_is_empty'))
    return [
      op.endsWith('uint64s_is_empty')
        ? 'uint64s'
        : op.endsWith('strings_is_empty')
          ? 'strings'
          : 'bool',
    ]
  if (op === 'bool_and' || op === 'bool_or') return ['bool', 'bool']
  if (op.startsWith('int64_') && !op.endsWith('_literal') && !op.endsWith('_ref'))
    return ['int64', 'int64']
  if (op.startsWith('uint64s_') && !op.endsWith('_literal') && !op.endsWith('_ref'))
    return ['uint64s', 'uint64s']
  if (op.startsWith('strings_') && !op.endsWith('_literal') && !op.endsWith('_ref'))
    return ['strings', 'strings']
  return []
}

function topologyFromWire(response: WireTopologyResponse): Topology {
  const nodes = response.physicalNodes.flatMap((physical) =>
    (physical.logicalNodes ?? []).map((logical) => {
      const key = asObject(logical.key)
      const rule = fromApiRuleKey(valueAt(key, 'rule'))
      const placementId = asString(valueAt(key, 'placementId'), 'default')
      const ticketCount = logical.ticketCount ?? 0
      return {
        id: `${physical.physicalNodeId}/${placementId}`,
        name: `${physical.physicalNodeId} / ${placementId}`,
        ruleKey: ruleKeyText(rule),
        placementId,
        ticketCount,
        state:
          logical.state === 'running' || logical.state === 'healthy'
            ? 'healthy'
            : logical.state === 'stopped'
              ? 'stopped'
              : ('degraded' as Topology['nodes'][number]['state']),
        load: Math.min(1, ticketCount / 100),
      }
    }),
  )
  return { updatedAt: new Date().toISOString(), nodes, routes: [] }
}

export class ApiError extends Error {
  readonly status: number
  readonly details?: unknown

  constructor(message: string, status = 0, details?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.details = details
  }
}

const unwrap = <T>(payload: unknown): T => {
  if (payload && typeof payload === 'object' && 'data' in payload) {
    return (payload as { data: T }).data
  }
  return payload as T
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${apiBase}${path}`, {
    ...init,
    headers: {
      Accept: 'application/json',
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
  })
  const contentType = response.headers.get('content-type') ?? ''
  const payload = contentType.includes('json') ? await response.json() : await response.text()
  if (!response.ok) {
    const payloadObject = asObject(payload)
    const errorObject = asObject(valueAt(payloadObject, 'error'))
    const message = asString(
      valueAt(errorObject, 'message', 'Message', 'error', 'Error'),
      `请求失败（HTTP ${response.status}）`,
    )
    throw new ApiError(message, response.status, payload)
  }
  return unwrap<T>(payload)
}

const json = (body: unknown): RequestInit => ({ method: 'POST', body: JSON.stringify(body) })

const waitForDemo = async <T>(value: T): Promise<T> => {
  await new Promise((resolve) => globalThis.setTimeout(resolve, 120))
  return value
}

const clone = <T>(value: T): T => structuredClone(value)

/** Build the exact raw scenario sent to PUT /scenario after one rule edit. */
export function scenarioPayload(scenario: Scenario, rule: RuleDocument): JsonObject {
  const raw = clone(scenario.rawScenario ?? {}) as JsonObject
  const rules = Array.isArray(raw.rules) ? raw.rules : []
  const aggregate = matchRuleFromDocument(rule)
  const target = rules.find((item: unknown) => {
    const logical = asObject(valueAt(asObject(item), 'logicalNode'))
    const key = fromApiRuleKey(valueAt(logical, 'rule'))
    return (
      ruleKeyText(key) === rule.ruleKey &&
      asString(valueAt(logical, 'placementId'), 'default') === rule.placementId
    )
  })
  if (target && typeof target === 'object' && !Array.isArray(target)) {
    const targetObject = target as JsonObject
    targetObject.rule = aggregate as unknown as JsonValue
    delete targetObject.contract
    delete targetObject.prefilter
    delete targetObject.evaluation
  } else {
    rules.push({
      logicalNode: {
        rule: toApiRuleKey(rule.apiRule, rule.ruleKey) as unknown as JsonValue,
        placementId: rule.placementId,
      } as unknown as JsonValue,
      enabled: true,
      rule: aggregate as unknown as JsonValue,
      tickFacts: (rule.tickFacts ?? {}) as unknown as JsonValue,
    } as unknown as JsonValue)
  }
  raw.rules = rules as unknown as JsonValue
  return raw
}

export const api = {
  async getHealth(): Promise<{ status: string; service?: string; version?: string }> {
    return isDemoMode
      ? waitForDemo({ status: 'ok', service: 'match-simulator', version: 'demo' })
      : request('/health')
  },

  async getCapabilities(): Promise<Capabilities> {
    return isDemoMode
      ? waitForDemo(clone(demoCapabilities))
      : request<WireCapabilitiesResponse>('/capabilities').then(capabilitiesFromWire)
  },

  async getScenario(): Promise<Scenario> {
    return isDemoMode
      ? waitForDemo(clone(demoScenario))
      : request<WireScenarioResponse>('/scenario').then(scenarioFromWire)
  },

  async replaceScenario(scenario: Scenario, rule: RuleDocument): Promise<Scenario> {
    if (isDemoMode) {
      const next = clone(scenario)
      const index = next.rules.findIndex(
        (item) => item.ruleKey === rule.ruleKey && item.placementId === rule.placementId,
      )
      if (index >= 0)
        next.rules[index] = {
          ...next.rules[index],
          contract: rule.contract,
          prefilter: rule.prefilter,
          evaluation: rule.evaluation,
          scoring: rule.scoring,
          seedSelection: rule.seedSelection,
          runtime: rule.runtime,
        }
      const demoIndex = demoScenario.rules.findIndex(
        (item) => item.ruleKey === rule.ruleKey && item.placementId === rule.placementId,
      )
      if (demoIndex >= 0)
        demoScenario.rules[demoIndex] = {
          ...demoScenario.rules[demoIndex],
          contract: rule.contract,
          prefilter: rule.prefilter,
          evaluation: rule.evaluation,
          scoring: rule.scoring,
          seedSelection: rule.seedSelection,
          runtime: rule.runtime,
        }
      if (rule.ruleKey === demoRule.ruleKey && rule.placementId === demoRule.placementId) {
        demoRule.contract = rule.contract
        demoRule.prefilter = rule.prefilter
        demoRule.evaluation = rule.evaluation
        demoRule.scoring = rule.scoring
        demoRule.seedSelection = rule.seedSelection
        demoRule.runtime = rule.runtime
        demoRule.graph = buildRuleGraph(demoRule)
      }
      return waitForDemo(next)
    }
    const raw = scenarioPayload(scenario, rule)
    return request<WireScenarioResponse>('/scenario', {
      method: 'PUT',
      body: JSON.stringify({ scenario: raw }),
    }).then(scenarioFromWire)
  },

  async replaceScenarioPayload(rawScenario: JsonObject): Promise<Scenario> {
    if (isDemoMode) throw new ApiError('演示模式不支持导入完整场景')
    return request<WireScenarioResponse>('/scenario', {
      method: 'PUT',
      body: JSON.stringify({ scenario: rawScenario }),
    }).then(scenarioFromWire)
  },

  async getRule(ruleKey: string, placementId: string): Promise<RuleDocument> {
    if (isDemoMode) {
      const summary = demoScenario.rules.find(
        (rule) => rule.ruleKey === ruleKey && rule.placementId === placementId,
      )
      return waitForDemo(
        clone(summary ? ruleDocumentFromSummary(summary) : { ...demoRule, ruleKey, placementId }),
      )
    }
    return api.getScenario().then((scenario) => {
      const summary = scenario.rules.find(
        (rule) => rule.ruleKey === ruleKey && rule.placementId === placementId,
      )
      if (!summary) throw new ApiError('场景中没有可编辑的规则')
      return ruleDocumentFromSummary(summary)
    })
  },

  async getTopology(): Promise<Topology> {
    return isDemoMode
      ? waitForDemo(clone(demoTopology))
      : request<WireTopologyResponse>('/topology').then(topologyFromWire)
  },

  async getTickets(params: {
    cursor?: string
    limit?: number
    search?: string
    status?: string
  }): Promise<TicketsPage> {
    if (isDemoMode) return waitForDemo(clone(demoTicketsPage(params.search, params.status)))
    const query = new URLSearchParams()
    if (params.cursor) query.set('cursor', params.cursor)
    if (params.limit) query.set('limit', String(params.limit))
    if (params.search?.trim()) query.set('search', params.search.trim())
    if (params.status && params.status !== 'all') query.set('state', params.status)
    const suffix = query.toString()
    return request<{ items: WireTicketView[]; nextCursor?: string; total?: number }>(
      `/tickets${suffix ? `?${suffix}` : ''}`,
    ).then((page) => ({
      items: page.items.map(ticketFromWire),
      nextCursor: page.nextCursor,
      total: page.total,
    }))
  },

  async createTicket(input: TicketInput): Promise<Ticket> {
    if (isDemoMode) {
      const ticket: Ticket = {
        ticketId:
          input.ticketId?.trim() || `ticket-${String(demoTickets.length + 1).padStart(4, '0')}`,
        createdAt: new Date().toISOString(),
        attributes: clone(input.attributes),
        facts: clone(input.facts),
        status: 'waiting',
        routeDecision: {
          status: 'routed',
          owner: demoTopology.nodes[0]
            ? {
                physicalNodeId: demoTopology.nodes[0].id,
                logicalNodeKey: `${demoTopology.nodes[0].ruleKey}/${demoTopology.nodes[0].placementId}`,
                ruleKey: demoTopology.nodes[0].ruleKey,
                placementId: demoTopology.nodes[0].placementId,
              }
            : undefined,
        },
      }
      demoTickets.unshift(ticket)
      return waitForDemo(clone(ticket))
    }
    const ticketId = input.ticketId?.trim() ? Number(input.ticketId) : Date.now()
    if (!Number.isSafeInteger(ticketId) || ticketId <= 0)
      throw new ApiError('Ticket ID 必须是正整数')
    const rule = toApiRuleKey(input.rule, input.rule?.namespace)
    const wireInput = {
      ticket: {
        ticketId,
        createdAt: Date.now(),
        stringLists: input.attributes.strings,
        uint64Lists: input.attributes.uint64s,
        int64Values: input.attributes.int64,
      },
      rule,
      placementId: input.placementId,
      affinityKey: input.affinityKey,
      facts: {
        stringLists: Object.fromEntries(
          Object.entries(input.facts).filter(
            ([, value]) => Array.isArray(value) && value.every((item) => typeof item === 'string'),
          ),
        ),
        uint64Lists: Object.fromEntries(
          Object.entries(input.facts).filter(
            ([, value]) => Array.isArray(value) && value.every((item) => typeof item === 'number'),
          ),
        ),
        int64Values: Object.fromEntries(
          Object.entries(input.facts).filter(
            ([, value]) => typeof value === 'number' && !Array.isArray(value),
          ),
        ),
      },
    }
    return request<WireTicketView>('/tickets', json(wireInput)).then(ticketFromWire)
  },

  async createBatch(spec: BatchGeneratorSpec): Promise<BatchGeneratorResponse> {
    if (isDemoMode) {
      const accepted = Math.max(0, Math.min(spec.count, 50000))
      for (let index = 0; index < Math.min(accepted, 200); index += 1) {
        const strings = Object.fromEntries(
          Object.entries(spec.stringChoices ?? {}).flatMap(([name, choices]) =>
            choices.length > 0 ? [[name, [choices[(spec.seed + index) % choices.length]]]] : [],
          ),
        )
        const uint64s = Object.fromEntries(
          Object.entries(spec.uint64Choices ?? {}).flatMap(([name, choices]) =>
            choices.length > 0 ? [[name, [choices[(spec.seed + index) % choices.length]]]] : [],
          ),
        )
        const int64 = Object.fromEntries(
          Object.entries(spec.int64Ranges ?? {}).flatMap(([name, range]) => {
            const span = Math.max(0, range.max - range.min)
            return [[name, range.min + ((spec.seed + index) % (span + 1))]]
          }),
        )
        demoTickets.unshift({
          ticketId: `batch-${spec.seed}-${String(index + 1).padStart(4, '0')}`,
          createdAt: new Date().toISOString(),
          attributes: { strings, uint64s, int64 },
          facts: {},
          routeDecision: { status: 'routed' },
          status: 'waiting',
        })
      }
      return waitForDemo({
        accepted,
        rejected: spec.count - accepted,
        generatorId: `demo-${spec.seed}`,
        seed: spec.seed,
      })
    }
    const wireRequest = {
      count: spec.count,
      seed: spec.seed,
      rule: toApiRuleKey(spec.rule, spec.ruleKey),
      placementId: spec.placementId,
      startTicketId: spec.startTicketId,
      stringChoices: spec.stringChoices,
      uint64Choices: spec.uint64Choices,
      int64Ranges: spec.int64Ranges,
    }
    return request<WireBatchResponse>('/tickets/custom', json(wireRequest)).then((response) => ({
      accepted: response.accepted,
      rejected: response.rejected ?? 0,
      generatorId: response.generatorId ?? `generator-${spec.seed}`,
      seed: response.seed ?? spec.seed,
    }))
  },

  async deleteTicket(ticketId: string): Promise<void> {
    if (isDemoMode) {
      const index = demoTickets.findIndex((ticket) => ticket.ticketId === ticketId)
      if (index >= 0) demoTickets.splice(index, 1)
      return waitForDemo(undefined)
    }
    const numericId = Number(ticketId)
    if (!Number.isSafeInteger(numericId) || numericId <= 0)
      throw new ApiError('Ticket ID 必须是正整数')
    await request<unknown>(`/tickets/${encodeURIComponent(String(numericId))}`, {
      method: 'DELETE',
    })
  },

  async startRound(input: RunRoundInput): Promise<RunRoundResponse> {
    if (isDemoMode) {
      const seed = input.now ?? Math.floor(Math.random() * 1_000_000)
      const round = {
        roundId: `round-${String(seed).padStart(6, '0')}`,
        startedAt: new Date().toISOString(),
        completedAt: new Date().toISOString(),
        seed,
        matchCount: demoMatches.length,
        ticketCount: demoTickets.length,
        status: 'completed' as const,
      }
      return waitForDemo({ round, matches: clone(demoMatches) })
    }
    return request<WireRoundResponse>(
      '/rounds',
      json({ now: input.now, maxMatches: input.matchLimit }),
    ).then((response) => ({
      round: {
        roundId: response.roundId ?? `round-${Date.now()}`,
        startedAt: new Date().toISOString(),
        completedAt: new Date().toISOString(),
        seed: input.now ?? 0,
        matchCount: response.produced,
        ticketCount: 0,
        status: 'completed' as const,
      },
      matches: (response.matches ?? []).map(matchFromWire),
    }))
  },

  async getMatches(
    params: { cursor?: string; limit?: number } = {},
  ): Promise<{ items: MatchRecord[]; total?: number }> {
    if (isDemoMode) return waitForDemo({ items: clone(demoMatches), total: demoMatches.length })
    const query = new URLSearchParams()
    if (params.cursor) query.set('cursor', params.cursor)
    if (params.limit) query.set('limit', String(params.limit))
    const suffix = query.toString()
    return request<WireMatchPage>(`/matches${suffix ? `?${suffix}` : ''}`).then((page) => ({
      items: (page.items ?? []).map(matchFromWire),
      total: page.total,
    }))
  },

  async validateRule(rule: RuleDocument): Promise<ValidationResponse> {
    if (isDemoMode) {
      return waitForDemo({ valid: true, errors: [], warnings: [] })
    }
    return request<{
      valid: boolean
      issues?: Array<{ path?: string; code?: string; message: string; severity?: string }>
    }>(
      '/rules/validate',
      json({ rule: matchRuleFromDocument(rule) }),
    ).then((response) => ({
      valid: response.valid,
      errors: (response.issues ?? []).map((issue) => ({
        path: issue.path ?? '/',
        message: issue.message,
        keyword: issue.code,
        source: 'backend' as const,
        severity: issue.severity === 'warning' ? ('warning' as const) : ('error' as const),
      })),
    }))
  },
}

export interface SimulatorEvent {
  type: string
  at: string
  payload: Record<string, unknown>
}

export function subscribeEvents(
  onEvent: (event: SimulatorEvent) => void,
  onError?: () => void,
): () => void {
  if (isDemoMode || typeof window === 'undefined' || !('EventSource' in window))
    return () => undefined
  const source = new EventSource(`${apiBase}/events`)
  source.onmessage = (message) => {
    try {
      onEvent(JSON.parse(message.data) as SimulatorEvent)
    } catch {
      onError?.()
    }
  }
  source.onerror = () => onError?.()
  return () => source.close()
}
