import { describe, expect, it } from 'vitest'
import type { MatchRecord, Ticket, TypedAttributes } from '../types'
import { ticketAttributeAnalysis, ticketAttributeFields } from './ticketAnalytics'
import { numericFields, numericValues } from './matchAnalytics'
import { analysisCSV, analysisPoints } from './analysisChart'

const member = (id: string, attributes: Partial<TypedAttributes>): Ticket => ({
  ticketId: id,
  createdAt: '2026-09-06T00:00:00Z',
  status: 'matched',
  facts: {},
  attributes: { strings: {}, int64: {}, uint64s: {}, ...attributes },
})
const matches: MatchRecord[] = [
  {
    matchId: 'a',
    createdAt: '2026-09-06T00:01:00Z',
    roundId: '1',
    ruleKey: 'demo/1',
    placementId: 'default',
    ticketIds: ['1', '2', '3', '4'],
    memberCount: 4,
    members: [
      member('1', { int64: { score: 0 }, strings: { region: ['cn', 'cn', 'a,b'] } }),
      member('2', { int64: { score: 20 }, strings: { region: ['cn', ''] } }),
      member('3', { strings: { region: [] } }),
      member('4', { int64: { score: '9223372036854775807' } }),
    ],
  },
]
describe('Ticket member attribute analytics', () => {
  it('discovers numeric fields and weights statistics by members while preserving zero', () => {
    const score = ticketAttributeFields(matches).find((field) => field.name === 'score')!
    expect(numericFields(matches).some((field) => field.key === `ticket:${score.key}`)).toBe(true)
    expect(numericValues(matches, `ticket:${score.key}`)).toEqual([0, 20])
    expect(analysisPoints(matches, `ticket:${score.key}`, 'match', 'mean')[0]).toMatchObject({
      samples: 2,
      value: 10,
    })
    expect(ticketAttributeAnalysis(matches, score)).toMatchObject({
      present: 3,
      missing: 1,
      excluded: 1,
      numbers: [0, 20],
    })
  })
  it('counts category membership once per member, retaining commas and empty values', () => {
    const region = ticketAttributeFields(matches).find((field) => field.name === 'region')!
    const result = ticketAttributeAnalysis(matches, region)
    expect(result).toMatchObject({ present: 3, missing: 1, empty: 1 })
    expect(result.points.map((point) => [point.label, point.value])).toEqual([
      ['"cn"', 2],
      ['""（空字符串）', 1],
      ['"a,b"', 1],
    ])
    const csv = analysisCSV(result.points, {
      field: region.key,
      statistic: 'count',
      grouping: 'category',
    })
    expect(csv).toContain('category')
    expect(csv).toContain('a,b')
  })
  it('reports missing member snapshots and numeric lists without coercing strings', () => {
    const records = [
      {
        ...matches[0],
        memberCount: 3,
        members: [member('x', { uint64s: { ids: [0, 5, '18446744073709551615'] } })],
      },
    ]
    const field = ticketAttributeFields(records)[0]
    expect(ticketAttributeAnalysis(records, field)).toMatchObject({
      numbers: [0, 5],
      excluded: 1,
      unavailable: 2,
    })
    expect(ticketAttributeAnalysis([], field)).toMatchObject({
      present: 0,
      numbers: [],
      points: [],
    })
  })
})
