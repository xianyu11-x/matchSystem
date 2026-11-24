import { beforeEach, expect, it, vi } from 'vitest'
import type { JsonObject } from '../types'

const mocked = vi.hoisted(() => ({
  states: [] as unknown[],
  stateIndex: 0,
  save: vi.fn(),
  getText: vi.fn(),
}))

vi.mock('react', async (importOriginal) => ({
  ...(await importOriginal<typeof import('react')>()),
  useRef: () => ({ current: null }),
  useState: (initial?: unknown) => {
    const index = mocked.stateIndex++
    if (index >= mocked.states.length) mocked.states.push(initial)
    return [mocked.states[index], (value: unknown) => (mocked.states[index] = value)]
  },
}))
vi.mock('../lib/api', () => ({
  api: { getScenarioJsonText: mocked.getText },
  formatApiError: (error: unknown, fallback: string) =>
    error instanceof Error ? error.message : fallback,
  isDemoMode: false,
}))
vi.mock('../lib/queries', () => ({
  useImportScenarioText: () => ({ isPending: false }),
}))
vi.mock('../lib/saveJsonFile', () => ({ saveJsonFile: mocked.save }))

import { ScenarioJsonActions } from './ScenarioJsonActions'

type Element = { type: unknown; props: Record<string, unknown> }
function elements(value: unknown): Element[] {
  if (Array.isArray(value)) return value.flatMap(elements)
  if (!value || typeof value !== 'object' || !('props' in value)) return []
  const element = value as Element
  return [element, ...elements(element.props.children)]
}
function render(scenario: JsonObject = { scenarioId: 'scene 1' }) {
  mocked.stateIndex = 0
  return elements(ScenarioJsonActions({ scenario }))
}
function button(nodes: Element[]) {
  return nodes.find((node) => node.type === 'button' && node.props.children === '导出场景')!
}

beforeEach(() => {
  mocked.states = []
  mocked.save.mockReset()
  mocked.getText.mockReset()
})

it('导出场景使用原始响应，显示实际结果并在新操作和取消时清除旧结果', async () => {
  const raw = '{"large":9223372036854775807}'
  mocked.getText.mockResolvedValue(raw)
  mocked.save.mockImplementationOnce(async (_fileName, getText) => {
    expect(await getText()).toBe(raw)
    return 'C:\\数据\\场景.json'
  })
  const exportClick = button(render()).props.onClick as () => void
  exportClick()
  exportClick()
  expect(mocked.save).toHaveBeenCalledOnce()
  expect(mocked.save).toHaveBeenCalledWith('scene-1.json', expect.any(Function))
  await vi.waitFor(() =>
    expect(render().find((node) => node.props.role === 'status')?.props.children).toContain(
      'C:\\数据\\场景.json',
    ),
  )

  let finish!: (result: string | null) => void
  mocked.save.mockImplementationOnce(() => new Promise((resolve) => (finish = resolve)))
  ;(button(render()).props.onClick as () => void)()
  const pending = render()
  expect(pending.find((node) => node.props.role === 'status')).toBeUndefined()
  expect(pending.find((node) => node.props.children === '导出中…')?.props.disabled).toBe(true)
  finish(null)
  await vi.waitFor(() => expect(button(render()).props.disabled).toBe(false))
  expect(render().find((node) => node.props.role === 'status')).toBeUndefined()
})

it('场景保存失败后显示错误并恢复操作', async () => {
  mocked.save.mockRejectedValueOnce(new Error('保存失败：权限不足'))
  ;(button(render()).props.onClick as () => void)()
  await vi.waitFor(() =>
    expect(render().find((node) => node.props.role === 'alert')?.props.children).toBe(
      '保存失败：权限不足',
    ),
  )
  expect(button(render()).props.disabled).toBe(false)
})
