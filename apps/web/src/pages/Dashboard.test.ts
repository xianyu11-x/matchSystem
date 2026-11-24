import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { expect, it } from 'vitest'
import { demoScenario, demoTopology } from '../lib/mockData'
import { queryKeys } from '../lib/queries'
import { Dashboard } from './Dashboard'

it('路由策略位于拓扑标题之后、物理节点控件之前', () => {
  const client = new QueryClient()
  client.setQueryData(queryKeys.scenario, { ...demoScenario, rawScenario: {} })
  client.setQueryData(queryKeys.topology, demoTopology)
  client.setQueryData(queryKeys.matches, { items: [], total: 0 })
  const html = renderToStaticMarkup(
    createElement(MemoryRouter, {
      children: createElement(QueryClientProvider, {
        client,
        children: createElement(Dashboard),
      }),
    }),
  )

  expect(html).toMatch(
    /<div class="panel panel-wide"><div class="section-title"><div><h2>拓扑 \/ Routing<\/h2>[\s\S]*?<\/div><\/div><section class="routing-settings"/,
  )
  expect(html.match(/class="routing-settings"/g)).toHaveLength(1)
  expect(html.indexOf('模拟操作步骤')).toBeLessThan(html.indexOf('class="routing-settings"'))
  expect(html.indexOf('class="routing-settings"')).toBeLessThan(
    html.indexOf('class="physical-node-controls"'),
  )
  client.clear()
})
