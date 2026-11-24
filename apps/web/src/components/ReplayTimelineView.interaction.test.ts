import { beforeEach, expect, it, vi } from 'vitest'
import type { ReplayTimelineItem } from './replayTimelineLayout'

const hooks = vi.hoisted(() => ({
  state: [] as unknown[],
  cursor: 0,
  refs: [] as { current: unknown }[],
  refCursor: 0,
  effects: [] as { deps: unknown[]; cleanup?: () => void }[],
  effectCursor: 0,
  pending: [] as { index: number; deps: unknown[]; run: () => void | (() => void) }[],
  effectsEnabled: false,
}))

vi.mock('react', async (importOriginal) => ({
  ...(await importOriginal<typeof import('react')>()),
  useMemo: (compute: () => unknown) => compute(),
  useRef: () => {
    const index = hooks.refCursor++
    if (!hooks.refs[index]) hooks.refs[index] = { current: null }
    return hooks.refs[index]
  },
  useEffect: (run: () => void | (() => void), deps: unknown[]) => {
    const index = hooks.effectCursor++
    const previous = hooks.effects[index]
    if (
      hooks.effectsEnabled &&
      (!previous || deps.some((value, dependency) => !Object.is(value, previous.deps[dependency])))
    ) {
      hooks.pending.push({ index, deps, run })
    }
  },
  useState: (initial: unknown) => {
    const index = hooks.cursor++
    if (!(index in hooks.state)) hooks.state[index] = initial
    return [
      hooks.state[index],
      (next: unknown) => {
        hooks.state[index] = typeof next === 'function' ? next(hooks.state[index]) : next
      },
    ]
  },
}))

import { ReplayTimelineView } from './ReplayTimelineView'

type Element = { type: unknown; props: Record<string, unknown> }
function elements(value: unknown): Element[] {
  if (Array.isArray(value)) return value.flatMap(elements)
  if (!value || typeof value !== 'object' || !('props' in value)) return []
  const element = value as Element
  return [element, ...elements(element.props.children)]
}

const items: ReplayTimelineItem[] = [
  { id: 'first', kind: 'ticket_add', offsetNs: '9007199254740993', label: '第一条' },
  { id: 'second', kind: 'ticket_add', offsetNs: '9007199254740993', label: '第二条' },
  { id: 'missing', kind: 'ticket_add', label: '无时点' },
  { id: 'last', kind: 'ticket_add', offsetNs: '9007199254741993', label: '最后一条' },
]

beforeEach(() => {
  hooks.state = []
  hooks.cursor = 0
  hooks.refs = []
  hooks.refCursor = 0
  hooks.effects = []
  hooks.effectCursor = 0
  hooks.pending = []
  hooks.effectsEnabled = false
})

it('聚合标记与逐条按钮能访问每条事件，包括缺失时间的数据', () => {
  const renderDetail = vi.fn((item: ReplayTimelineItem) => item.label)
  const onSelect = vi.fn()
  const render = (events = items) => {
    hooks.cursor = 0
    hooks.refCursor = 0
    hooks.effectCursor = 0
    return elements(ReplayTimelineView({ events, renderDetail, onSelect }))
  }
  let nodes = render()
  expect(renderDetail).not.toHaveBeenCalled()
  const marker = nodes.find((node) => node.props['data-event-count'] === 2)!
  expect(marker.props['aria-label']).toContain('2 条事件')
  ;(marker.props.onClick as () => void)()
  nodes = render()
  expect(onSelect).toHaveBeenLastCalledWith(items[0])
  expect(renderDetail).toHaveBeenLastCalledWith(items[0])
  const next = () => {
    ;(
      nodes.find((node) => node.type === 'button' && node.props.children === '下一条')!.props
        .onClick as () => void
    )()
    nodes = render()
  }
  next()
  expect(renderDetail).toHaveBeenLastCalledWith(items[1])
  next()
  expect(renderDetail).toHaveBeenLastCalledWith(items[2])
  next()
  expect(renderDetail).toHaveBeenLastCalledWith(items[3])
  expect(
    nodes.find((node) => node.type === 'button' && node.props.children === '下一条')!.props
      .disabled,
  ).toBe(true)

  renderDetail.mockClear()
  nodes = render([...items])
  expect(renderDetail).not.toHaveBeenCalled()
  expect(nodes.some((node) => node.props.children === '选择事件查看详情')).toBe(true)
})

