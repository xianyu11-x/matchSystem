import type { FactValue, MatchRecord } from '../types'

export interface MatchNumericField {
  key: string
  label: string
  description: string
}

export interface NumericStatistics {
  count: number
  mean: number
  variance: number
  standardDeviation: number
  min: number
  max: number
  range: number
  median: number
  p95: number
}

const matchFields: Array<{
  key: string
  label: string
  description: string
  read: (match: MatchRecord) => unknown
}> = [
  {
    key: 'memberCount',
    label: '成员数',
    description: '每场比赛包含的 Ticket 数量',
    read: (match) => match.memberCount,
  },
  {
    key: 'durationMs',
    label: '队列等待耗时',
    description: '最早成员从创建到本轮完成的等待时间（毫秒）；不代表引擎处理耗时',
    read: (match) => match.durationMs,
  },
  {
    key: 'round',
    label: '轮次',
    description: '产生该比赛的 Round 序号',
    read: (match) => match.round,
  },
]

function finiteNumbers(value: unknown): number[] {
  if (Array.isArray(value))
    return value.filter(
      (item): item is number => typeof item === 'number' && Number.isSafeInteger(item),
    )
  return typeof value === 'number' && Number.isSafeInteger(value) ? [value] : []
}

function factValues(match: MatchRecord, name: string): number[] {
  return finiteNumbers(match.facts?.[name])
}

/**
 * Discover the numeric properties available in a retained Match set.
 * Match Facts can be scalar values or numeric lists; list values become one
 * observation per item when statistics are calculated.
 */
export function numericFields(matches: MatchRecord[]): MatchNumericField[] {
  const fields = matchFields.filter((field) =>
    matches.some((match) => finiteNumbers(field.read(match)).length),
  )
  const factNames = new Set<string>()
  for (const match of matches)
    for (const [name, value] of Object.entries(match.facts ?? {}))
      if (finiteNumbers(value).length > 0) factNames.add(name)

  return [
    ...fields.map(({ key, label, description }) => ({ key, label, description })),
    ...Array.from(factNames)
      .sort((left, right) => left.localeCompare(right))
      .map((name) => ({
        key: `fact:${name}`,
        label: `Match Fact / ${name}`,
        description: `来自 Match Facts 的数值属性；数值列表按元素计入样本`,
      })),
  ]
}

export function valuesForField(match: MatchRecord, fieldKey: string): number[] {
  if (fieldKey.startsWith('fact:')) return factValues(match, fieldKey.slice('fact:'.length))
  const field = matchFields.find((item) => item.key === fieldKey)
  return field ? finiteNumbers(field.read(match)) : []
}

/** Return all finite observations for one selected field across the matches. */
export function numericValues(matches: MatchRecord[], fieldKey: string): number[] {
  return matches.flatMap((match) => valuesForField(match, fieldKey))
}

/**
 * Calculate descriptive statistics using population variance (divide by N),
 * which describes the complete retained match window rather than estimating
 * a larger sample. P95 uses linear interpolation between sorted observations.
 */
export function calculateStatistics(values: number[]): NumericStatistics | undefined {
  // Match numeric fields are int64/uint64 observations. Keep the same exact
  // integer boundary here as the wire decoder so a caller cannot reintroduce
  // a rounded value by invoking the statistic helper directly.
  const sorted = values.filter((value) => Number.isSafeInteger(value)).sort((a, b) => a - b)
  const count = sorted.length
  if (count === 0) return undefined
  const mean = sorted.reduce((sum, value) => sum + value, 0) / count
  const variance = sorted.reduce((sum, value) => sum + (value - mean) ** 2, 0) / count
  const percentile = (ratio: number) => {
    const position = (count - 1) * ratio
    const lower = Math.floor(position)
    const upper = Math.ceil(position)
    if (lower === upper) return sorted[lower]
    return sorted[lower] + (sorted[upper] - sorted[lower]) * (position - lower)
  }
  return {
    count,
    mean,
    variance,
    standardDeviation: Math.sqrt(variance),
    min: sorted[0],
    max: sorted[count - 1],
    range: sorted[count - 1] - sorted[0],
    median: percentile(0.5),
    p95: percentile(0.95),
  }
}

export function matchInTimeRange(
  match: MatchRecord,
  start: Date | undefined,
  end: Date | undefined,
): boolean {
  const timestamp = Date.parse(match.createdAt)
  if (!Number.isFinite(timestamp)) return false
  if (start && timestamp < start.getTime()) return false
  if (end && timestamp >= end.getTime()) return false
  return true
}

/** Extract numeric values while preserving a useful type guard for callers. */
export function isNumericFact(value: FactValue | undefined): value is number | number[] {
  return finiteNumbers(value).length > 0
}
