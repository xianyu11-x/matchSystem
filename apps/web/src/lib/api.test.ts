import { describe, expect, it, vi } from 'vitest'
import type { MatchRecord, MatchesPage } from '../types'
import {
  api,
  collectAllMatches,
  safeWireInt64Number,
  safeWireUint64Number,
  safeWireUint64Text,
} from './api'
import { mergeMatchPage } from './queries'

const match = (matchId: string): MatchRecord => ({
  matchId,
  createdAt: '2026-08-29T08:35:00.000Z',
  roundId: 'round-1',
  ruleKey: 'ranked-5v5',
  placementId: 'sea-1',
  ticketIds: [],
  memberCount: 0,
})

const page = (items: MatchRecord[], nextCursor?: string): MatchesPage => ({
  items,
  nextCursor,
  total: 3,
})

describe('Match API helpers', () => {
  it('follows multiple offset pages and de-duplicates by matchId', async () => {
    const calls: Array<{ cursor?: string; limit?: number }> = []
    const readPage = async (params: { cursor?: string; limit?: number }): Promise<MatchesPage> => {
      calls.push(params)
      if (!params.cursor) return page([match('a'), match('b')], '2')
      return page([match('c')])
    }

    const result = await collectAllMatches(readPage)

    expect(result.map((item) => item.matchId)).toEqual(['a', 'b', 'c'])
    expect(calls).toHaveLength(3) // two pages plus first-page stability check
    expect(calls[1]).toMatchObject({ cursor: '2', limit: 1000 })
  })

  it('retries when a moving offset produces a duplicate across pages', async () => {
    let firstPageCalls = 0
    let secondPageCalls = 0
    const readPage = async (params: { cursor?: string; limit?: number }): Promise<MatchesPage> => {
      if (!params.cursor) {
        firstPageCalls += 1
        return page([match('a'), match('b')], firstPageCalls <= 2 ? '2' : '2')
      }
      secondPageCalls += 1
      return secondPageCalls === 1 ? page([match('b'), match('c')]) : page([match('c')])
    }

    const result = await collectAllMatches(readPage)

    expect(result.map((item) => item.matchId)).toEqual(['a', 'b', 'c'])
    expect(firstPageCalls).toBe(4) // initial page, verification, retry page, retry verification
    expect(secondPageCalls).toBe(2)
  })

  it('excludes unsafe numeric uint64 values but preserves safe quoted identifiers', () => {
    expect(safeWireUint64Number(Number.MAX_SAFE_INTEGER)).toBe(Number.MAX_SAFE_INTEGER)
    expect(safeWireUint64Number(Number.MAX_SAFE_INTEGER + 1)).toBeUndefined()
    expect(safeWireUint64Number(String(Number.MAX_SAFE_INTEGER))).toBe(Number.MAX_SAFE_INTEGER)
    expect(safeWireUint64Number('9007199254740992')).toBeUndefined()
    expect(safeWireUint64Text('18446744073709551615')).toBe('18446744073709551615')
    expect(safeWireUint64Text('18446744073709551616')).toBeUndefined()
  })

  it('excludes int64 values outside the exact JavaScript integer range', () => {
    expect(safeWireInt64Number(Number.MAX_SAFE_INTEGER)).toBe(Number.MAX_SAFE_INTEGER)
    expect(safeWireInt64Number(-Number.MAX_SAFE_INTEGER)).toBe(-Number.MAX_SAFE_INTEGER)
    expect(safeWireInt64Number('9007199254740991')).toBe(Number.MAX_SAFE_INTEGER)
    expect(safeWireInt64Number('-9007199254740991')).toBe(-Number.MAX_SAFE_INTEGER)
    expect(safeWireInt64Number('9007199254740992')).toBeUndefined()
    expect(safeWireInt64Number('-9007199254740992')).toBeUndefined()
    expect(safeWireInt64Number(Number.MAX_SAFE_INTEGER + 1)).toBeUndefined()
  })

  it('keeps unsafe Match Fact values out of statistics and reports exclusions', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: { get: () => 'application/json' },
      json: async () => ({
        items: [
          {
            matchId: 'unsafe-facts',
            memberCount: '7',
            round: '9007199254740992',
            createdAt: 1_700_000_000_000,
            durationMs: 25,
            facts: {
              omittedNumericSamples: 1,
              uint64Lists: { safeUint: ['4'] },
              int64Values: { safeInt: '-3', unsafe: '9007199254740992' },
            },
          },
        ],
        total: 1,
      }),
    })
    vi.stubGlobal('fetch', fetchMock)
    try {
      const page = await api.getMatches({ limit: 1 })
      expect(page.items[0].memberCount).toBe(7)
      expect(page.items[0].facts).toEqual({ safeUint: [4], safeInt: -3 })
      expect(page.items[0].excludedNumericSamples).toBe(3)
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it('requests a complete refresh when a poll cannot cover all new records', () => {
    const cached = Array.from({ length: 200 }, (_, index) => match(`old-${index}`))
    const latest = Array.from({ length: 100 }, (_, index) => match(`new-${index}`))
    expect(mergeMatchPage(cached, { items: latest, total: 300 }).requiresFullRefresh).toBe(true)
    expect(mergeMatchPage(cached, { items: latest, total: 200 }).requiresFullRefresh).toBe(true)
  })

  it('clears cached history when the server reports an empty replacement', () => {
    const cached = [match('old-1')]
    expect(mergeMatchPage(cached, { items: [], total: 0 })).toEqual({
      matches: [],
      requiresFullRefresh: false,
    })
  })
})