it('可跳到任意条目，并对越界或非整数输入禁用跳转', () => {
  const onSelect = vi.fn()
  const render = () => {
    hooks.cursor = 0
    hooks.refCursor = 0
    hooks.effectCursor = 0
    return elements(ReplayTimelineView({ events: items, onSelect }))
  }
  let nodes = render()
  const input = () => nodes.find((node) => node.props['aria-label'] === '要跳转的事件条目位置')!
  const jump = () => nodes.find((node) => node.type === 'button' && node.props.children === '跳转')!
  for (const invalid of ['0', '5', '2.5', '']) {
    ;(input().props.onChange as (event: unknown) => void)({ target: { value: invalid } })
    nodes = render()
    expect(jump().props.disabled).toBe(true)
  }
  ;(input().props.onChange as (event: unknown) => void)({ target: { value: '3' } })
  nodes = render()
  expect(jump().props.disabled).toBe(false)
  ;(jump().props.onClick as () => void)()
  nodes = render()
  expect(onSelect).toHaveBeenLastCalledWith(items[2])
  expect(nodes.some((node) => node.props.children === '无时点')).toBe(true)
})

it('双端拖动缩小范围，拖动选区和抓手平移，并可恢复全部时间', () => {
  const timed: ReplayTimelineItem[] = [
    { id: 'a', kind: 'ticket_add', offsetNs: 0, label: '起点' },
    { id: 'b', kind: 'ticket_add', offsetNs: 500, label: '中点' },
    { id: 'c', kind: 'ticket_add', offsetNs: 1000, label: '终点' },
  ]
  const render = () => {
    hooks.cursor = 0
    hooks.refCursor = 0
    hooks.effectCursor = 0
    return elements(ReplayTimelineView({ events: timed }))
  }
  let nodes = render()
  const setPointerCapture = vi.fn()
  hooks.refs[1].current = { getBoundingClientRect: () => ({ width: 1000 }), setPointerCapture }
  const drag = (selector: string, delta: number, isClass = false) => {
    const target = nodes.find((node) =>
      isClass ? node.props.className === selector : node.props['aria-label'] === selector,
    )!
    const event = {
      button: 0,
      pointerId: 1,
      clientX: 100,
      currentTarget: { focus: vi.fn() },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    }
    ;(target.props.onPointerDown as (event: unknown) => void)(event)
    expect(event.preventDefault).toHaveBeenCalled()
    expect(event.currentTarget.focus).toHaveBeenCalledWith({ preventScroll: true })
    const rail = nodes.find((node) => node.props.className === 'replay-timeline-overview-rail')!
    ;(rail.props.onPointerMove as (event: unknown) => void)({ ...event, clientX: 100 + delta })
    ;(rail.props.onPointerUp as (event: unknown) => void)(event)
    nodes = render()
  }
  const value = (label: string) =>
    nodes.find((node) => node.props['aria-label'] === label)!.props['aria-valuetext']
  drag('时间范围起点', 200)
  drag('时间范围终点', -200)
  expect(value('时间范围起点')).toBe('+0.0002 ms')
  expect(value('时间范围终点')).toBe('+0.0008 ms')
  expect(nodes.some((node) => String(node.props['aria-label']).startsWith('起点，'))).toBe(false)
  expect(nodes.some((node) => String(node.props['aria-label']).startsWith('中点，'))).toBe(true)
  drag('平移时间范围', 100)
  expect(value('时间范围起点')).toBe('+0.0003 ms')
  expect(value('时间范围终点')).toBe('+0.0009 ms')
  drag('replay-timeline-overview-grip', -300, true)
  expect(value('时间范围起点')).toBe('+0 ms')
  expect(value('时间范围终点')).toBe('+0.0006 ms')
  expect(setPointerCapture).toHaveBeenCalledTimes(4)
  ;(
    nodes.find((node) => node.type === 'button' && node.props.children === '显示全部时间')!.props
      .onClick as () => void
  )()
  nodes = render()
  expect(value('时间范围起点')).toBe('+0 ms')
  expect(value('时间范围终点')).toBe('+0.001 ms')
})

