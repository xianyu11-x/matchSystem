import { describe, expect, it } from 'vitest'
import type { MatchRecord } from '../types'
import { analysisCSV, analysisPoints, recentMatchBuckets } from './analysisChart'
import { matchesForAnalysis, matchInTimeRange } from './matchAnalytics'

const match = (id: string, overrides: Partial<MatchRecord> = {}): MatchRecord => ({
  matchId: id,
  createdAt: '2026-09-06T08:00:00Z',
  roundId: '1',
  ruleKey: 'a',
  placementId: 'b',
  ticketIds: [],
  memberCount: 1,
  ...overrides,
})

describe('analysis chart and export data', () => {
  it('uses the same selected window, keeps zero, and excludes unsafe integers and missing measurements', () => {
    const start = new Date('2026-09-06T08:00:00Z')
    const end = new Date('2026-09-06T09:00:00Z')
    const records = [
      match('zero', { processingDurationNs: 0 }),
      match('missing'),
      match('unsafe', { processingDurationNs: Number.MAX_SAFE_INTEGER + 1 }),
      match('unselected', { processingDurationNs: 30 }),
      match('end', { createdAt: end.toISOString(), processingDurationNs: 50 }),
    ]
    const selected = matchesForAnalysis(
      records.filter((item) => matchInTimeRange(item, start, end)),
      new Set(['zero', 'missing', 'unsafe', 'end']),
    )
    const points = analysisPoints(selected, 'processingDurationNs', 'match', 'mean')
    expect(points.map((point) => [point.label, point.value])).toEqual([
      ['missing', null],
      ['unsafe', null],
      ['zero', 0],
    ])
    const csv = analysisCSV(points, {
      field: 'processingDurationNs',
      statistic: 'mean',
      grouping: 'match',
      start: start.toISOString(),
      end: end.toISOString(),
    })
    expect(csv).toContain('"2026-09-06T08:00:00.000Z","2026-09-06T09:00:00.000Z"')
    expect(csv).not.toContain('unselected')
    expect(csv).not.toContain('9007199254740992')
    expect(csv).toContain('"0",""\r\n')
    expect(csv).toContain('"1","0"\r\n')
    expect(
      analysisPoints(matchesForAnalysis(records, new Set()), 'durationMs', 'match', 'mean'),
    ).toEqual([])
  })

  it('weights Fact lists by samples and does not merge ambiguous display labels', () => {
    const records = [
      match('a', { facts: { values: [0, 10] } }),
      match('b', { facts: { values: [20] } }),
    ]
    expect(analysisPoints(records, 'fact:values', 'node', 'mean')[0]).toMatchObject({
      value: 10,
      samples: 3,
      matchIds: ['a', 'b'],
    })
    expect(
      analysisPoints(
        [
          match('a', { ruleKey: 'a / b', placementId: 'c' }),
          match('b', { ruleKey: 'a', placementId: 'b / c' }),
        ],
        'memberCount',
        'node',
        'count',
      ),
    ).toHaveLength(2)
  })

  it('escapes CSV delimiters, newlines and spreadsheet formulas without changing numeric negatives', () => {
    const csv = analysisCSV(
      [{ label: '=SUM(1,2)\n"x"', matchIds: ['18446744073709551615'], samples: 1, value: -3 }],
      { field: 'fact:test', statistic: 'min', grouping: 'match' },
    )
    expect(csv.startsWith('\uFEFF')).toBe(true)
    expect(csv).toContain('"\'=SUM(1,2)\n""x"""')
    expect(csv).toContain('18446744073709551615')
    expect(csv).toContain('"-3"')
  })

  it('buckets retained matches in six non-overlapping half-open five-minute intervals', () => {
    const now = Date.parse('2026-09-06T08:30:00Z')
    const points = recentMatchBuckets(
      [
        match('start'),
        match('boundary', { createdAt: '2026-09-06T08:05:00Z' }),
        match('outside', { createdAt: '2026-09-06T07:59:59Z' }),
        match('end', { createdAt: '2026-09-06T08:30:00Z' }),
      ],
      now,
    )
    expect(points.map((point) => point.value)).toEqual([1, 1, 0, 0, 0, 0])
    expect(points.flatMap((point) => point.matchIds)).toEqual(['start', 'boundary'])
  })
})
