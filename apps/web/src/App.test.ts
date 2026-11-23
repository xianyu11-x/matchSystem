import { QueryClient } from '@tanstack/react-query'
import { expect, it } from 'vitest'
import { invalidateSimulatorEventQueries } from './App'
import { queryKeys } from './lib/queries'

it('refreshes topology and tickets for a physical node clear event without invalidating history', () => {
  const client = new QueryClient()
  const ticketsKey = queryKeys.tickets({ limit: 10 })
  for (const key of [queryKeys.topology, ticketsKey, queryKeys.scenario, queryKeys.matches]) {
    client.setQueryData(key, {})
  }
  invalidateSimulatorEventQueries(client, 'physical_node_tickets_cleared')
  expect(client.getQueryState(queryKeys.topology)?.isInvalidated).toBe(true)
  expect(client.getQueryState(ticketsKey)?.isInvalidated).toBe(true)
  expect(client.getQueryState(queryKeys.scenario)?.isInvalidated).toBe(false)
  expect(client.getQueryState(queryKeys.matches)?.isInvalidated).toBe(false)
})
