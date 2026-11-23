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
})
