import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { demoRuleSummary } from '../lib/mockData'
import { ticketTargets } from '../lib/ticketTargets'
import { TicketTargetPicker } from './TicketTargetPicker'

it('默认按规则选择并展示路由说明', () => {
  const targets = ticketTargets([demoRuleSummary])
  const html = renderToStaticMarkup(
    createElement(TicketTargetPicker, {
      mode: 'rule',
      targets: targets.rule,
      selectedKey: undefined,
      disabled: false,
      onModeChange: () => undefined,
      onSelectionChange: () => undefined,
    }),
  )
  expect(html).toContain('指定规则')
  expect(html).toContain('指定节点')
  expect(html).toContain('目标规则')
  expect(html).toContain('由 Router 根据规则、亲和键和部署权重选择可用部署')
  expect(html).toContain('selected=""')
})

it('所选部署消失时保留失效提示并允许切换或重新选择', () => {
  const targets = ticketTargets([demoRuleSummary])
  const html = renderToStaticMarkup(
    createElement(TicketTargetPicker, {
      mode: 'node',
      targets: targets.node,
      selectedKey: '["999","1","1"]',
      disabled: false,
      onModeChange: () => undefined,
      onSelectionChange: () => undefined,
    }),
  )
  expect(html).toContain('目标节点与部署')
  expect(html).toContain('原目标已不存在，请重新选择')
  expect(html).toContain('所选目标已不存在，请重新选择或切换投递方式')
  expect(html).toContain('物理节点 1 · 部署 1 · 规则')
  expect(html).toContain('role="alert"')
})
