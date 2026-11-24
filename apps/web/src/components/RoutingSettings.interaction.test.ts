import { beforeEach, expect, it, vi } from 'vitest'
import type { JsonObject } from '../types'

const mocked = vi.hoisted(() => ({
  states: [] as unknown[],
  stateIndex: 0,
  demo: false,
  pending: false,
  update: vi.fn(),
}))

vi.mock('react', async (importOriginal) => ({
  ...(await importOriginal<typeof import('react')>()),
  useState: (initial?: unknown) => {
    const index = mocked.stateIndex++
    if (index >= mocked.states.length) mocked.states.push(initial)
    return [
      mocked.states[index],
      (value: unknown) => {
        mocked.states[index] = value
      },
    ]
  },
  useEffect: () => undefined,
}))

vi.mock('../lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/api')>()),
  get isDemoMode() {
    return mocked.demo
  },
}))

vi.mock('../lib/queries', () => ({
  useUpdateRouting: () => ({ isPending: mocked.pending, mutateAsync: mocked.update }),
}))

import { RoutingSettings } from './RoutingSettings'

type Element = { type: unknown; props: Record<string, unknown> }
function elements(value: unknown): Element[] {
  if (Array.isArray(value)) return value.flatMap(elements)
  if (!value || typeof value !== 'object' || !('props' in value)) return []
  const element = value as Element
  return [element, ...elements(element.props.children)]
}
function render(scenario: JsonObject = {}) {
  mocked.stateIndex = 0
  return elements(RoutingSettings({ scenario }))
}

beforeEach(() => {
  mocked.states = []
  mocked.demo = false
  mocked.pending = false
  mocked.update.mockReset()
})

it('权重优先显示默认阈值，应用策略后调用独立接口', async () => {
  let nodes = render()
  expect(nodes.find((node) => node.type === 'input')).toBeUndefined()
  const select = nodes.find((node) => node.type === 'select')!
  ;(select.props.onChange as (event: unknown) => void)({ target: { value: 'weighted_priority' } })
  nodes = render()
  const threshold = nodes.find((node) => node.type === 'input')!
  expect(threshold.props.value).toBe('50')
  ;(threshold.props.onChange as (event: unknown) => void)({ target: { value: '3' } })
  mocked.update.mockResolvedValue({ algorithm: 'weighted_priority', ticketLimit: 3 })
  const form = render().find((node) => node.type === 'form')!
  ;(form.props.onSubmit as (event: unknown) => void)({ preventDefault: () => undefined })
  await vi.waitFor(() =>
    expect(mocked.update).toHaveBeenCalledWith({ algorithm: 'weighted_priority', ticketLimit: 3 }),
  )
  await vi.waitFor(() => {
    expect(render().find((node) => node.props.role === 'status')?.props.children).toContain(
      '已应用',
    )
  })
})

it('阈值为零时显示错误且不发出请求', async () => {
  const select = render().find((node) => node.type === 'select')!
  ;(select.props.onChange as (event: unknown) => void)({ target: { value: 'weighted_priority' } })
  const threshold = render().find((node) => node.type === 'input')!
  ;(threshold.props.onChange as (event: unknown) => void)({ target: { value: '0' } })
  const form = render().find((node) => node.type === 'form')!
  ;(form.props.onSubmit as (event: unknown) => void)({ preventDefault: () => undefined })
  await vi.waitFor(() => {
    expect(mocked.update).not.toHaveBeenCalled()
    const alert = render().find((node) => node.props.role === 'alert')!
    expect(alert.props.children).toContain('队列阈值')
  })
})

it('服务器拒绝保存时显示错误并保留编辑值', async () => {
  const select = render().find((node) => node.type === 'select')!
  ;(select.props.onChange as (event: unknown) => void)({
    target: { value: 'queue_weighted_random' },
  })
  mocked.update.mockRejectedValue(new Error('路由更新失败'))
  const form = render().find((node) => node.type === 'form')!
  ;(form.props.onSubmit as (event: unknown) => void)({ preventDefault: () => undefined })
  await vi.waitFor(() => {
    const nodes = render()
    expect(nodes.find((node) => node.props.role === 'alert')?.props.children).toBe('路由更新失败')
    expect(nodes.find((node) => node.type === 'select')?.props.value).toBe('queue_weighted_random')
  })
})

it('编辑期间配置变化时禁止应用并可恢复当前值', () => {
  const select = render().find((node) => node.type === 'select')!
  ;(select.props.onChange as (event: unknown) => void)({ target: { value: 'weighted_priority' } })
  const current = { routing: { algorithm: 'queue_weighted_random' } }
  const staleNodes = render(current)
  const apply = staleNodes.find((node) => node.type === 'button' && node.props.type === 'submit')!
  expect(apply.props.disabled).toBe(true)
  expect(staleNodes.find((node) => node.props.role === 'alert')?.props.children).toContain(
    '路由配置已变化',
  )
  const restore = staleNodes.find(
    (node) => node.type === 'button' && node.props.children === '恢复当前值',
  )!
  ;(restore.props.onClick as () => void)()
  const restored = render(current)
  expect(restored.find((node) => node.type === 'select')?.props.value).toBe('queue_weighted_random')
  expect(restored.find((node) => node.type === 'input')).toBeUndefined()
  expect(restored.find((node) => node.props.role === 'alert')).toBeUndefined()
})

it('演示模式和请求进行中禁用路由设置', () => {
  mocked.demo = true
  expect(render().find((node) => node.type === 'fieldset')?.props.disabled).toBe(true)
  mocked.demo = false
  mocked.pending = true
  const nodes = render()
  expect(nodes.find((node) => node.type === 'fieldset')?.props.disabled).toBe(true)
  expect(
    nodes.find((node) => node.type === 'button' && node.props.type === 'submit')?.props.children,
  ).toBe('应用中…')
})
