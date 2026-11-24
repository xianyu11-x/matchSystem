import type { ApiRuleKey } from '../types'

export type ReplayInteger = number | string

export interface ReplayInstance {
  instanceId: string
  physicalNodeId: string
  logicalNode: { rule: ApiRuleKey; placementId: string }
  eventCount: ReplayInteger
  firstSequence: ReplayInteger
  lastSequence: ReplayInteger
  replayable: boolean
  issue?: string
}

export interface ReplayExtractRequest {
  logs?: string
  instanceId?: string
}

export interface ReplayExtractResult {
  instances: ReplayInstance[]
  timelineJson?: string
}

export interface ReplayRequest {
  timelineJson: string
  targetRuleId: string
  providerLogicalNode?: { rule: ApiRuleKey; placementId: string }
}

export interface ReplayEvent {
  sequence: ReplayInteger
  type: string
  offsetNs?: ReplayInteger
  round?: ReplayInteger
  now?: ReplayInteger
  status?: string
  ticketIds?: ReplayInteger[]
  matchTicketIds?: ReplayInteger[]
  sourceTicketIds?: ReplayInteger[]
  seedAttempts?: ReplayInteger
  error?: string
  result?: string
  ticketId?: ReplayInteger
  ticket?: { ticketId?: ReplayInteger; TicketID?: ReplayInteger; CreatedAt?: ReplayInteger }
  [key: string]: unknown
}

export interface ReplayResult {
  instanceId: string
  targetRuleId: string
  status: 'completed' | 'interrupted'
  interruptedAt?: string
  error?: string
  events: ReplayEvent[]
  rounds: ReplayInteger
  calls: ReplayInteger
  matches: ReplayInteger
  seedAttempts: ReplayInteger
}

export interface ReplayTimeline {
  schemaVersion: string
  instanceId: string
  events: ReplayEvent[]
}

/** 仅用于展示：将 JSON 中超出安全范围的整数保留为十进制字符串。 */
export function parseReplayJson<T>(text: string): T {
  let converted = ''
  let index = 0
  while (index < text.length) {
    const start = index
    if (text[index] === '"') {
      index++
      while (index < text.length) {
        if (text[index] === '\\') index += 2
        else if (text[index++] === '"') break
      }
      converted += text.slice(start, index)
      continue
    }
    if (text[index] === '-' || /[0-9]/.test(text[index])) {
      const token = text.slice(index).match(/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/)?.[0]
      if (token) {
        index += token.length
        converted +=
          /^-?\d+$/.test(token) && !Number.isSafeInteger(Number(token)) ? `"${token}"` : token
        continue
      }
    }
    converted += text[index++]
  }
  return JSON.parse(converted) as T
}

export function readReplayTimeline(text: string): ReplayTimeline {
  const value = parseReplayJson<ReplayTimeline>(text)
  if (
    !value ||
    value.schemaVersion !== 'match-timeline/v1' ||
    typeof value.instanceId !== 'string' ||
    !Array.isArray(value.events) ||
    !value.events.every(
      (event) =>
        event &&
        typeof event === 'object' &&
        typeof event.type === 'string' &&
        (typeof event.sequence === 'string' || typeof event.sequence === 'number'),
    )
  )
    throw new Error('时间线格式无效，请选择 match-timeline/v1 文件。')
  return value
}

/** 保留纳秒精度显示偏移，避免将大整数转成 Number。 */
export function replayOffset(value: ReplayInteger | undefined): string {
  if (
    value === undefined ||
    (typeof value === 'number' && !Number.isSafeInteger(value)) ||
    !/^-?\d+$/.test(String(value))
  )
    return '—'
  const ns = BigInt(value)
  const magnitude = ns < 0n ? -ns : ns
  const fraction = String(magnitude % 1_000_000n)
    .padStart(6, '0')
    .replace(/0+$/, '')
  return `${ns < 0n ? '-' : '+'}${magnitude / 1_000_000n}${fraction ? `.${fraction}` : ''} ms`
}

/** 按记录顺序标识轮次内的调用，票据事件仍保留在实际发生位置。 */
export function replayRows(events: ReplayEvent[]) {
  const calls = new Map<string, number>()
  return events.map((event) => {
    const round = event.round === undefined ? undefined : String(event.round)
    let call: number | undefined
    if (event.type === 'produce_match' && round !== undefined) {
      call = (calls.get(round) ?? 0) + 1
      calls.set(round, call)
    }
    return { event, call }
  })
}
