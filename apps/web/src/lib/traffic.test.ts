import { afterEach, expect, it, vi } from 'vitest'
import { trafficRequest } from './api'
afterEach(() => vi.unstubAllGlobals())
it('uses the real traffic endpoint and preserves generator configuration', async () => {
  const fetcher = vi
    .fn()
    .mockImplementation(
      async () =>
        new Response(JSON.stringify({ state: 'running' }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
    )
  vi.stubGlobal('fetch', fetcher)
  await trafficRequest(
    'POST',
    {
      distribution: 'poisson',
      rate: 10,
      burstSize: 1,
      burstIntervalMs: 1000,
      matchIntervalMs: 150,
      maxMatches: 2,
      seed: 42,
    },
    {
      count: 5,
      seed: 7,
      ruleKey: 'test:1',
      rule: { namespace: 'test', ruleId: 1 },
      placementId: 'p1',
      startTicketId: 100,
      int64Ranges: { score: { min: 1, max: 5 } },
    },
  )
  const [url, options] = fetcher.mock.calls[0]
  expect(url).toContain('/traffic')
  expect(options.method).toBe('POST')
  const body = JSON.parse(options.body)
  expect(body.generator).toMatchObject({
    seed: 7,
    startTicketId: 100,
    int64Ranges: { score: { min: 1, max: 5 } },
  })
  expect(body.generator).not.toHaveProperty('ruleKey')
  expect(body.generator).not.toHaveProperty('placementId')
  await trafficRequest('DELETE')
  expect(fetcher.mock.calls[1][1].method).toBe('DELETE')
})
