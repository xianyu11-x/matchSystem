import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { Topology } from '../types'

const mocked = vi.hoisted(() => ({
  states: [] as unknown[],
  stateIndex: 0,
  demo: false,
  pending: false,
  clear: vi.fn(),
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
  useRef: () => ({ current: null }),
  useEffect: () => undefined,
}))

vi.mock('../lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/api')>()),
  get isDemoMode() {
    return mocked.demo
  },
}))

vi.mock('../lib/queries', () => ({
  useRuleLibrary: () => ({}),
  useAddPhysicalNode: () => ({ isPending: false }),
  useUpdatePhysicalNode: () => ({ isPending: false }),
  useDeletePhysicalNode: () => ({ isPending: false }),
  useClearPhysicalNodeTickets: () => ({
    isPending: mocked.pending,
    variables: '2',
    mutateAsync: mocked.clear,
  }),
}))

import { PhysicalNodeManager } from './PhysicalNodeManager'

const logical = {
  id: 'logical-2',
  name: 'p2',
  ruleKey: '1',
  placementId: '1',
  ticketCount: 3,
  state: 'healthy' as const,
}
const topology: Topology = {
  updatedAt: '',
  nodes: [logical],
  physicalNodes: [
    { id: '1', endpoint: 'inproc://empty', enabled: true, logicalNodes: [] },
    { id: '2', endpoint: 'inproc://busy', enabled: true, logicalNodes: [logical] },
  ],
  routes: [],
}

type Element = { props: Record<string, unknown> }

function elements(value: unknown): Element[] {
  if (Array.isArray(value)) return value.flatMap(elements)
  if (!value || typeof value !== 'object' || !('props' in value)) return []
  const element = value as Element
  return [element, ...elements(element.props.children)]
}

function render(current = topology) {
  mocked.stateIndex = 0
  return elements(PhysicalNodeManager({ topology: current }))
}

function button(nodes: Element[], id: string) {
  const found = nodes.find(
    (node) => node.props['aria-label'] === `清空物理节点 ${id} 的等待 Ticket`,
  )
  if (!found) throw new Error(`missing clear button for ${id}`)
  return found
}

beforeEach(() => {
  mocked.states = []
  mocked.demo = false
  mocked.pending = false
  mocked.clear.mockReset()
})

afterEach(() => vi.unstubAllGlobals())

it('enables clearing only occupied nodes while disabling demo and pending actions', () => {
  expect(button(render(), '1').props.disabled).toBe(true)
  expect(button(render(), '2').props.disabled).toBe(false)
  mocked.demo = true
  expect(button(render(), '2').props.disabled).toBe(true)
  mocked.demo = false
  mocked.pending = true
  expect(button(render(), '2').props.disabled).toBe(true)
  expect(button(render(), '2').props.children).toBe('清空中…')
})

it('confirms the selected node, respects cancellation and displays the actual cleared count', async () => {
  const confirm = vi.fn().mockReturnValueOnce(false).mockReturnValueOnce(true)
  vi.stubGlobal('window', { confirm })
  mocked.clear.mockResolvedValue({ removedTickets: 2 })

  ;(button(render(), '2').props.onClick as () => void)()
  expect(confirm).toHaveBeenCalledWith(expect.stringContaining('物理节点 2'))
  expect(confirm).toHaveBeenCalledWith(expect.stringContaining('持续流量可能再次注入'))
  expect(mocked.clear).not.toHaveBeenCalled()

  ;(button(render(), '2').props.onClick as () => void)()
  await vi.waitFor(() => expect(mocked.clear).toHaveBeenCalledWith('2'))
  await vi.waitFor(() => {
    const status = render().find((node) => node.props.role === 'status')
    expect(status?.props.children).toBe('物理节点 2 已清空 2 个等待 Ticket。')
  })
})

it('shows an error even if the refreshed topology no longer contains the node', async () => {
  vi.stubGlobal('window', { confirm: () => true })
  mocked.clear.mockRejectedValue(new Error('节点不存在'))
  ;(button(render(), '2').props.onClick as () => void)()
  await vi.waitFor(() => {
    const alert = render({ ...topology, physicalNodes: [topology.physicalNodes[0]] }).find(
      (node) => node.props.role === 'alert',
    )
    expect(alert?.props.children).toBe('节点不存在')
  })
})
