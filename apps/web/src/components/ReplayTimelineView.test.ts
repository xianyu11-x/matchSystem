import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { ReplayTimelineView, type ReplayTimelineItem } from './ReplayTimelineView'
import {
  layoutReplayTimeline,
  prepareReplayTimeline,
  replayTimelineTicks,
  replayTimelineX,
  TIMELINE_PADDING,
  timelineOffset,
} from './replayTimelineLayout'

function event(index: number, offsetNs: ReplayTimelineItem['offsetNs']): ReplayTimelineItem {
  return { id: String(index), offsetNs, kind: 'ticket_add', label: `票据 ${index}` }
}

describe('时间轴的精确时间与布局', () => {
  it('先相减再映射大整数，极值范围和相近时点都保持正确比例', () => {
    const first = 9_223_372_036_854_770_000n
    const last = first + 10_000n
    const width = 1128
    expect(replayTimelineX(first, first, last, width)).toBe(TIMELINE_PADDING)
    expect(replayTimelineX(first + 2500n, first, last, width)).toBe(TIMELINE_PADDING + 250)
    expect(replayTimelineX(last, first, last, width)).toBe(width - TIMELINE_PADDING)
    const negative = -9_223_372_036_854_775_808n
    const positive = 9_223_372_036_854_775_807n
    expect(replayTimelineX(0n, negative, positive, width)).toBeCloseTo(width / 2, 2)
    expect(replayTimelineX(first, first, first, width)).toBe(width / 2)
  })

  it('跳过无效偏移和已经丢失精度的 Number，但保留精确整数字符串', () => {
    expect(timelineOffset(undefined)).toBeUndefined()
    expect(timelineOffset(null as unknown as number)).toBeUndefined()
    expect(timelineOffset('1.2')).toBeUndefined()
    expect(timelineOffset('1e3')).toBeUndefined()
    expect(timelineOffset(Number.MAX_SAFE_INTEGER + 1)).toBeUndefined()
    expect(timelineOffset('-9223372036854775808')).toBe(-9223372036854775808n)
    const prepared = prepareReplayTimeline([
      event(1, '9223372036854775807'),
      event(2, undefined),
      event(3, -1),
    ])
    expect(prepared.minimum).toBe(-1n)
    expect(prepared.maximum).toBe(9223372036854775807n)
    expect([...prepared.byIndex.keys()]).toEqual([0, 2])
  })

  it('使用易读刻度与适合尺度的单位，支持负值和大整数', () => {
    expect(replayTimelineTicks(0n, 62_899_000n, 608).map((tick) => tick.label)).toEqual([
      '+0 ms',
      '+20 ms',
      '+40 ms',
      '+60 ms',
    ])
    expect(replayTimelineTicks(-5200n, 5200n, 608).map((tick) => tick.label)).toEqual([
      '-4 μs',
      '-2 μs',
      '+0 μs',
      '+2 μs',
      '+4 μs',
    ])
    expect(
      replayTimelineTicks(9007199254740993n, 9007199254740996n, 608).map((tick) => tick.label),
    ).toEqual([
      '+9007199254740993 ns',
      '+9007199254740994 ns',
      '+9007199254740995 ns',
      '+9007199254740996 ns',
    ])
  })

  it('密集点按时间和泳道聚合，缩小时间窗口后拆分且保留可见事件索引', () => {
    const events = Array.from({ length: 1000 }, (_, index) => event(index, index))
    events.push({ id: 'round', kind: 'round_begin', offsetNs: 500, label: '轮次开始' })
    const prepared = prepareReplayTimeline(events)
    const overview = layoutReplayTimeline(prepared, 560)
    const zoomed = layoutReplayTimeline(prepared, 560, { minimum: 480n, maximum: 520n })
    expect(overview.groupByIndex.get(480)).toBe(overview.groupByIndex.get(520))
    expect(zoomed.groupByIndex.get(480)).not.toBe(zoomed.groupByIndex.get(520))
    expect(overview.groupsByLane.get('round_begin')).toHaveLength(1)
    expect(overview.groupByIndex.size).toBe(events.length)
    expect(zoomed.groupByIndex.size).toBe(42)
    const xs = zoomed.groupsByLane.get('ticket_add')!.map((group) => group.x)
    expect(xs.every((x, index) => index === 0 || x - xs[index - 1] >= 132)).toBe(true)
    expect(xs[0]).toBe(TIMELINE_PADDING)
  })

  it('大批同时刻事件只创建一个聚合标记，并保留全部事件', () => {
    const events = Array.from({ length: 20_000 }, (_, index) => event(index, '42'))
    const prepared = prepareReplayTimeline(events)
    const layout = layoutReplayTimeline(prepared, 560)
    const groups = layout.groupsByLane.get('ticket_add')!
    expect(groups).toHaveLength(1)
    expect(groups[0].indices).toHaveLength(events.length)
    expect(layout.groupByIndex.get(events.length - 1)).toBe(groups[0].key)
    expect(groups[0].x).toBe(280)
  })

  it('显示空、单点和无效偏移的状态，详情仅在用户选择后显示', () => {
    const empty = renderToStaticMarkup(createElement(ReplayTimelineView, { events: [] }))
    expect(empty).toContain('没有事件')
    const events = [event(1, '9007199254740993'), event(2, undefined)]
    const html = renderToStaticMarkup(createElement(ReplayTimelineView, { events }))
    expect(html).toContain('+9007199254.740993 ms')
    expect(html).toContain('1 条事件没有可精确使用的时间偏移')
    expect(html).toContain('票据加入')
    expect(html).toContain('票据 1')
    expect(html).toContain('选择事件查看详情')
    expect(html).toContain('要跳转的事件条目位置')
    expect(html).not.toContain('NaN')
  })
})
