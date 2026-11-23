import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from './api'
import { queryKeys, useClearPhysicalNodeTickets } from './queries'

afterEach(() => vi.restoreAllMocks())

it.each(['success', 'error'])(
  'refreshes topology and tickets after a clear %s',
  async (outcome) => {
    const client = new QueryClient()
    const ticketsKey = queryKeys.tickets({ limit: 10 })
    for (const key of [queryKeys.topology, ticketsKey, queryKeys.scenario, queryKeys.matches]) {
      client.setQueryData(key, {})
    }
    const clear = vi.spyOn(api, 'clearPhysicalNodeTickets')
    if (outcome === 'success') clear.mockResolvedValue({ removedTickets: 2 })
    else clear.mockRejectedValue(new Error('节点不存在'))

    let mutate!: (id: string) => Promise<{ removedTickets: number }>
    function Hook() {
      mutate = useClearPhysicalNodeTickets().mutateAsync
      return null
    }
    renderToStaticMarkup(
      createElement(QueryClientProvider, { client, children: createElement(Hook) }),
    )

    if (outcome === 'success') await expect(mutate('2')).resolves.toEqual({ removedTickets: 2 })
    else await expect(mutate('2')).rejects.toThrow('节点不存在')
    expect(clear).toHaveBeenCalledWith('2')
    expect(client.getQueryState(queryKeys.topology)?.isInvalidated).toBe(true)
    expect(client.getQueryState(ticketsKey)?.isInvalidated).toBe(true)
    expect(client.getQueryState(queryKeys.scenario)?.isInvalidated).toBe(false)
    expect(client.getQueryState(queryKeys.matches)?.isInvalidated).toBe(false)
  },
)
