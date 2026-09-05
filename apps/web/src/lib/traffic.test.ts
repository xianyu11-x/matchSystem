import { afterEach, expect, it, vi } from 'vitest'
import { api, trafficRequest } from './api'
afterEach(() => vi.unstubAllGlobals())
it('uses the real traffic endpoint and preserves generator configuration', async () => {
  const fetcher = vi.fn().mockImplementation(
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
      atomic: true,
      objectFacts: { stringLists: {}, uint64Lists: {}, int64Values: { latency: 42 } },
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
  expect(body.generator).not.toHaveProperty('atomic')
  expect(body.generator.objectFacts.int64Values.latency).toBe(42)
  await trafficRequest('DELETE')
  expect(fetcher.mock.calls[1][1].method).toBe('DELETE')
})

it('sends batch failure handling and common facts to the batch endpoint', async () => {
  const fetcher = vi.fn(async () => new Response('{"accepted":2}', { status: 201 }))
  vi.stubGlobal('fetch', fetcher)
  await api.createBatch({
    count: 2,
    seed: 7,
    ruleKey: 'test:1',
    rule: { namespace: 'test', ruleId: 1 },
    atomic: true,
    createdAtStep: 250,
    objectFacts: { stringLists: {}, uint64Lists: {}, int64Values: { latency: 42 } },
  })
  const [url, options] = fetcher.mock.calls[0] as unknown as [string, RequestInit]
  expect(url).toContain('/tickets/batch')
  expect(JSON.parse(options.body as string)).toMatchObject({
    atomic: true,
    createdAtStep: 250,
    objectFacts: { int64Values: { latency: 42 } },
  })
})
