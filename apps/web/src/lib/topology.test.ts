import { afterEach, expect, it, vi } from 'vitest'
import { api } from './api'
afterEach(() => vi.unstubAllGlobals())

it('maps actual runtime states and keeps same-placement rules distinct without invented load', async () => {
  const node = (ruleId: number, state: string) => ({
    key: { rule: { namespace: 'demo', ruleId }, placementId: 'default' },
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
                physicalNodeId: 'p1',
                enabled: true,
                logicalNodes: [node(1, 'Ready'), node(2, 'Draining'), node(3, 'Stopped')],
              },
              { physicalNodeId: 'p2', enabled: false, logicalNodes: [node(1, 'Ready')] },
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
})
