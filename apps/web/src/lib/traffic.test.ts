import { afterEach, expect, it, vi } from 'vitest'
import { api, trafficRequest } from './api'
import { demoRuleSummary } from './mockData'
import { ticketTargetFields, ticketTargets } from './ticketTargets'
afterEach(() => vi.unstubAllGlobals())

it('手动轮次原样发送每物理节点上限并接受跨节点汇总结果', async () => {
  const fetcher = vi.fn(
    async (_url: string, _options: RequestInit) =>
      new Response(JSON.stringify({ produced: 200, matches: [] }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
  )
  vi.stubGlobal('fetch', fetcher)
  const result = await api.startRound({ now: 1700000000000, matchLimit: 100 })
  expect(fetcher).toHaveBeenCalledOnce()
  expect(JSON.parse(fetcher.mock.calls[0][1].body as string)).toEqual({
    now: 1700000000000,
    maxMatches: 100,
  })
  expect(result.round.matchCount).toBe(200)
})

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
      ruleKey: '3:1',
      rule: { namespace: '3', ruleId: 1 },
      placementId: '1',
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
  expect(body.generator.placementId).toBe('1')
  expect(body.generator).not.toHaveProperty('atomic')
  expect(body.generator.objectFacts.int64Values.latency).toBe(42)
  await trafficRequest('DELETE')
  expect(fetcher.mock.calls[1][1].method).toBe('DELETE')
})

it.each(['rule', 'node'] as const)('单条、批量与持续流量一致使用 %s 目标', async (mode) => {
  const fetcher = vi.fn().mockImplementation(async (url: string) => {
    const payload = url.endsWith('/tickets')
      ? { ticket: { ticketId: 42, createdAt: 1700000000000 }, state: 'waiting' }
      : url.endsWith('/tickets/batch')
        ? { accepted: 1 }
        : { state: 'running' }
    return new Response(JSON.stringify(payload), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    })
  })
  vi.stubGlobal('fetch', fetcher)
  const target = ticketTargets([
    {
      ...demoRuleSummary,
      apiRule: { namespace: '旧/别名', ruleId: '9007199254740993' },
      ruleKey: '9007199254740993',
      physicalNodeId: '18446744073709551615',
      placementId: '18446744073709551614',
    },
  ])[mode][0]
  const fields = ticketTargetFields(target)!
  await api.createTicket({
    ticketId: '42',
    ...fields,
    attributes: { strings: {}, uint64s: {}, int64: {} },
    facts: {},
  })
  const spec = {
    count: 1,
    seed: 1,
    ruleKey: target.ruleId,
    ...fields,
  }
  await api.createBatch(spec)
  await trafficRequest(
    'POST',
    {
      distribution: 'constant',
      rate: 1,
      burstSize: 1,
      burstIntervalMs: 1000,
      matchIntervalMs: 1000,
      maxMatches: 1,
      seed: 1,
    },
    spec,
  )

  const single = JSON.parse(fetcher.mock.calls[0][1].body)
  const batch = JSON.parse(fetcher.mock.calls[1][1].body)
  const traffic = JSON.parse(fetcher.mock.calls[2][1].body).generator
  for (const body of [single, batch, traffic]) {
    expect(body.rule).toEqual({ namespace: '旧/别名', ruleId: '9007199254740993' })
    if (mode === 'node') expect(body.placementId).toBe('18446744073709551614')
    else expect(body).not.toHaveProperty('placementId')
  }
})

it('sends fixed-step clock settings to the traffic endpoint', async () => {
  const fetcher = vi.fn().mockImplementation(
    async () =>
      new Response(
        JSON.stringify({
          state: 'running',
          config: {
            mode: 'fixed',
            steps: 12,
            stepMs: 250,
            startTimeMs: 1700000000000,
          },
          step: 1,
          totalSteps: 12,
          virtualNow: 1700000000000,
          wallElapsedMs: 3,
        }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      ),
  )
  vi.stubGlobal('fetch', fetcher)

  const result = await trafficRequest('POST', {
    mode: 'fixed',
    distribution: 'constant',
    rate: 10,
    burstSize: 1,
    burstIntervalMs: 1000,
    matchIntervalMs: 250,
    maxMatches: 2,
    seed: 42,
    steps: 12,
    stepMs: 250,
    startTimeMs: 1700000000000,
  })

  expect(JSON.parse(fetcher.mock.calls[0][1].body).config).toMatchObject({
    mode: 'fixed',
    steps: 12,
    stepMs: 250,
    startTimeMs: 1700000000000,
  })
  expect(result).toMatchObject({
    state: 'running',
    step: 1,
    totalSteps: 12,
    virtualNow: 1700000000000,
    wallElapsedMs: 3,
  })
})

it.each(['realtime', 'fixed'] as const)('持续流量 %s 模式原样发送和读取零上限', async (mode) => {
  const fetcher = vi.fn(async (_url: string, options: RequestInit) => {
    const config = JSON.parse(options.body as string).config
    return new Response(JSON.stringify({ state: 'running', config }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    })
  })
  vi.stubGlobal('fetch', fetcher)
  const config = {
    mode,
    distribution: 'constant' as const,
    rate: 10,
    burstSize: 1,
    burstIntervalMs: 1000,
    matchIntervalMs: 1000,
    maxMatches: 0,
    seed: 42,
  }
  const response = await trafficRequest('POST', config)
  expect(JSON.parse(fetcher.mock.calls[0][1].body as string).config.maxMatches).toBe(0)
  expect(response.config.maxMatches).toBe(0)
})

it('sends batch failure handling and common facts to the batch endpoint', async () => {
  const fetcher = vi.fn(async () => new Response('{"accepted":2}', { status: 201 }))
  vi.stubGlobal('fetch', fetcher)
  await api.createBatch({
    count: 2,
    seed: 7,
    ruleKey: '3:1',
    rule: { namespace: '3', ruleId: 1 },
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
