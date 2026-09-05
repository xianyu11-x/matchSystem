import { describe, expect, it } from 'vitest'
import type { MatchRecord } from '../types'
import {
  calculateStatistics,
  matchesForAnalysis,
  matchInTimeRange,
  numericFields,
  numericValues,
  valuesForField,
} from './matchAnalytics'

const match = (overrides: Partial<MatchRecord> = {}): MatchRecord => ({
  matchId: 'match-1',
  createdAt: '2026-08-29T08:35:00.000Z',
  roundId: 'round-7',
  round: 7,
  ruleKey: 'ranked-5v5',
  placementId: 'sea-1',
  ticketIds: ['ticket-1', 'ticket-2'],
  memberCount: 2,
  facts: { latencyMs: 20, buckets: [2, 4], label: ['sea'] },
  ...overrides,
})

describe('match analytics', () => {
  it('calculates population descriptive statistics and interpolated p95', () => {
    const statistics = calculateStatistics([1, 2, 3, 4])!
    expect(statistics.count).toBe(4)
    expect(statistics.mean).toBe(2.5)
    expect(statistics.variance).toBe(1.25)
    expect(statistics.standardDeviation).toBe(Math.sqrt(1.25))
    expect(statistics.min).toBe(1)
    expect(statistics.max).toBe(4)
    expect(statistics.range).toBe(3)
    expect(statistics.median).toBe(2.5)
    expect(statistics.p95).toBeCloseTo(3.85)
  })

  it('ignores non-numeric observations and returns undefined when empty', () => {
    expect(calculateStatistics([Number.NaN, Number.POSITIVE_INFINITY])).toBeUndefined()
    expect(calculateStatistics([1, Number.NaN, 3])?.count).toBe(2)
    expect(calculateStatistics([Number.MAX_SAFE_INTEGER + 1, 3])?.count).toBe(1)
  })

  it('discovers match fields and numeric Match Facts', () => {
    const fields = numericFields([match(), match({ durationMs: 36, facts: { latencyMs: 25 } })])
    expect(fields.map((field) => field.key)).toEqual([
      'memberCount',
      'durationMs',
      'round',
      'fact:buckets',
      'fact:latencyMs',
    ])
    expect(valuesForField(match(), 'fact:buckets')).toEqual([2, 4])
    expect(numericValues([match(), match({ memberCount: 4 })], 'memberCount')).toEqual([2, 4])
  })

  it('uses an inclusive start and exclusive end for time windows', () => {
    const start = new Date('2026-08-29T08:35:00.000Z')
    const end = new Date('2026-08-29T08:36:00.000Z')
    expect(matchInTimeRange(match(), start, end)).toBe(true)
    expect(matchInTimeRange(match({ createdAt: '2026-08-29T08:36:00.000Z' }), start, end)).toBe(
      false,
    )
  })
})

describe('selected match aggregation', () => {
  const matches = [
    match({ matchId: 'a', durationMs: 10, processingDurationNs: 2000 }),
    match({ matchId: 'b', durationMs: 30, processingDurationNs: 8000 }),
    match({ matchId: 'c' }),
  ]
  it('aggregates only explicitly selected records within the current window', () => {
    const selected = matchesForAnalysis(matches, new Set(['a', 'b', 'evicted']))
    expect(calculateStatistics(numericValues(selected, 'durationMs'))?.mean).toBe(20)
    expect(calculateStatistics(numericValues(selected, 'processingDurationNs'))?.mean).toBe(5000)
    expect(matchesForAnalysis(matches.slice(1), new Set(['a', 'b']))).toEqual([matches[1]])
    expect(matchesForAnalysis(matches, undefined)).toEqual(matches)
    expect(matchesForAnalysis(matches, new Set())).toEqual([])
  })
  it('preserves missing measurements and treats measured zero as a real sample', () => {
    expect(numericValues(matches, 'processingDurationNs')).toEqual([2000, 8000])
    expect(numericValues([match({ processingDurationNs: 0 })], 'processingDurationNs')).toEqual([0])
    expect(numericFields(matches).some((field) => field.key === 'processingDurationNs')).toBe(true)
  })
})
