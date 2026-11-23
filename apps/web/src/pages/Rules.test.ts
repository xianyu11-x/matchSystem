import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { demoCapabilities } from '../lib/mockData'
import { queryKeys } from '../lib/queries'
import { newPortableRule, useLibraryWorkspace } from '../lib/ruleLibrary'
import { useRuleStore } from '../lib/ruleStore'
import { Rules } from './Rules'

it('空规则库页面提供新建、导入和保存入口', () => {
  const snapshot = { revision: '1234567890123456789', rules: [newPortableRule({ ruleId: 5 })] }
  useLibraryWorkspace.getState().hydrate(snapshot, true)
  useRuleStore.getState().setDocument(useLibraryWorkspace.getState().rules[0])
  const queryClient = new QueryClient()
  queryClient.setQueryData(queryKeys.ruleLibrary, snapshot)
  queryClient.setQueryData(queryKeys.capabilities, demoCapabilities)
  const html = renderToStaticMarkup(
    createElement(QueryClientProvider, {
      client: queryClient,
      children: createElement(Rules),
    }),
  )
  expect(html).toContain('保存规则库')
  expect(html).toContain('新建规则')
  expect(html).toContain('导出规则库')
  expect(html).not.toContain('保存场景')
  expect(html).not.toContain('场景与部署')
  useRuleStore.getState().clearGraphSession()
  queryClient.clear()
})
