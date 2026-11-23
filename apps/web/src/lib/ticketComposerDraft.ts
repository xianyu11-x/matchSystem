import type { TrafficConfig } from './api'
import type { TicketTargetMode } from './ticketTargets'
import type { AttributeGenerator, BatchGeneratorSpec } from '../types'

export const ticketComposerDraftKey = 'matchsystem.ticket-composer-draft.v1'

type DraftStorage = Pick<Storage, 'getItem' | 'setItem'>

export interface TicketComposerDraft {
  contractKey?: string
  targetMode: TicketTargetMode
  targetSelections: Partial<Record<TicketTargetMode, string>>
  mode: 'single' | 'batch'
  ticketId: string
  createdAt: string
  affinityKey: string
  attributeDraft: Record<string, string>
  factDraft: Record<string, string>
  batchFacts: Record<string, string>
  batchOptions: {
    createdAtStart: string
    createdAtStep: string
    affinityPrefix: string
    requestIdPrefix: string
    atomic: boolean
  }
  generators: Record<string, AttributeGenerator>
  batch: Pick<BatchGeneratorSpec, 'count' | 'seed' | 'startTicketId'>
  traffic: TrafficConfig
}

function defaultDraft(now: number): TicketComposerDraft {
  return {
    targetMode: 'rule',
    targetSelections: {},
    mode: 'batch',
    ticketId: '',
    createdAt: '',
    affinityKey: '',
    attributeDraft: {},
    factDraft: {},
    batchFacts: {},
    batchOptions: {
      createdAtStart: '',
      createdAtStep: '1',
      affinityPrefix: '',
      requestIdPrefix: '',
      atomic: true,
    },
    generators: {},
    batch: { count: 1000, seed: 20260829, startTicketId: now * 1000 },
    traffic: {
      mode: 'realtime',
      distribution: 'constant',
      rate: 10,
      burstSize: 50,
      burstIntervalMs: 1000,
      matchIntervalMs: 1000,
      maxMatches: 10,
      seed: 42,
      steps: 100,
      stepMs: 1000,
      startTimeMs: now,
    },
  }
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined
}

function stringValue(value: unknown, fallback: string): string {
  return typeof value === 'string' ? value : fallback
}