it('空时间段保持泳道及预览，键盘可操作重合的手柄及选区', () => {
  const timed: ReplayTimelineItem[] = [
    { id: 'a', kind: 'ticket_add', offsetNs: 0, label: '起点' },
    { id: 'b', kind: 'round_begin', offsetNs: 1000, label: '终点' },
  ]
  const render = () => {
    hooks.cursor = 0
    hooks.refCursor = 0
    hooks.effectCursor = 0
    return elements(ReplayTimelineView({ events: timed }))
  }
  let nodes = render()
  const press = (label: string, key: string) => {
    const node = nodes.find((item) => item.props['aria-label'] === label)!
    const event = { key, shiftKey: false, preventDefault: vi.fn() }
    ;(node.props.onKeyDown as (event: unknown) => void)(event)
    expect(event.preventDefault).toHaveBeenCalled()
    nodes = render()
  }
  press('时间范围起点', 'End')
  expect(
    nodes.find((node) => node.props['aria-label'] === '时间范围起点')!.props['aria-valuetext'],
  ).toBe('+0.001 ms')
  press('时间范围终点', 'ArrowLeft')
  expect(
    nodes.find((node) => node.props['aria-label'] === '时间范围终点')!.props['aria-valuetext'],
  ).toBe('+0.001 ms')
  press('时间范围起点', 'ArrowLeft')
  expect(
    nodes.find((node) => node.props['aria-label'] === '时间范围起点')!.props['aria-valuetext'],
  ).toBe('+0.000999 ms')
  press('平移时间范围', 'Home')
  expect(
    nodes.find((node) => node.props['aria-label'] === '时间范围终点')!.props['aria-valuetext'],
  ).toBe('+0.000001 ms')
  press('平移时间范围', 'ArrowRight')
  expect(nodes.filter((node) => node.props['data-event-count'] !== undefined)).toHaveLength(0)
  expect(nodes.some((node) => node.props.className === 'replay-timeline-density')).toBe(true)
  expect(nodes.filter((node) => node.props.className === 'replay-timeline-lane')).toHaveLength(2)
  expect(
    nodes.filter((node) => node.props.className === 'replay-timeline-tick').length,
  ).toBeGreaterThan(0)
})

it('拖动中忽略其他指针，取消捕获后不再修改范围', () => {
  const render = () => {
    hooks.cursor = 0
    hooks.refCursor = 0
    hooks.effectCursor = 0
    return elements(ReplayTimelineView({ events: items }))
  }
  let nodes = render()
  const setPointerCapture = vi.fn()
  hooks.refs[1].current = { getBoundingClientRect: () => ({ width: 1000 }), setPointerCapture }
  const pointer = (pointerId: number, clientX: number) => ({
    button: 0,
    pointerId,
    clientX,
    currentTarget: { focus: vi.fn() },
    preventDefault: vi.fn(),
    stopPropagation: vi.fn(),
  })
  const start = nodes.find((node) => node.props['aria-label'] === '时间范围起点')!
  const end = nodes.find((node) => node.props['aria-label'] === '时间范围终点')!
  const rail = nodes.find((node) => node.props.className === 'replay-timeline-overview-rail')!
  ;(start.props.onPointerDown as (event: unknown) => void)(pointer(1, 0))
  ;(end.props.onPointerDown as (event: unknown) => void)(pointer(2, 0))
  ;(rail.props.onPointerMove as (event: unknown) => void)(pointer(2, -500))
  nodes = render()
  expect(
    nodes.find((node) => node.props['aria-label'] === '时间范围终点')!.props['aria-valuetext'],
  ).toBe('+9007199254.741993 ms')
  ;(rail.props.onPointerMove as (event: unknown) => void)(pointer(1, 100))
  ;(rail.props.onPointerCancel as (event: unknown) => void)(pointer(1, 100))
  ;(rail.props.onPointerMove as (event: unknown) => void)(pointer(1, 300))
  nodes = render()
  expect(
    nodes.find((node) => node.props['aria-label'] === '时间范围起点')!.props['aria-valuetext'],
  ).toBe('+9007199254.741093 ms')
  expect(setPointerCapture).toHaveBeenCalledTimes(1)
})

