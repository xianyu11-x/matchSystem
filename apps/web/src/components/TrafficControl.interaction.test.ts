import { beforeEach, expect, it, vi } from 'vitest'
import type { TrafficConfig } from '../lib/api'

const mocked = vi.hoisted(() => ({
  config: {} as TrafficConfig,
  request: vi.fn(),
  pending: undefined as Promise<unknown> | undefined,
  error: undefined as unknown,
}))

vi.mock('react', async (importOriginal) => ({
  ...(await importOriginal<typeof import('react')>()),
  useEffect: () => undefined,
}))
vi.mock('@tanstack/react-query', () => ({
  useQueryClient: () => ({ setQueryData: vi.fn(), invalidateQueries: vi.fn() }),
  useQuery: () => ({ data: { state: 'idle', config: mocked.config, nextTicketId: 1 } }),
  useMutation: ({
    mutationFn,
  }: {
    mutationFn: (method: 'POST' | 'DELETE') => Promise<unknown>
  }) => ({
    isPending: false,
    mutate: (method: 'POST' | 'DELETE') => {
      mocked.pending = mutationFn(method).catch((error: unknown) => {
        mocked.error = error
      })
    },
  }),
}))
vi.mock('../lib/api', () => ({
  trafficRequest: mocked.request,
  formatApiError: (error: unknown) => String(error),
}))

import { TrafficControl } from './TrafficControl'

type Element = { type: unknown; props: Record<string, unknown> }
function elements(value: unknown): Element[] {
  if (Array.isArray(value)) return value.flatMap(elements)
  if (!value || typeof value !== 'object' || !('props' in value)) return []
  const element = value as Element
  return [element, ...elements(element.props.children)]
}
function render() {
  return elements(
    TrafficControl({
      config: mocked.config,
      onConfigChange: (next) => {
        mocked.config = next
      },
      buildSpec: () => ({ count: 1, seed: 1, ruleKey: '1' }),
    }),
  )
}
function limitInput(nodes: Element[]) {
  const label = nodes.find(
    (node) =>
      node.type === 'label' &&
      Array.isArray(node.props.children) &&
      node.props.children[0] === '每次每个物理节点最多产出 Match（局）',
  )!
  return elements(label).find((node) => node.type === 'input')!
}
async function submit(nodes: Element[]) {
  const form = nodes.find((node) => node.type === 'form')!
  ;(form.props.onSubmit as (event: unknown) => void)({ preventDefault: () => {} })
  await mocked.pending
}

beforeEach(() => {
  mocked.config = {
    mode: 'realtime',
    distribution: 'constant',
    rate: 10,
    burstSize: 1,
    burstIntervalMs: 1000,
    matchIntervalMs: 1000,
    maxMatches: 10,
    seed: 42,
  }
  mocked.request.mockReset()
  mocked.request.mockResolvedValue({ state: 'running', config: mocked.config })
  mocked.pending = undefined
  mocked.error = undefined
})

it('允许显式输入零并向持续流量接口提交零', async () => {
  let nodes = render()
  let input = limitInput(nodes)
  expect(input.props.min).toBe(0)
  ;(input.props.onChange as (event: unknown) => void)({ target: { value: '0' } })
  nodes = render()
  input = limitInput(nodes)
  expect(input.props.value).toBe(0)
  expect(
    nodes.some(
      (node) => node.type === 'span' && node.props.children === '0 表示只注入，不进行匹配。',
    ),
  ).toBe(true)
  await submit(nodes)
  expect(mocked.request).toHaveBeenCalledWith(
    'POST',
    expect.objectContaining({ maxMatches: 0 }),
    expect.anything(),
  )
})

it('清空不会变成零，非法数值不会发出请求', async () => {
  let nodes = render()
  ;(limitInput(nodes).props.onChange as (event: unknown) => void)({ target: { value: '' } })
  nodes = render()
  expect(limitInput(nodes).props.value).toBe('')
  await submit(nodes)
  expect(mocked.error).toBeInstanceOf(Error)
  expect(mocked.request).not.toHaveBeenCalled()
  for (const value of ['-1', '0.5', '10001']) {
    ;(limitInput(nodes).props.onChange as (event: unknown) => void)({ target: { value } })
    nodes = render()
    await submit(nodes)
    expect(mocked.request).not.toHaveBeenCalled()
  }
})