function numberValue(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function stringRecord(value: unknown): Record<string, string> {
  return Object.fromEntries(
    Object.entries(record(value) ?? {}).filter(
      (entry): entry is [string, string] => typeof entry[1] === 'string',
    ),
  )
}

function generatorRecord(value: unknown): Record<string, AttributeGenerator> {
  return Object.fromEntries(
    Object.entries(record(value) ?? {}).flatMap(([name, value]) => {
      const source = record(value)
      if (!source || !['strings', 'uint64s', 'int64'].includes(String(source.type))) return []
      const generator: AttributeGenerator = { type: source.type as AttributeGenerator['type'] }
      if (source.source === 'sample' || source.source === 'ticketId' || source.source === 'shared')
        generator.source = source.source
      if (typeof source.ref === 'string') generator.ref = source.ref
      if (Array.isArray(source.values) && source.values.every((item) => typeof item === 'string'))
        generator.values = source.values
      if (typeof source.set === 'string') generator.set = source.set
      if (typeof source.min === 'string') generator.min = source.min
      if (typeof source.max === 'string') generator.max = source.max
      if (typeof source.count === 'number' && Number.isFinite(source.count))
        generator.count = source.count
      if (typeof source.replacement === 'boolean') generator.replacement = source.replacement
      if (
        source.distribution === 'uniform' ||
        source.distribution === 'low' ||
        source.distribution === 'high' ||
        source.distribution === 'triangular' ||
        source.distribution === 'normal'
      )
        generator.distribution = source.distribution
      // 正态分布的空数值在表单中表示为 NaN，JSON 保存后为 null。
      if (source.mean === null) generator.mean = NaN
      else if (typeof source.mean === 'number' && Number.isFinite(source.mean))
        generator.mean = source.mean
      if (source.stdDev === null) generator.stdDev = NaN
      else if (typeof source.stdDev === 'number' && Number.isFinite(source.stdDev))
        generator.stdDev = source.stdDev
      return [[name, generator]]
    }),
  )
}

export function loadTicketComposerDraft(
  storage?: DraftStorage,
  now = Date.now(),
): TicketComposerDraft {
  const defaults = defaultDraft(now)
  try {
    const source = storage ?? (typeof window === 'undefined' ? undefined : window.localStorage)
    const stored = source?.getItem(ticketComposerDraftKey)
    if (!stored) return defaults
    const envelope = record(JSON.parse(stored))
    const draft = record(envelope?.draft)
    if (envelope?.version !== 1 || !draft) return defaults

    const selections = record(draft.targetSelections)
    const options = record(draft.batchOptions)
    const batch = record(draft.batch)
    const traffic = record(draft.traffic)
    return {
      contractKey: typeof draft.contractKey === 'string' ? draft.contractKey : undefined,
      targetMode: draft.targetMode === 'node' ? 'node' : 'rule',
      targetSelections: {
        ...(typeof selections?.rule === 'string' ? { rule: selections.rule } : {}),
        ...(typeof selections?.node === 'string' ? { node: selections.node } : {}),
      },
      mode: draft.mode === 'single' ? 'single' : 'batch',
      ticketId: stringValue(draft.ticketId, defaults.ticketId),
      createdAt: stringValue(draft.createdAt, defaults.createdAt),
      affinityKey: stringValue(draft.affinityKey, defaults.affinityKey),
      attributeDraft: stringRecord(draft.attributeDraft),
      factDraft: stringRecord(draft.factDraft),
      batchFacts: stringRecord(draft.batchFacts),
      batchOptions: {
        createdAtStart: stringValue(options?.createdAtStart, defaults.batchOptions.createdAtStart),
        createdAtStep: stringValue(options?.createdAtStep, defaults.batchOptions.createdAtStep),
        affinityPrefix: stringValue(options?.affinityPrefix, defaults.batchOptions.affinityPrefix),
        requestIdPrefix: stringValue(
          options?.requestIdPrefix,
          defaults.batchOptions.requestIdPrefix,
        ),
        atomic:
          typeof options?.atomic === 'boolean' ? options.atomic : defaults.batchOptions.atomic,
      },
      generators: generatorRecord(draft.generators),
      batch: {
        count: numberValue(batch?.count, defaults.batch.count),
        seed: numberValue(batch?.seed, defaults.batch.seed),
        startTicketId: batch
          ? typeof batch.startTicketId === 'number' && Number.isFinite(batch.startTicketId)
            ? batch.startTicketId
            : undefined
          : defaults.batch.startTicketId,
      },
      traffic: {
        mode: traffic?.mode === 'fixed' ? 'fixed' : 'realtime',
        distribution:
          traffic?.distribution === 'poisson' || traffic?.distribution === 'burst'
            ? traffic.distribution
            : 'constant',
        rate: numberValue(traffic?.rate, defaults.traffic.rate),
        burstSize: numberValue(traffic?.burstSize, defaults.traffic.burstSize),
        burstIntervalMs: numberValue(traffic?.burstIntervalMs, defaults.traffic.burstIntervalMs),
        matchIntervalMs: numberValue(traffic?.matchIntervalMs, defaults.traffic.matchIntervalMs),
        maxMatches: numberValue(traffic?.maxMatches, defaults.traffic.maxMatches),
        seed: numberValue(traffic?.seed, defaults.traffic.seed),
        steps: numberValue(traffic?.steps, defaults.traffic.steps!),
        stepMs: numberValue(traffic?.stepMs, defaults.traffic.stepMs!),
        startTimeMs: numberValue(traffic?.startTimeMs, defaults.traffic.startTimeMs!),
      },
    }
  } catch {
    return defaults
  }
}

export function saveTicketComposerDraft(draft: TicketComposerDraft, storage?: DraftStorage): void {
  try {
    const target = storage ?? (typeof window === 'undefined' ? undefined : window.localStorage)
    target?.setItem(ticketComposerDraftKey, JSON.stringify({ version: 1, draft }))
  } catch {
    // 存储被禁用或配额不足时，表单仍可在当前页面正常使用。
  }
}
