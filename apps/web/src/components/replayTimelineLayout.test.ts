import { describe, expect, it } from 'vitest'
import {
  layoutReplayTimeline,
  moveReplayTimelineRange,
  prepareReplayTimeline,
  replayTimelineHistogram,
  replayTimelineOffsetAtPosition,
  replayTimelinePosition,
  replayTimelineX,
  resizeReplayTimelineRange,
  timelineOffset,
  type ReplayTimelineItem,
} from './replayTimelineLayout'

function event(index: number, offsetNs: ReplayTimelineItem['offsetNs']): ReplayTimelineItem {
  return { id: String(index), offsetNs, kind: 'ticket_add', label: `事件 ${index}` }
}

describe('时间轴的时间窗口', () => {
  it('对无序事件按闭区间筛选，保留同刻事件与原始索引', () => {
    const base = 9_223_372_036_854_770_000n
    const events = [
      event(0, String(base + 2n)),
      event(1, String(base + 4n)),
      event(2, String(base + 1n)),
      event(3, String(base + 1n)),
      event(4, String(base)),
      event(5, undefined),
      { ...event(6, String(base + 2n)), kind: 'round_begin' },
    ]
    const prepared = prepareReplayTimeline(events)
    const layout = layoutReplayTimeline(prepared, 160, { minimum: base + 1n, maximum: base + 2n })
    expect([...layout.groupByIndex.keys()].sort((a, b) => a - b)).toEqual([0, 2, 3, 6])
    const ticketGroup = layout.groupsByLane.get('ticket_add')![0]
    expect(ticketGroup.firstIndex).toBe(0)
    expect(ticketGroup.minimum).toBe(base + 1n)
    expect(ticketGroup.maximum).toBe(base + 2n)
    expect(ticketGroup.indices.slice().sort((a, b) => a - b)).toEqual([0, 2, 3])
    expect(layout.groupsByLane.get('round_begin')![0].indices).toEqual([6])

    const point = layoutReplayTimeline(prepared, 560, { minimum: base + 1n, maximum: base + 1n })
    expect([...point.groupByIndex.keys()]).toEqual([2, 3])
    expect(point.groupsByLane.get('ticket_add')![0].x).toBe(280)
    expect(
      layoutReplayTimeline(prepared, 560, { minimum: base + 5n, maximum: base + 8n }).groupByIndex
        .size,
    ).toBe(0)
    expect(
      layoutReplayTimeline(prepared, 560, { minimum: base + 2n, maximum: base + 1n }).groupByIndex
        .size,
    ).toBe(0)
  })

  it('大量事件中只绘制窗口内的事件，闭区间包含两个端点', () => {
    const prepared = prepareReplayTimeline(
      Array.from({ length: 50_000 }, (_, index) => event(index, index)),
    )
    const layout = layoutReplayTimeline(prepared, 560, { minimum: 25_000n, maximum: 25_006n })
    expect([...layout.groupByIndex.keys()]).toEqual([
      25_000, 25_001, 25_002, 25_003, 25_004, 25_005, 25_006,
    ])
  })

  it('密度桶覆盖完整时间域，端点、空桶、单点和无效时间计数正确', () => {
    const prepared = prepareReplayTimeline(
      [100, 0, 50, 100, 25, 1, undefined].map((offset, index) => event(index, offset)),
    )
    expect(replayTimelineHistogram(prepared, 4)).toEqual([2, 1, 1, 2])
    const narrow = prepareReplayTimeline([0, 1, 2].map((offset, index) => event(index, offset)))
    expect(replayTimelineHistogram(narrow, 4)).toEqual([1, 0, 1, 1])
    const point = prepareReplayTimeline([
      event(0, '9007199254740993'),
      event(1, '9007199254740993'),
    ])
    expect(replayTimelineHistogram(point, 4)).toEqual([0, 0, 2, 0])
    expect(replayTimelineHistogram(prepareReplayTimeline([]), 3)).toEqual([0, 0, 0])
    expect(replayTimelineHistogram(prepared, 0)).toEqual([])
  })

  it('窄窗口和超过 Number 安全范围的时间仍可精确转换', () => {
    const origin = 10n ** 60n
    const range = { minimum: origin, maximum: origin + 100n }
    expect(timelineOffset(origin)).toBe(origin)
    expect(replayTimelineOffsetAtPosition(0, range)).toBe(range.minimum)
    expect(replayTimelineOffsetAtPosition(1, range)).toBe(range.maximum)
    expect(replayTimelineOffsetAtPosition(0.27, range)).toBe(origin + 27n)
    expect(replayTimelinePosition(origin + 27n, range)).toBeCloseTo(0.27, 14)
    expect(replayTimelineOffsetAtPosition(-1, range)).toBe(range.minimum)
    expect(replayTimelineOffsetAtPosition(Infinity, range)).toBe(range.maximum)
    expect(replayTimelineOffsetAtPosition(NaN, range)).toBe(range.minimum)
    expect(replayTimelinePosition(origin - 1n, range)).toBe(0)
    expect(replayTimelinePosition(origin + 101n, range)).toBe(1)
    const point = { minimum: origin, maximum: origin }
    expect(replayTimelinePosition(origin, point)).toBe(0.5)
    expect(replayTimelineOffsetAtPosition(1, point)).toBe(origin)
    expect(replayTimelineX(origin, origin, origin + 100n, 160)).toBe(40)
    expect(replayTimelineX(origin + 100n, origin, origin + 100n, 160)).toBe(120)
  })

  it('移动和调整端点不会越过范围，支持端点重合', () => {
    const bounds = { minimum: 0n, maximum: 100n }
    const range = { minimum: 20n, maximum: 80n }
    expect(moveReplayTimelineRange({ minimum: 120n, maximum: -20n }, bounds, 0n)).toEqual(bounds)
    expect(moveReplayTimelineRange(range, bounds, 1_000_000n)).toEqual({
      minimum: 40n,
      maximum: 100n,
    })
    expect(moveReplayTimelineRange(range, bounds, -1_000_000n)).toEqual({
      minimum: 0n,
      maximum: 60n,
    })
    expect(resizeReplayTimelineRange(range, bounds, 'minimum', 99n)).toEqual({
      minimum: 80n,
      maximum: 80n,
    })
    expect(resizeReplayTimelineRange(range, bounds, 'maximum', -3n)).toEqual({
      minimum: 20n,
      maximum: 20n,
    })
  })

  it('极大跨度下调整窗口端点仍可分离靠近的事件', () => {
    const large = 10n ** 80n
    const bounds = { minimum: -large, maximum: large }
    const prepared = prepareReplayTimeline(
      [-large, -400n, 0n, 400n, large].map((offset, index) => event(index, offset)),
    )
    const full = layoutReplayTimeline(prepared, 1128, bounds)
    expect(full.groupByIndex.get(1)).toBe(full.groupByIndex.get(2))
    expect(full.groupByIndex.get(2)).toBe(full.groupByIndex.get(3))
    const left = resizeReplayTimelineRange(bounds, bounds, 'minimum', -500n)
    const narrowed = resizeReplayTimelineRange(left, bounds, 'maximum', 500n)
    const detail = layoutReplayTimeline(prepared, 1128, narrowed)
    expect([...detail.groupByIndex.keys()]).toEqual([1, 2, 3])
    expect(detail.groupByIndex.get(1)).not.toBe(detail.groupByIndex.get(2))
    expect(detail.groupByIndex.get(2)).not.toBe(detail.groupByIndex.get(3))
  })
})