it('跳转到范围外事件会保持跨度并将事件移入范围，无时点事件不改变范围', () => {
  const timed: ReplayTimelineItem[] = [
    { id: 'a', kind: 'ticket_add', offsetNs: 0, label: '起点' },
    { id: 'b', kind: 'ticket_add', offsetNs: 500, label: '中点' },
    { id: 'missing', kind: 'ticket_add', label: '无时点' },
    { id: 'c', kind: 'ticket_add', offsetNs: 1000, label: '终点' },
  ]
  const render = () => {
    hooks.cursor = 0
    hooks.refCursor = 0
    hooks.effectCursor = 0
    return elements(ReplayTimelineView({ events: timed }))
  }
  let nodes = render()
  const press = (label: string, key: string) => {
    ;(
      nodes.find((node) => node.props['aria-label'] === label)!.props.onKeyDown as (
        event: unknown,
      ) => void
    )({ key, shiftKey: false, preventDefault: vi.fn() })
    nodes = render()
  }
  press('时间范围终点', 'Home')
  press('时间范围终点', 'ArrowRight')
  const jump = (index: string) => {
    ;(
      nodes.find((node) => node.props['aria-label'] === '要跳转的事件条目位置')!.props.onChange as (
        event: unknown,
      ) => void
    )({ target: { value: index } })
    nodes = render()
    ;(
      nodes.find((node) => node.type === 'button' && node.props.children === '跳转')!.props
        .onClick as () => void
    )()
    nodes = render()
  }
  jump('4')
  expect(
    nodes.find((node) => node.props['aria-label'] === '时间范围起点')!.props['aria-valuetext'],
  ).toBe('+0.000999 ms')
  expect(
    nodes.find((node) => node.props['aria-label'] === '时间范围终点')!.props['aria-valuetext'],
  ).toBe('+0.001 ms')
  expect(nodes.some((node) => String(node.props['aria-label']).startsWith('终点，'))).toBe(true)
  jump('3')
  expect(
    nodes.find((node) => node.props['aria-label'] === '时间范围起点')!.props['aria-valuetext'],
  ).toBe('+0.000999 ms')
  jump('1')
  expect(
    nodes.find((node) => node.props['aria-label'] === '时间范围起点')!.props['aria-valuetext'],
  ).toBe('+0 ms')
  expect(
    nodes.find((node) => node.props['aria-label'] === '时间范围终点')!.props['aria-valuetext'],
  ).toBe('+0.000001 ms')
})

it('空或无效数据变为有时间数据后重新监听并测量容器宽度', () => {
  const observers: { observe: ReturnType<typeof vi.fn>; disconnect: ReturnType<typeof vi.fn> }[] =
    []
  class Observer {
    observe = vi.fn()
    disconnect = vi.fn()
    constructor() {
      observers.push(this)
    }
  }
  vi.stubGlobal('ResizeObserver', Observer)
  hooks.effectsEnabled = true
  const element = { clientWidth: 900, scrollLeft: 0 }
  const invalid: ReplayTimelineItem[] = [{ id: 'invalid', kind: 'ticket_add', label: '无时点' }]
  const empty: ReplayTimelineItem[] = []
  const render = (events: ReplayTimelineItem[]) => {
    hooks.cursor = 0
    hooks.refCursor = 0
    hooks.effectCursor = 0
    hooks.pending = []
    const nodes = elements(ReplayTimelineView({ events }))
    hooks.refs[0].current = nodes.some((node) => node.props.className === 'replay-timeline-chart')
      ? element
      : null
    for (const effect of hooks.pending) {
      hooks.effects[effect.index]?.cleanup?.()
      const cleanup = effect.run()
      hooks.effects[effect.index] = { deps: effect.deps, cleanup: cleanup || undefined }
    }
    return nodes
  }
  const measuredWidth = () => {
    const nodes = render(items)
    const scroller = nodes.find((node) => node.props.className === 'replay-timeline-chart')!
    const content = elements(scroller.props.children).find((node) => node.props.style)!
    return (content.props.style as { width: number }).width
  }
  try {
    render(empty)
    expect(observers).toHaveLength(0)
    render(items)
    expect(observers).toHaveLength(1)
    expect(observers[0].observe).toHaveBeenCalledWith(element)
    expect(measuredWidth()).toBe(898)
    render(invalid)
    expect(observers[0].disconnect).toHaveBeenCalled()
    render(items)
    expect(observers).toHaveLength(2)
    expect(measuredWidth()).toBe(898)
    render(empty)
    expect(observers[1].disconnect).toHaveBeenCalled()
    render(items)
    expect(observers).toHaveLength(3)
    expect(measuredWidth()).toBe(898)
  } finally {
    vi.unstubAllGlobals()
  }
})
