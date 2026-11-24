import { beforeEach, expect, it, vi } from 'vitest'
import type { RuleDocument } from '../types'

const mocked = vi.hoisted(() => ({
  states: [] as unknown[],
  stateIndex: 0,
  store: {} as Record<string, unknown>,
  workspace: {} as Record<string, unknown>,
  save: vi.fn(),
}))

vi.mock('react', async (importOriginal) => ({
  ...(await importOriginal<typeof import('react')>()),
  useRef: () => ({ current: null }),
  useEffect: () => undefined,
  useMemo: (factory: () => unknown) => factory(),
  useState: (initial?: unknown) => {
    const index = mocked.stateIndex++
    if (index >= mocked.states.length) mocked.states.push(initial)
    return [mocked.states[index], (value: unknown) => (mocked.states[index] = value)]
  },
}))
vi.mock('../lib/queries', () => ({
  useRuleLibrary: () => ({ isLoading: false, isError: false }),
  useCapabilities: () => ({ isError: false }),
  useReplaceRuleLibrary: () => ({ isPending: false }),
  useValidateRule: () => ({ isPending: false }),
}))
vi.mock('../lib/ruleStore', () => ({
  useRuleStore: Object.assign((selector: (state: unknown) => unknown) => selector(mocked.store), {
    getState: () => mocked.store,
  }),
}))
vi.mock('../lib/ruleLibrary', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/ruleLibrary')>()),
  useLibraryWorkspace: Object.assign(
    (selector: (state: unknown) => unknown) => selector(mocked.workspace),
    { getState: () => mocked.workspace },
  ),
}))
vi.mock('../lib/saveJsonFile', () => ({ saveJsonFile: mocked.save }))

import { demoRule } from '../lib/mockData'
import { Rules } from './Rules'

type Element = { type: unknown; props: Record<string, unknown> }
function elements(value: unknown): Element[] {
  if (Array.isArray(value)) return value.flatMap(elements)
  if (!value || typeof value !== 'object' || !('props' in value)) return []
  const element = value as Element
  return [element, ...elements(element.props.children), ...elements(element.props.actions)]
}
function render() {
  mocked.stateIndex = 0
  return elements(Rules())
}
function button(nodes: Element[], label: string) {
  return nodes.find((node) => node.type === 'button' && node.props.children === label)!
}

beforeEach(() => {
  const current = structuredClone(demoRule)
  mocked.states = []
  mocked.save.mockReset()
  mocked.store = { document: current, dirty: true, activeTab: 'settings' }
  mocked.workspace = {
    rules: [current],
    selected: 0,
    revision: '1',
    dirty: true,
    updateCurrent: vi.fn(),
  }
})

it('单条规则导出使用当前草稿、便携格式与原文件名，并显示实际保存位置', async () => {
  let finish!: (result: string | null) => void
  mocked.save.mockImplementationOnce(() => new Promise((resolve) => (finish = resolve)))
  const exportClick = button(render(), '导出规则').props.onClick as () => void
  exportClick()
  exportClick()
  expect(mocked.save).toHaveBeenCalledOnce()
  expect(mocked.workspace.updateCurrent).toHaveBeenCalledWith(mocked.store.document, true)
  expect(mocked.save).toHaveBeenCalledWith('matchscope-1-1.json', expect.any(Function))
  const getText = mocked.save.mock.calls[0][1] as () => string
  const exported = JSON.parse(getText())
  expect(exported.schemaVersion).toBe('match-rule/v1')
  expect(exported.ruleKey).toEqual({ namespace: '1', ruleId: '1' })
  expect(exported).not.toHaveProperty('graph')
  expect(exported).not.toHaveProperty('placementId')
  const pending = render()
  expect(button(pending, '导出中…').props.disabled).toBe(true)
  expect(button(pending, '导出规则库').props.disabled).toBe(true)
  finish('C:\\导出\\rule.json')
  await vi.waitFor(() =>
    expect(render().find((node) => node.props.role === 'status')?.props.children).toContain(
      'C:\\导出\\rule.json',
    ),
  )
  expect(button(render(), '导出规则').props.disabled).toBe(false)
})

it('整库导出使用便携规则库格式，取消后清除先前结果', async () => {
  mocked.save.mockResolvedValueOnce('previous.json')
  ;(button(render(), '导出规则').props.onClick as () => void)()
  await vi.waitFor(() =>
    expect(render().find((node) => node.props.role === 'status')).toBeDefined(),
  )
  mocked.save.mockResolvedValueOnce(null)
  ;(button(render(), '导出规则库').props.onClick as () => void)()
  expect(render().find((node) => node.props.role === 'status')).toBeUndefined()
  expect(mocked.save).toHaveBeenLastCalledWith('match-rule-library.json', expect.any(Function))
  const getText = mocked.save.mock.calls[1][1] as () => string
  expect(JSON.parse(getText())).toMatchObject({
    schemaVersion: 'match-rule-library/v1',
    rules: [{ schemaVersion: 'match-rule/v1' }],
  })
  await vi.waitFor(() => expect(button(render(), '导出规则库').props.disabled).toBe(false))
  expect(render().find((node) => node.props.role === 'status')).toBeUndefined()
})

it('无效或不安全的规则数值在打开选择器前报错', () => {
  const current = mocked.store.document as RuleDocument
  current.runtime.maxPlayers = Number.POSITIVE_INFINITY
  ;(button(render(), '导出规则').props.onClick as () => void)()
  expect(mocked.save).not.toHaveBeenCalled()
  expect(render().find((node) => node.props.role === 'alert')?.props.children).toContain(
    '无法精确表示的数值',
  )
  expect(button(render(), '导出规则').props.disabled).toBe(false)
})
