import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { PhysicalNodeEditor, PhysicalNodeManager } from './PhysicalNodeManager'
import { physicalNodeFormFromScenario } from '../lib/physicalNodes'
import { newPortableRule } from '../lib/ruleLibrary'
import type { JsonObject, Topology } from '../types'

it('shows empty physical nodes and the removal impact on occupied nodes', () => {
  const logical = {
    id: 'logical-1',
    name: 'occupied / p1',
    ruleKey: '1/4',
    placementId: '1',
    ticketCount: 3,
    state: 'healthy' as const,
  }
  const topology: Topology = {
    updatedAt: '2026-11-20',
    nodes: [logical],
    physicalNodes: [
      { id: '1', endpoint: 'inproc://empty', enabled: true, logicalNodes: [] },
      { id: '2', endpoint: 'inproc://occupied', enabled: true, logicalNodes: [logical] },
    ],
    routes: [],
  }
  const html = renderToStaticMarkup(
    createElement(QueryClientProvider, {
      client: new QueryClient(),
      children: createElement(PhysicalNodeManager, { topology }),
    }),
  )
  expect(html).toContain('暂无规则部署，不能接收匹配对象。')
  expect(html).toContain('删除物理节点 1')
  expect(html).toContain('删除物理节点 2')
  expect(html).toContain('编辑物理节点 2')
  expect(html).toMatch(/aria-label="清空物理节点 1 的等待 Ticket" disabled=""/)
  expect(html).toMatch(/aria-label="清空物理节点 2 的等待 Ticket">清空等待 Ticket/)
  expect(html).toContain('3 个等待对象')
  expect(html).toContain('保留比赛历史')
  expect(html.indexOf('添加物理节点')).toBeLessThan(html.indexOf('inproc://empty'))
})

it('shows separate deployment selectors and retains a rule absent from the library as a snapshot', () => {
  const scenario: JsonObject = {
    physicalNodes: [{ id: '3', endpoint: 'inproc://p', enabled: true }],
    rules: [
      {
        logicalNode: { rule: { namespace: '1', ruleId: 1 }, placementId: '1' },
        physicalNodeId: '3',
        weight: 1,
        enabled: true,
      },
      {
        logicalNode: { rule: { namespace: '1', ruleId: 2 }, placementId: '2' },
        physicalNodeId: '3',
        weight: 2,
        enabled: true,
        tickFacts: { int64s: { wait: 3 } },
        pythonProvider: { script: 'provider.py' },
      },
    ],
  }
  const html = renderToStaticMarkup(
    createElement(PhysicalNodeEditor, {
      form: physicalNodeFormFromScenario(scenario, '3'),
      editing: true,
      busy: false,
      libraryReady: true,
      rules: [newPortableRule({ namespace: '1', ruleId: 1 })],
      onChange: () => undefined,
      onCancel: () => undefined,
      onSubmit: () => undefined,
    }),
  )
  expect(html).toContain('逻辑节点部署 1')
  expect(html).toContain('逻辑节点部署 2')
  expect(html).toContain('1/1')
  expect(html).toContain('1/2')
  expect(html).toContain('规则已不在规则库')
  expect(html).toContain('继续使用当前运行快照')
  expect(html).toContain('应用规则库中的最新版本')
  expect(html).toContain('provider.py')
  expect(html).toContain('&quot;wait&quot;: 3')
  expect(html).not.toContain('节点端点')
})

it('matches the canonical uint64 rule in the library to its existing deployment', () => {
  const namespace = '18446744073709551615'
  const ruleId = '9007199254740993'
  const scenario: JsonObject = {
    physicalNodes: [{ id: '42', endpoint: 'inproc://42', enabled: true }],
    rules: [
      {
        logicalNode: { rule: { namespace, ruleId }, placementId: '17' },
        physicalNodeId: '42',
        weight: 1,
        enabled: true,
      },
    ],
  }
  const html = renderToStaticMarkup(
    createElement(PhysicalNodeEditor, {
      form: physicalNodeFormFromScenario(scenario, '42'),
      editing: true,
      busy: false,
      libraryReady: true,
      rules: [newPortableRule({ namespace, ruleId })],
      onChange: () => undefined,
      onCancel: () => undefined,
      onSubmit: () => undefined,
    }),
  )
  expect(html).toContain(`${namespace}/${ruleId}`)
  expect(html).toContain('应用规则库中的最新版本')
  expect(html).not.toContain('规则已不在规则库')
})
