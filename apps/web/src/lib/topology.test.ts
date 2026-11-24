import { afterEach, expect, it, vi } from 'vitest'
import { api } from './api'
afterEach(() => vi.unstubAllGlobals())

it('maps actual runtime states and keeps same-placement rules distinct without invented load', async () => {
  const node = (ruleId: string | number, state: string) => ({
    key: {
      rule: { namespace: '18446744073709551615', ruleId },
      placementId: '18446744073709551614',
    },
    state,
    ticketCount: 1000,
  })
  vi.stubGlobal(
    'fetch',
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            physicalNodes: [
              {
                physicalNodeId: '1',
                endpoint: 'inproc://p1',
                enabled: true,
                logicalNodes: [
                  node('9007199254740993', 'Ready'),
                  node(2, 'Draining'),
                  node(3, 'Stopped'),
                ],
              },
              { physicalNodeId: '2', enabled: false, logicalNodes: [node(1, 'Ready')] },
              { physicalNodeId: '3', endpoint: 'inproc://empty', enabled: true },
            ],
          }),
          { headers: { 'content-type': 'application/json' } },
        ),
    ),
  )
  const result = await api.getTopology()
  expect(result.nodes.map((node) => node.state)).toEqual([
    'healthy',
    'degraded',
    'stopped',
    'stopped',
  ])
  expect(new Set(result.nodes.map((node) => node.id)).size).toBe(4)
  expect(result.nodes[0]).not.toHaveProperty('load')
  expect(result.nodes[0].ticketCount).toBe(1000)
  expect(result.nodes[0].ruleKey).toBe('9007199254740993')
  expect(result.nodes[0].placementId).toBe('18446744073709551614')
  expect(result.physicalNodes.map((physical) => physical.id)).toEqual(['1', '2', '3'])
  expect(result.physicalNodes[0]).toMatchObject({
    endpoint: 'inproc://p1',
    enabled: true,
    logicalNodes: result.nodes.slice(0, 3),
  })
  expect(result.physicalNodes[2]).toEqual({
    id: '3',
    endpoint: 'inproc://empty',
    enabled: true,
    logicalNodes: [],
  })
})

it('sends dynamic node operations with exact identities and handles empty 204 responses', async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValue(
      new Response(null, { status: 204, headers: { 'content-type': 'application/json' } }),
    )
  vi.stubGlobal('fetch', fetchMock)
  const id = '18446744073709551615'
  const request = {
    physicalNode: { id, endpoint: ' inproc://节点 ', enabled: true, selector: 'round_robin' },
    deployments: [
      {
        logicalNode: {
          rule: { namespace: '18446744073709551615', ruleId: '9007199254740993' },
          placementId: '18446744073709551614',
        },
        enabled: true,
        weight: 1,
      },
      {
        logicalNode: { rule: { ruleId: '5' }, placementId: '2' },
        enabled: false,
        weight: 2,
        applyLatest: true,
        keepTickFacts: true,
      },
    ],
  }

  await api.addPhysicalNode(request)
  await api.updatePhysicalNode(request)
  await api.deletePhysicalNode(id)

  expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/physical-nodes')
  expect(fetchMock.mock.calls[0][1]).toMatchObject({
    method: 'POST',
    body: JSON.stringify(request),
  })
  expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/physical-nodes')
  expect(fetchMock.mock.calls[1][1]).toMatchObject({
    method: 'PUT',
    body: JSON.stringify(request),
  })
  const [url, options] = fetchMock.mock.calls[2]
  expect(new URL(url, 'http://localhost').searchParams.get('physicalNodeId')).toBe(id)
  expect(options.method).toBe('DELETE')
})

it('通过独立路由接口更新策略', async () => {
  const config = { algorithm: 'weighted_priority' as const, ticketLimit: 50 }
  const fetchMock = vi.fn().mockResolvedValue(
    new Response(JSON.stringify(config), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    }),
  )
  vi.stubGlobal('fetch', fetchMock)
  expect(await api.updateRouting(config)).toEqual(config)
  expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/routing')
  expect(fetchMock.mock.calls[0][1]).toMatchObject({ method: 'PUT', body: JSON.stringify(config) })
})
