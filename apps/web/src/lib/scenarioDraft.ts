import { scenarioPayload } from './api'
import { buildRuleGraph } from './graphBuilder'
import type {
  FactSnapshot,
  JsonObject,
  JsonValue,
  MatchRuleDocument,
  RuleDocument,
  Scenario,
} from '../types'

export const draftObject = (value: JsonValue | undefined): JsonObject =>
  value && typeof value === 'object' && !Array.isArray(value) ? value : {}
export const draftRules = (draft: JsonObject): JsonObject[] =>
  Array.isArray(draft.rules) ? draft.rules.map(draftObject) : []

export function documentFromDraft(raw: JsonObject): RuleDocument {
  const logical = draftObject(raw.logicalNode)
  const aggregate = draftObject(raw.rule) as unknown as MatchRuleDocument
  const apiRule = { ...aggregate.ruleKey }
  const tick = draftObject(raw.tickFacts)
  const document: RuleDocument = {
    ...structuredClone(aggregate),
    ruleKey: `${apiRule.namespace ? `${apiRule.namespace}/` : ''}${apiRule.ruleId}`,
    apiRule,
    placementId: String(logical.placementId ?? 'default'),
    runtime: {
      ...aggregate.runtime,
      candidateScoringLimitPerSeed: aggregate.runtime.candidateScoringLimitPerSeed ?? 500,
      candidateLimitPerSeed: aggregate.runtime.candidateLimitPerSeed ?? 50,
    },
    tickFacts: structuredClone({
      ...draftObject(tick.strings),
      ...draftObject(tick.uint64s),
      ...draftObject(tick.int64s),
    }) as FactSnapshot,
    providerDescriptors: {},
    graph: { nodes: [], edges: [] },
  }
  for (const [scope, key] of [
    ['tick', 'factProviderDescriptor'],
    ['object', 'objectFactProviderDescriptor'],
    ['match', 'matchFactProviderDescriptor'],
  ] as const) {
    if (raw[key] !== undefined)
      document.providerDescriptors![scope] = structuredClone(raw[key]) as unknown as NonNullable<
        RuleDocument['providerDescriptors']
      >[typeof scope]
  }
  document.graph = buildRuleGraph(document)
  return document
}

/** Flush the selected editor before changing row order, identity or selection. */
export function mergeRuleDraft(
  draft: JsonObject,
  index: number,
  document?: RuleDocument,
): JsonObject {
  const next = structuredClone(draft)
  const rows = draftRules(next)
  if (document && rows[index]) {
    // Feed the transport adapter a temporary identity matching the editor;
    // retain the user's new deployment identity from the actual draft row.
    const source = {
      ...rows[index],
      logicalNode: {
        rule: document.apiRule as unknown as JsonValue,
        placementId: document.placementId,
      },
    }
    const encoded = draftRules(
      scenarioPayload({ rawScenario: { rules: [source] } } as unknown as Scenario, document),
    )[0]
    for (const key of [
      'rule',
      'tickFacts',
      'factProviderDescriptor',
      'objectFactProviderDescriptor',
      'matchFactProviderDescriptor',
    ]) {
      delete rows[index][key]
      if (encoded[key] !== undefined) rows[index][key] = encoded[key]
    }
  }
  for (const row of rows)
    row.rule = { ...draftObject(row.rule), ruleKey: draftObject(row.logicalNode).rule }
  next.rules = rows
  return next
}

function availableRuleId(draft: JsonObject): number {
  const used = new Set(
    draftRules(draft).map((row) => Number(draftObject(draftObject(row.logicalNode).rule).ruleId)),
  )
  let id = 1
  while (used.has(id)) id++
  return id
}

export function appendRuleDraft(draft: JsonObject, copyIndex?: number): JsonObject {
  const next = structuredClone(draft)
  const rows = draftRules(next)
  const physical = Array.isArray(next.physicalNodes) ? next.physicalNodes.map(draftObject) : []
  if (physical.length === 0)
    physical.push({
      id: 'simulator-1',
      endpoint: 'inproc://simulator-1',
      enabled: true,
      selector: 'round_robin',
    })
  next.physicalNodes = physical
  next.schemaVersion = 'simulator-scenario/v1'
  const id = availableRuleId(next)
  const source = copyIndex === undefined ? undefined : rows[copyIndex]
  const key = {
    namespace: source ? (draftObject(draftObject(source.logicalNode).rule).namespace ?? '') : '',
    ruleId: id,
  }
  const raw: JsonObject = source
    ? structuredClone(source)
    : {
        physicalNodeId: physical[0].id,
        enabled: true,
        weight: 1,
        rule: {
          schemaVersion: 'match-rule/v1',
          ruleKey: key,
          contract: {
            schemaVersion: 'logical-node-contract/v3',
            attributes: [],
            facts: [],
            indexes: [],
          },
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
        },
      }
  raw.logicalNode = { rule: key, placementId: source ? `copy-${id}` : 'default' }
  raw.rule = { ...draftObject(raw.rule), ruleKey: key }
  next.rules = [...rows, raw]
  return next
}

export function removeRuleDraft(
  draft: JsonObject,
  removed: number,
  selected: number,
): { draft: JsonObject; selected: number } {
  const rows = draftRules(draft).filter((_, index) => index !== removed)
  return {
    draft: { ...draft, rules: rows },
    selected:
      rows.length === 0
        ? -1
        : selected > removed
          ? selected - 1
          : Math.min(selected, rows.length - 1),
  }
}
