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
import { enrichCapabilityDescriptions } from './capabilityDescriptions'
import type {
  ApiRuleKey,
  BatchGeneratorResponse,
  BatchGeneratorSpec,
  CapabilityNode,
  Capabilities,
  CandidateScoringConfig,
  FactProviderDescriptor,
  FactSpec,
  FactSnapshot,
  JsonObject,
  JsonValue,
  MatchRecord,
  MatchRuleDocument,
  MatchesPage,
  LogicalNodeFactsResponse,
  ProviderDescriptorSet,
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
type WireUint64 = number | string
type WireInt64 = number | string
type WireTypedValues = {
  stringLists?: Record<string, string[]>
  uint64Lists?: Record<string, WireUint64[]>
  int64Values?: Record<string, WireInt64>
  omittedNumericSamples?: number
}
type WireRuleKey = { namespace?: string; ruleId: number }
type WireTicket = WireTypedValues & { ticketId: WireUint64; createdAt: number }
type WireFacts = WireTypedValues
type WireOwner = { physicalNodeId: string; logicalNode?: WireObject }
type WireRouteDecision = {
  decisionId?: string
  owner?: WireOwner
  endpoint?: string
}
type WireTicketView = {
  ticket: WireTicket
  facts?: WireFacts
  owner?: WireOwner
  route?: WireRouteDecision
  state: string
}
type WireMatch = {
  matchId?: string
  id?: string
  round?: WireUint64
  physicalNodeId?: string
  logicalNode?: WireObject
  memberCount?: number | string
  tickets?: WireTicket[]
  members?: WireTicketView[]
  facts?: WireFacts
  createdAt?: number
  durationMs?: number | string
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
type WireFactSpec = {
  name: string
  type: 'strings' | 'int64' | 'uint64s'
  scope: 'tick' | 'object' | 'match'
  maxValues?: number
  description?: string
}
type WireProviderDescriptor = {
  id: string
  version: string
  facts?: WireFactSpec[]
}
type WireProviderDescriptorSet = {
  tick?: WireProviderDescriptor
  object?: WireProviderDescriptor
  match?: WireProviderDescriptor
}
type WireRuntimeFactValues = {
  tick?: WireTypedValues
}
type WireLogicalNodeFactsResponse = {
  logicalNode: WireObject
  facts?: WireFactSpec[]
  contractFacts?: WireFactSpec[]
  providerDescriptors?: WireProviderDescriptorSet
  runtimeFacts?: WireRuntimeFactValues
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

const MAX_SAFE_INTEGER_BIGINT = BigInt(Number.MAX_SAFE_INTEGER)
const MAX_UINT64 = (1n << 64n) - 1n

/**
 * Convert a JSON uint64 to a browser-safe number. Numeric JSON values above
 * MAX_SAFE_INTEGER are already rounded by JSON.parse and are therefore
 * intentionally excluded. A quoted decimal can still be accepted when it is
 * within the safe range.
 */
export function safeWireUint64Number(value: unknown): number | undefined {
  if (typeof value === 'number')
    return Number.isSafeInteger(value) && value >= 0 ? value : undefined
  if (typeof value !== 'string' || !/^\d+$/.test(value)) return undefined
  try {
    const parsed = BigInt(value)
    return parsed <= MAX_SAFE_INTEGER_BIGINT ? Number(parsed) : undefined
  } catch {
    return undefined
  }
}

/** Preserve a full-domain uint64 as a decimal string for identifier fields. */
export function safeWireUint64Text(value: unknown): string | undefined {
  if (typeof value === 'number') {
    return Number.isSafeInteger(value) && value >= 0 ? String(value) : undefined
  }
  if (typeof value !== 'string' || !/^\d+$/.test(value)) return undefined
  try {
    const parsed = BigInt(value)
    return parsed <= MAX_UINT64 ? String(parsed) : undefined
  } catch {
    return undefined
  }
}

/** Convert an int64 observation only when JavaScript can represent it exactly. */
export function safeWireInt64Number(value: unknown): number | undefined {
  if (typeof value === 'number') return Number.isSafeInteger(value) ? value : undefined
  if (typeof value !== 'string' || !/^-?\d+$/.test(value)) return undefined
  try {
    const parsed = BigInt(value)
    return parsed >= -MAX_SAFE_INTEGER_BIGINT && parsed <= MAX_SAFE_INTEGER_BIGINT
      ? Number(parsed)
      : undefined
  } catch {
    return undefined
  }
}

function safeWireUint64Lists(value: unknown): Record<string, number[]> {
  const result: Record<string, number[]> = {}
  for (const [name, values] of Object.entries(asObject(value))) {
    if (!Array.isArray(values)) continue
    const safe = values.flatMap((item) => {
      const parsed = safeWireUint64Number(item)
      return parsed === undefined ? [] : [parsed]
    })
    if (safe.length > 0) result[name] = safe
  }
  return result
}

function safeWireInt64Values(value: unknown): Record<string, number> {
  const result: Record<string, number> = {}
  for (const [name, item] of Object.entries(asObject(value))) {
    const safe = safeWireInt64Number(item)
    if (safe !== undefined) result[name] = safe
  }
  return result
}

function unsafeNumericSampleCount(value: unknown): number {
  const object = asObject(value)
  let omitted = Math.max(0, Math.trunc(asNumber(valueAt(object, 'omittedNumericSamples'))))
  const uint64Lists = valueAt(object, 'uint64Lists', 'uint64s')
  for (const values of Object.values(asObject(uint64Lists))) {
    if (Array.isArray(values))
      omitted += values.filter((item) => safeWireUint64Number(item) === undefined).length
  }
  const int64Values = valueAt(object, 'int64Values', 'int64s')
  for (const item of Object.values(asObject(int64Values))) {
    if (safeWireInt64Number(item) === undefined) omitted += 1
  }
  return omitted
}

function safeWireTicketId(value: unknown): string | undefined {
  const text = safeWireUint64Text(value)
  return text && text !== '0' ? text : undefined
}

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
  if (value === undefined || value === null || value === '') return new Date().toISOString()
  const numeric = asNumber(value, Number.NaN)
  if (!Number.isFinite(numeric)) return new Date().toISOString()
  const milliseconds = numeric > 1e15 ? numeric / 1e6 : numeric > 1e12 ? numeric : numeric * 1000
  return new Date(milliseconds).toISOString()
}

function typedAttributes(value: WireTypedValues | undefined) {
  return {
    strings: value?.stringLists ?? {},
    uint64s: safeWireUint64Lists(value?.uint64Lists),
    int64: safeWireInt64Values(value?.int64Values),
  }
}

function wireFacts(value: WireFacts | undefined): FactSnapshot {
  const facts: FactSnapshot = {}
  for (const [name, values] of Object.entries(value?.stringLists ?? {})) facts[name] = values
  for (const [name, values] of Object.entries(safeWireUint64Lists(value?.uint64Lists)))
    facts[name] = values
  for (const [name, item] of Object.entries(safeWireInt64Values(value?.int64Values)))
    facts[name] = item
  return facts
}

function factSnapshot(value: unknown): FactSnapshot {
  const object = asObject(value)
  const stringLists = valueAt(object, 'stringLists', 'strings')
  const uint64Lists = valueAt(object, 'uint64Lists', 'uint64s')
  const int64Values = valueAt(object, 'int64Values', 'int64s')
  return wireFacts({
    stringLists: asObject(stringLists) as Record<string, string[]>,
    uint64Lists: asObject(uint64Lists) as Record<string, WireUint64[]>,
    int64Values: asObject(int64Values) as Record<string, WireInt64>,
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

function safeTicketFromWire(value: WireTicketView): Ticket | undefined {
  const ticketId = safeWireTicketId(value.ticket.ticketId)
  if (!ticketId) return undefined
  const routeOwner = ownerFromWire(value.owner ?? value.route?.owner)
  const state = value.state === 'removed' ? 'expired' : value.state
  const routeDecision = routeOwner
    ? {
        status: 'routed' as const,
        owner: routeOwner,
        ...(value.route?.decisionId ? { decisionId: value.route.decisionId } : {}),
        ...(value.route?.endpoint ? { endpoint: value.route.endpoint } : {}),
      }
    : { status: 'pending' as const }
  return {
    ticketId,
    createdAt: timestampToIso(value.ticket.createdAt),
    attributes: typedAttributes(value.ticket),
    facts: wireFacts(value.facts),
    routeDecision,
    status:
      state === 'waiting' || state === 'matched' || state === 'expired' || state === 'rejected'
        ? state
        : 'waiting',
    matchId: undefined,
  }
}

function ticketFromWire(value: WireTicketView): Ticket {
  const ticket = safeTicketFromWire(value)
  if (!ticket)
    throw new ApiError(
      'API 返回了无法在 JavaScript 中安全表示的 Ticket ID；该 Ticket 已被排除',
      502,
    )
  return ticket
}

function matchFromWire(value: WireMatch): MatchRecord {
  const logical = asObject(value.logicalNode)
  const rule = fromApiRuleKey(valueAt(logical, 'rule'))
  const placementId = asString(valueAt(logical, 'placementId'), 'default')
  const members = (value.members ?? []).flatMap((member) => {
    const ticket = safeTicketFromWire(member)
    return ticket ? [ticket] : []
  })
  const ticketIds =
    members.length > 0
      ? members.map((ticket) => ticket.ticketId)
      : (value.tickets ?? []).flatMap((ticket) => {
          const ticketId = safeWireTicketId(ticket.ticketId)
          return ticketId ? [ticketId] : []
        })
  const roundText = value.round === undefined ? undefined : safeWireUint64Text(value.round)
  const roundNumber = roundText === undefined ? undefined : safeWireUint64Number(roundText)
  const facts = factSnapshot(value.facts)
  let excludedNumericSamples = unsafeNumericSampleCount(value.facts)
  if (value.round !== undefined && roundNumber === undefined) excludedNumericSamples += 1
  const durationCandidate =
    value.durationMs === undefined ? undefined : safeWireInt64Number(value.durationMs)
  const duration =
    durationCandidate === undefined || durationCandidate < 0 ? undefined : durationCandidate
  if (value.durationMs !== undefined && duration === undefined) excludedNumericSamples += 1
  const memberCountCandidate =
    value.memberCount === undefined ? undefined : safeWireInt64Number(value.memberCount)
  const memberCount =
    memberCountCandidate === undefined || memberCountCandidate < 0
      ? ticketIds.length
      : memberCountCandidate
  return {
    matchId: value.matchId ?? value.id ?? `match-${value.createdAt ?? Date.now()}`,
    createdAt: timestampToIso(value.createdAt),
    roundId: roundText ? `round-${roundText}` : '',
    ...(roundNumber === undefined ? {} : { round: roundNumber }),
    ...(value.physicalNodeId ? { physicalNodeId: value.physicalNodeId } : {}),
    ruleKey: ruleKeyText(rule),
    placementId,
    ticketIds,
    memberCount,
    ...(members.length > 0 ? { members } : {}),
    facts,
    ...(duration === undefined ? {} : { durationMs: duration }),
    ...(excludedNumericSamples > 0 ? { excludedNumericSamples } : {}),
  }
}

function demoMatchDetail(match: MatchRecord): MatchRecord {
  const result = clone(match)
  const members =
    result.members ??
    result.ticketIds.flatMap((ticketId) => {
      const ticket = demoTickets.find((item) => item.ticketId === ticketId)
      return ticket ? [clone(ticket)] : []
    })
  result.members = members
  if (members.length > 0) {
    result.ticketIds = members.map((ticket) => ticket.ticketId)
    result.memberCount = members.length
  }
  if (!result.physicalNodeId)
    result.physicalNodeId = members[0]?.routeDecision?.owner?.physicalNodeId
  if (result.round === undefined) {
    const round = Number(result.roundId.match(/(\d+)$/)?.[1] ?? 0)
    if (round > 0) result.round = round
  }
  return result
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

function factSpecToJson(spec: FactSpec): JsonObject {
  const result: JsonObject = {
    name: spec.name,
    type: spec.type,
    scope: spec.scope,
  }
  if (spec.maxValues !== undefined) result.maxValues = spec.maxValues
  if (spec.description !== undefined) result.description = spec.description
  return result
}

function descriptorToJson(descriptor: FactProviderDescriptor): JsonObject {
  return {
    id: descriptor.id,
    version: descriptor.version,
    facts: (descriptor.facts ?? []).map(factSpecToJson),
  }
}

function providerDescriptorsToScenarioFields(
  descriptors: ProviderDescriptorSet | undefined,
): JsonObject {
  if (!descriptors) return {}
  const result: JsonObject = {}
  if (descriptors.tick) result.factProviderDescriptor = descriptorToJson(descriptors.tick)
  if (descriptors.object) result.objectFactProviderDescriptor = descriptorToJson(descriptors.object)
  if (descriptors.match) result.matchFactProviderDescriptor = descriptorToJson(descriptors.match)
  return result
}

/** Convert the editor's convenient flat Fact map to the simulator's typed
 * FactSnapshot wire shape. The Contract type is authoritative when a value
 * is edited; unknown names are retained so backend validation can explain the
 * mistake instead of silently dropping it. */
function runtimeTickFactsToScenario(
  values: FactSnapshot | undefined,
  contract: RuleDocument['contract'],
): JsonObject {
  const result: JsonObject = { strings: {}, uint64s: {}, int64s: {} }
  const byName = new Map(
    contract.facts.filter((fact) => fact.scope === 'tick').map((fact) => [fact.name, fact]),
  )
  for (const [name, value] of Object.entries(values ?? {})) {
    const declared = byName.get(name)
    if (
      declared?.type === 'strings' ||
      (!declared && Array.isArray(value) && value.every((item) => typeof item === 'string'))
    ) {
      ;(result.strings as JsonObject)[name] = value as unknown as JsonValue
    } else if (declared?.type === 'uint64s') {
      ;(result.uint64s as JsonObject)[name] = value as unknown as JsonValue
    } else {
      ;(result.int64s as JsonObject)[name] = value as unknown as JsonValue
    }
  }
  return result
}

const defaultScoring: CandidateScoringConfig = {
  type: 'created_at',
  params: { direction: 'descending' },
}
const defaultSeedSelection: SeedSelectionConfig = { type: 'arrival', params: {} }
const defaultRuntime: RuleRuntimeConfig = {
  candidateScoringLimitPerSeed: 500,
  candidateLimitPerSeed: 50,
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
    const runtime = valueAt(aggregate, 'runtime') as Partial<RuleRuntimeConfig> | undefined
    const providerDescriptors = providerDescriptorsFromWire({
      tick: valueAt(item, 'factProviderDescriptor') as WireProviderDescriptor | undefined,
      object: valueAt(item, 'objectFactProviderDescriptor') as WireProviderDescriptor | undefined,
      match: valueAt(item, 'matchFactProviderDescriptor') as WireProviderDescriptor | undefined,
    })
    return {
      ruleKey,
      apiRule,
      placementId,
      displayName: `${ruleKey} · ${placementId}`,
      enabled: valueAt(item, 'enabled') !== false,
      contract: asObject(valueAt(aggregate, 'contract')) as unknown as RuleSummary['contract'],
      prefilter: asObject(valueAt(aggregate, 'prefilter')) as unknown as RuleSummary['prefilter'],
      evaluation: asObject(
        valueAt(aggregate, 'evaluation'),
      ) as unknown as RuleSummary['evaluation'],
      scoring: scoring ?? defaultScoring,
      seedSelection: seedSelection ?? defaultSeedSelection,
      runtime: { ...defaultRuntime, ...(runtime ?? {}) },
      tickFacts: factSnapshot(valueAt(item, 'tickFacts')),
      providerDescriptors,
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
    runtime: { ...defaultRuntime, ...(summary.runtime ?? {}) },
    tickFacts: summary.tickFacts,
    providerDescriptors: summary.providerDescriptors,
    graph: buildRuleGraph(summary),
  }
}

export function capabilitiesFromWire(response: WireCapabilitiesResponse): Capabilities {
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
    if (node.type === 'compare.int64')
      return has(['int64_eq', 'int64_neq', 'int64_lt', 'int64_lte', 'int64_gt', 'int64_gte'])
    if (node.type === 'compare.strings')
      return has([
        'strings_eq',
        'strings_neq',
        'strings_is_empty',
        'strings_contains',
        'strings_contains_any',
        'strings_contains_all',
        'strings_intersects',
      ])
    if (node.type === 'compare.uint64')
      return has([
        'uint64s_eq',
        'uint64s_neq',
        'uint64s_is_empty',
        'uint64s_contains',
        'uint64s_contains_any',
        'uint64s_contains_all',
        'uint64s_intersects',
      ])
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
    const variadic = isVariadicCapabilityOp(operator.name, operator.inputs)
    return {
      type: capabilityNodeTypeForOp(operator.name, bitmap),
      op: operator.name,
      label: operator.name,
      description: `服务端 capability：${operator.name}`,
      category: (bitmap ? 'prefilter' : 'expression') as CapabilityNode['category'],
      outputType: bitmap
        ? ('bitmap' as const)
        : outputTypeForCapabilityOp(operator.name, operator.resultType),
      inputTypes,
      maxInputs: variadic ? 16 : inputTypes.length,
      variadic,
      variadicInputType: variadic ? inputTypes.at(-1) : undefined,
      fields: operator.fields,
    }
  })
  const legacyOperators: Array<{ op: string; bitmap: boolean }> = [
    ...(scalarCatalog.length === 0 ? expressionOps.map((op) => ({ op, bitmap: false })) : []),
    ...(bitmapCatalog.length === 0 ? bitmapOps.map((op) => ({ op, bitmap: true })) : []),
  ]
  const legacyGenericNodeTypes = legacyOperators.map(({ op, bitmap }) => ({
    type: capabilityNodeTypeForOp(op, bitmap),
    op,
    label: op,
    description: `服务端 capability：${op}`,
    category: (bitmap ? 'prefilter' : 'expression') as CapabilityNode['category'],
    outputType: bitmap ? ('bitmap' as const) : outputTypeForCapabilityOp(op),
    inputTypes: inputTypesForCapabilityOp(op),
    maxInputs: isVariadicCapabilityOp(op) ? 16 : inputTypesForCapabilityOp(op).length,
    variadic: isVariadicCapabilityOp(op),
    variadicInputType: isVariadicCapabilityOp(op)
      ? inputTypesForCapabilityOp(op).at(-1)
      : undefined,
  }))
  const nodeTypes = enrichCapabilityDescriptions({
    schemaVersions: [],
    candidateScorers: [],
    seedSelections: [],
    sources: [],
    nodeTypes: [...baseNodeTypes, ...genericNodeTypes, ...legacyGenericNodeTypes],
    limits: {},
  }).nodeTypes
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
  const schemaInputs: Record<string, ValueType[]> = {
    bool_literal: [],
    bool_and: ['bool'],
    bool_or: ['bool'],
    bool_not: ['bool'],
    int64_literal: [],
    int64_ref: [],
    int64_step: ['int64'],
    int64_clamp: ['int64', 'int64', 'int64'],
    int64_add: ['int64', 'int64'],
    int64_sub: ['int64', 'int64'],
    int64_min: ['int64', 'int64'],
    int64_max: ['int64', 'int64'],
    int64_eq: ['int64', 'int64'],
    int64_neq: ['int64', 'int64'],
    int64_lt: ['int64', 'int64'],
    int64_lte: ['int64', 'int64'],
    int64_gt: ['int64', 'int64'],
    int64_gte: ['int64', 'int64'],
    strings_literal: [],
    strings_ref: [],
    strings_union: ['strings'],
    strings_eq: ['strings', 'strings'],
    strings_neq: ['strings', 'strings'],
    strings_is_empty: ['strings'],
    strings_contains: ['strings'],
    strings_contains_any: ['strings', 'strings'],
    strings_contains_all: ['strings', 'strings'],
    strings_intersects: ['strings', 'strings'],
    uint64s_literal: [],
    uint64s_ref: [],
    uint64s_union: ['uint64s'],
    uint64s_eq: ['uint64s', 'uint64s'],
    uint64s_neq: ['uint64s', 'uint64s'],
    uint64s_is_empty: ['uint64s'],
    uint64s_contains: ['uint64s'],
    uint64s_contains_any: ['uint64s', 'uint64s'],
    uint64s_contains_all: ['uint64s', 'uint64s'],
    uint64s_intersects: ['uint64s', 'uint64s'],
    none: [],
    and: ['bitmap'],
    or: ['bitmap'],
    exclude: ['bitmap'],
    if: ['bool', 'bitmap', 'bitmap'],
    lookup_string: ['strings'],
    lookup_uint64: ['uint64s'],
    lookup_range: ['int64', 'int64'],
  }
  if (schemaInputs[op]) return schemaInputs[op]
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

function isVariadicCapabilityOp(op: string, declaredInputs?: string[]): boolean {
  return (
    op === 'bool_and' ||
    op === 'bool_or' ||
    op === 'and' ||
    op === 'or' ||
    op === 'strings_union' ||
    op === 'uint64s_union' ||
    Boolean(declaredInputs?.some((input) => input.endsWith('[]')))
  )
}

function capabilityNodeTypeForOp(op: string, bitmap: boolean): CapabilityNode['type'] {
  if (bitmap) {
    if (op === 'and' || op === 'or') return 'prefilter.combine'
    if (op === 'exclude') return 'prefilter.exclude'
    if (op === 'lookup_string' || op === 'lookup_uint64' || op === 'lookup_range')
      return 'prefilter.lookup'
    return 'prefilter.generic'
  }
  if (op === 'bool_and') return 'logic.and'
  if (op === 'bool_or') return 'logic.or'
  if (op === 'bool_not') return 'logic.not'
  if (
    op.startsWith('int64_') &&
    ['int64_eq', 'int64_neq', 'int64_lt', 'int64_lte', 'int64_gt', 'int64_gte'].includes(op)
  )
    return 'compare.int64'
  if (
    op.startsWith('strings_') &&
    !op.endsWith('_literal') &&
    !op.endsWith('_ref') &&
    !op.includes('union')
  )
    return 'compare.strings'
  if (
    op.startsWith('uint64s_') &&
    !op.endsWith('_literal') &&
    !op.endsWith('_ref') &&
    !op.includes('union')
  )
    return 'compare.uint64'
  if (op === 'bool_literal') return 'literal.bool'
  if (op === 'int64_literal') return 'literal.int64'
  if (op === 'strings_literal') return 'literal.string'
  if (op === 'uint64s_literal') return 'literal.uint64'
  return 'expression.generic'
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

function factSpecsFromWire(facts: WireFactSpec[] | undefined): FactSpec[] {
  return (facts ?? []).map((fact) => ({
    name: fact.name,
    type: fact.type,
    scope: fact.scope,
    ...(fact.maxValues === undefined ? {} : { maxValues: fact.maxValues }),
    ...(fact.description === undefined ? {} : { description: fact.description }),
  }))
}

function providerDescriptorFromWire(
  descriptor: WireProviderDescriptor | undefined,
): FactProviderDescriptor | undefined {
  if (!descriptor) return undefined
  return {
    id: descriptor.id ?? '',
    version: descriptor.version ?? '',
    facts: factSpecsFromWire(descriptor.facts),
  }
}

function providerDescriptorsFromWire(
  descriptors: WireProviderDescriptorSet | undefined,
): ProviderDescriptorSet {
  const tick = providerDescriptorFromWire(descriptors?.tick)
  const object = providerDescriptorFromWire(descriptors?.object)
  const match = providerDescriptorFromWire(descriptors?.match)
  return {
    ...(tick ? { tick } : {}),
    ...(object ? { object } : {}),
    ...(match ? { match } : {}),
  }
}

function logicalNodeFactsFromWire(
  response: WireLogicalNodeFactsResponse,
): LogicalNodeFactsResponse {
  const contractFacts = factSpecsFromWire(response.contractFacts ?? response.facts)
  return {
    logicalNode: response.logicalNode,
    facts: factSpecsFromWire(response.facts ?? response.contractFacts),
    contractFacts,
    providerDescriptors: providerDescriptorsFromWire(response.providerDescriptors),
    runtimeFacts: {
      tick: factSnapshot(response.runtimeFacts?.tick),
    },
  }
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

function demoMatchPage(params: { cursor?: string; limit?: number }): MatchesPage {
  const cursorText = params.cursor
  let start = 0
  if (cursorText) {
    const parsed = Number(cursorText)
    if (!/^[+-]?\d+$/.test(cursorText) || !Number.isSafeInteger(parsed) || parsed < 0)
      throw new ApiError('cursor must be a non-negative integer', 400)
    start = parsed
  }

  const requestedLimit = params.limit || 100
  if (!Number.isInteger(requestedLimit) || requestedLimit < 1 || requestedLimit > 1000)
    throw new ApiError('limit must be between 1 and 1000', 400)

  // The runtime retains records oldest-first for cheap eviction, while its
  // public Match endpoint exposes the newest records first.
  const ordered = demoMatches.slice().reverse()
  const total = ordered.length
  const end = Math.min(start + requestedLimit, total)
  const page: MatchesPage = {
    items: ordered.slice(start, end).map(demoMatchDetail),
    total,
  }
  if (end < total) page.nextCursor = String(start + requestedLimit)
  return page
}

type MatchPageReader = (params: { cursor?: string; limit?: number }) => Promise<MatchesPage>

const matchPageSignature = (page: MatchesPage): string =>
  page.items.map((match) => match.matchId).join('\u001f')

/**
 * Read a cursor-paginated Match history while tolerating the simulator's
 * offset cursors moving when new records arrive. Duplicate IDs, changing
 * totals, cursor loops, and a changed first page are treated as an unstable
 * snapshot and retried from the beginning. The final attempt is returned as
 * a best-effort de-duplicated result so a busy simulator does not blank the
 * analytics screen indefinitely.
 */
export async function collectAllMatches(
  readPage: MatchPageReader,
  options: { pageLimit?: number; maxAttempts?: number } = {},
): Promise<MatchRecord[]> {
  const pageLimit = Math.max(1, Math.min(1000, Math.trunc(options.pageLimit ?? 1000)))
  const maxAttempts = Math.max(1, Math.min(5, Math.trunc(options.maxAttempts ?? 3)))

  for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
    const byId = new Map<string, MatchRecord>()
    const seenCursors = new Set<string>()
    let cursor: string | undefined
    let firstPage: MatchesPage | undefined
    let pages = 0
    let unstable = false

    while (pages < 10000) {
      const page = await readPage(cursor ? { cursor, limit: pageLimit } : { limit: pageLimit })
      pages += 1
      if (!firstPage) firstPage = page
      else if (firstPage.total !== undefined && page.total !== firstPage.total) unstable = true

      for (const match of page.items) {
        if (!match.matchId || byId.has(match.matchId)) {
          unstable = true
          continue
        }
        byId.set(match.matchId, match)
      }

      if (!page.nextCursor) break
      if (seenCursors.has(page.nextCursor) || page.nextCursor === cursor) {
        unstable = true
        break
      }
      seenCursors.add(page.nextCursor)
      cursor = page.nextCursor
    }
    if (pages >= 10000) unstable = true

    if (firstPage?.total !== undefined && byId.size < firstPage.total) unstable = true

    // An offset list can shift without producing a duplicate (for example
    // when an insertion and an eviction happen together). Re-read the first
    // page after a multi-page traversal to detect that case.
    if (firstPage && pages > 1) {
      const verification = await readPage({ limit: pageLimit })
      if (
        verification.total !== firstPage.total ||
        matchPageSignature(verification) !== matchPageSignature(firstPage)
      ) {
        unstable = true
      }
    }

    if (!unstable || attempt === maxAttempts - 1) return Array.from(byId.values())
  }
  return []
}

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
    if (rule.tickFacts !== undefined)
      targetObject.tickFacts = runtimeTickFactsToScenario(rule.tickFacts, rule.contract)
    if (rule.providerDescriptors !== undefined) {
      for (const field of [
        'factProviderDescriptor',
        'objectFactProviderDescriptor',
        'matchFactProviderDescriptor',
      ])
        delete targetObject[field]
      Object.assign(targetObject, providerDescriptorsToScenarioFields(rule.providerDescriptors))
    }
  } else {
    const nextRule: JsonObject = {
      logicalNode: {
        rule: toApiRuleKey(rule.apiRule, rule.ruleKey) as unknown as JsonValue,
        placementId: rule.placementId,
      } as unknown as JsonValue,
      enabled: true,
      rule: aggregate as unknown as JsonValue,
      tickFacts: runtimeTickFactsToScenario(rule.tickFacts, rule.contract),
    }
    Object.assign(nextRule, providerDescriptorsToScenarioFields(rule.providerDescriptors))
    rules.push(nextRule as unknown as JsonValue)
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
      ? waitForDemo(enrichCapabilityDescriptions(clone(demoCapabilities)))
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
          tickFacts: rule.tickFacts,
          providerDescriptors: rule.providerDescriptors,
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
          tickFacts: rule.tickFacts,
          providerDescriptors: rule.providerDescriptors,
        }
      if (rule.ruleKey === demoRule.ruleKey && rule.placementId === demoRule.placementId) {
        demoRule.contract = rule.contract
        demoRule.prefilter = rule.prefilter
        demoRule.evaluation = rule.evaluation
        demoRule.scoring = rule.scoring
        demoRule.seedSelection = rule.seedSelection
        demoRule.runtime = rule.runtime
        demoRule.tickFacts = rule.tickFacts
        demoRule.providerDescriptors = rule.providerDescriptors
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

  async getLogicalNodeFacts(
    rule: ApiRuleKey,
    placementId: string,
  ): Promise<LogicalNodeFactsResponse> {
    if (isDemoMode) {
      const ruleKey = ruleKeyText(rule)
      const summary =
        demoScenario.rules.find(
          (item) =>
            item.apiRule?.ruleId === rule.ruleId &&
            (item.apiRule.namespace ?? '') === (rule.namespace ?? '') &&
            item.placementId === placementId,
        ) ??
        demoScenario.rules.find(
          (item) => item.ruleKey === ruleKey && item.placementId === placementId,
        )
      if (!summary) throw new ApiError('指定的 LogicalNode 不存在')
      return waitForDemo(
        clone({
          logicalNode: {
            rule: summary.apiRule ?? toApiRuleKey(undefined, summary.ruleKey),
            placementId,
          },
          facts: summary.contract.facts,
          contractFacts: summary.contract.facts,
          providerDescriptors: summary.providerDescriptors ?? {},
          runtimeFacts: { tick: summary.tickFacts ?? {} },
        }),
      )
    }
    const query = new URLSearchParams({
      ruleId: String(rule.ruleId),
      placementId,
    })
    if (rule.namespace) query.set('ruleNamespace', rule.namespace)
    return request<WireLogicalNodeFactsResponse>(`/logical-nodes/facts?${query.toString()}`).then(
      logicalNodeFactsFromWire,
    )
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
      return waitForDemo({ round, matches: demoMatches.map(demoMatchDetail) })
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

  async getMatches(params: { cursor?: string; limit?: number } = {}): Promise<MatchesPage> {
    if (isDemoMode) return waitForDemo(demoMatchPage(params))
    const query = new URLSearchParams()
    if (params.cursor) query.set('cursor', params.cursor)
    if (params.limit) query.set('limit', String(params.limit))
    const suffix = query.toString()
    return request<WireMatchPage>(`/matches${suffix ? `?${suffix}` : ''}`).then((page) => ({
      items: (page.items ?? []).map(matchFromWire),
      nextCursor: page.nextCursor,
      total: page.total,
    }))
  },

  /** Read the complete retained history, following the stable cursor order. */
  async getAllMatches(): Promise<MatchRecord[]> {
    return collectAllMatches((params) => api.getMatches(params))
  },

  async getMatch(matchId: string): Promise<MatchRecord> {
    const normalized = matchId.trim()
    if (!normalized) throw new ApiError('Match ID 不能为空', 400)
    if (isDemoMode) {
      const match = demoMatches.find((item) => item.matchId === normalized)
      if (!match) throw new ApiError('Match 不存在或已被淘汰', 404)
      return waitForDemo(demoMatchDetail(match))
    }
    return request<WireMatch>(`/matches/${encodeURIComponent(normalized)}`).then(matchFromWire)
  },

  async validateRule(rule: RuleDocument): Promise<ValidationResponse> {
    if (isDemoMode) {
      return waitForDemo({ valid: true, errors: [], warnings: [] })
    }
    return request<{
      valid: boolean
      issues?: Array<{ path?: string; code?: string; message: string; severity?: string }>
    }>('/rules/validate', json({ rule: matchRuleFromDocument(rule) })).then((response) => ({
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
