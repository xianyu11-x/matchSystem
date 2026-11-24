export interface ReplayTimelineItem {
  id: string
  offsetNs?: number | string | bigint
  kind: string
  label: string
}

export const TIMELINE_LANES = [
  { kind: 'stream_start', label: '录制开始', symbol: '●', className: 'stream-start' },
  { kind: 'stream_end', label: '录制结束', symbol: '◎', className: 'stream-end' },
  { kind: 'ticket_add', label: '票据加入', symbol: '◆', className: 'ticket-add' },
  { kind: 'ticket_withdraw', label: '票据主动退出', symbol: '▼', className: 'ticket-withdraw' },
  { kind: 'ticket_remove', label: '票据移除', symbol: '✕', className: 'ticket-remove' },
  { kind: 'round_begin', label: '轮次开始', symbol: '■', className: 'round-begin' },
  { kind: 'produce_match', label: '匹配调用', symbol: '▲', className: 'produce-match' },
  { kind: 'other', label: '其他事件', symbol: '◇', className: 'other' },
] as const

type TimelineLane = (typeof TIMELINE_LANES)[number]['kind']

export const TIMELINE_LABEL_WIDTH = 112
export const TIMELINE_PADDING = 64
const MARKER_GAP = 132
const knownLanes = new Set<string>(TIMELINE_LANES.map((lane) => lane.kind))

interface TimedEvent {
  index: number
  offset: bigint
  lane: TimelineLane
}

export interface PreparedTimeline {
  entries: TimedEvent[]
  sortedEntries: TimedEvent[]
  byIndex: Map<number, TimedEvent>
  minimum?: bigint
  maximum?: bigint
}

export interface TimelineRange {
  minimum: bigint
  maximum: bigint
}

export interface TimelineGroup {
  key: string
  lane: TimelineLane
  x: number
  firstIndex: number
  minimum: bigint
  maximum: bigint
  indices: number[]
}

export interface TimelineLayout {
  groupsByLane: Map<TimelineLane, TimelineGroup[]>
  groupByIndex: Map<number, string>
}

export interface TimelineTick {
  offset: bigint
  x: number
  label: string
}

const RATIO_SCALE = BigInt(Number.MAX_SAFE_INTEGER)

function clampTime(value: bigint, minimum: bigint, maximum: bigint) {
  return value < minimum ? minimum : value > maximum ? maximum : value
}

/** 夹紧闭区间，避免拖动手柄后越过全域边界或产生反向区间。 */
function clampReplayTimelineRange(range: TimelineRange, bounds: TimelineRange): TimelineRange {
  const lower = range.minimum < range.maximum ? range.minimum : range.maximum
  const upper = range.minimum < range.maximum ? range.maximum : range.minimum
  return {
    minimum: clampTime(lower, bounds.minimum, bounds.maximum),
    maximum: clampTime(upper, bounds.minimum, bounds.maximum),
  }
}

/** 在完整范围内平移窗口，并保持窗口的时间跨度不变。 */
export function moveReplayTimelineRange(
  range: TimelineRange,
  bounds: TimelineRange,
  delta: bigint,
): TimelineRange {
  const current = clampReplayTimelineRange(range, bounds)
  const span = current.maximum - current.minimum
  const minimum = clampTime(current.minimum + delta, bounds.minimum, bounds.maximum - span)
  return { minimum, maximum: minimum + span }
}

/** 调整一个端点；两个端点可重合，但不可交叉或越出全域。 */
export function resizeReplayTimelineRange(
  range: TimelineRange,
  bounds: TimelineRange,
  edge: 'minimum' | 'maximum',
  value: bigint,
): TimelineRange {
  const current = clampReplayTimelineRange(range, bounds)
  return edge === 'minimum'
    ? { ...current, minimum: clampTime(value, bounds.minimum, current.maximum) }
    : { ...current, maximum: clampTime(value, current.minimum, bounds.maximum) }
}

/** 仅在计算有限比例时转成 Number，时间及跨度始终保留为 BigInt。 */
export function replayTimelinePosition(offset: bigint, range: TimelineRange): number {
  const span = range.maximum - range.minimum
  if (span <= 0n) return 0.5
  const relative = clampTime(offset, range.minimum, range.maximum) - range.minimum
  return Number((relative * RATIO_SCALE + span / 2n) / span) / Number(RATIO_SCALE)
}

export function replayTimelineOffsetAtPosition(position: number, range: TimelineRange): bigint {
  const span = range.maximum - range.minimum
  if (span <= 0n) return range.minimum
  const fraction = Number.isNaN(position) ? 0 : Math.max(0, Math.min(1, position))
  const scaled = BigInt(Math.round(fraction * Number(RATIO_SCALE)))
  return range.minimum + (span * scaled + RATIO_SCALE / 2n) / RATIO_SCALE
}

/** 数值若已超出安全整数范围，其原始时间已不可恢复，不能放入时间轴。 */
export function timelineOffset(value: ReplayTimelineItem['offsetNs']): bigint | undefined {
  if (typeof value === 'bigint') return value
  if (typeof value === 'number') {
    if (!Number.isSafeInteger(value)) return undefined
    return BigInt(value)
  }
  if (typeof value !== 'string' || !/^-?\d+$/.test(value)) return undefined
  try {
    return BigInt(value)
  } catch {
    return undefined
  }
}

export function prepareReplayTimeline(events: ReplayTimelineItem[]): PreparedTimeline {
  const entries: TimedEvent[] = []
  const byIndex = new Map<number, TimedEvent>()
  let minimum: bigint | undefined
  let maximum: bigint | undefined
  events.forEach((event, index) => {
    const offset = timelineOffset(event.offsetNs)
    if (offset === undefined) return
    const lane = (knownLanes.has(event.kind) ? event.kind : 'other') as TimelineLane
    const entry = { index, offset, lane }
    entries.push(entry)
    byIndex.set(index, entry)
    if (minimum === undefined || offset < minimum) minimum = offset
    if (maximum === undefined || offset > maximum) maximum = offset
  })
  const sortedEntries = [...entries].sort((left, right) =>
    left.offset < right.offset ? -1 : left.offset > right.offset ? 1 : left.index - right.index,
  )
  return { entries, sortedEntries, byIndex, minimum, maximum }
}

/** 窄屏适当减小留白，始终为绘图区保留可用宽度。 */
function replayTimelinePadding(width: number) {
  return Math.min(TIMELINE_PADDING, Math.max(0, width) / 4)
}

/** 先以 BigInt 减去原点，再将有限宽度的像素坐标转换为 Number。 */
export function replayTimelineX(offset: bigint, minimum: bigint, maximum: bigint, width: number) {
  if (minimum === maximum) return width / 2
  const padding = replayTimelinePadding(width)
  const usable = Math.max(0, Math.round(width - padding * 2))
  return padding + Number(((offset - minimum) * BigInt(usable * 1000)) / (maximum - minimum)) / 1000
}

function niceStep(target: bigint) {
  if (target <= 1n) return 1n
  let power = 1n
  while (power * 10n <= target) power *= 10n
  const candidates = [power, 2n * power, 5n * power, 10n * power]
  return candidates.reduce((best, candidate) => {
    const distance = candidate > target ? candidate - target : target - candidate
    const bestDistance = best > target ? best - target : target - best
    return distance < bestDistance ? candidate : best
  })
}

function previousStep(step: bigint) {
  let power = 1n
  while (power * 10n <= step) power *= 10n
  if (step === 5n * power) return 2n * power
  if (step === 2n * power) return power
  return power >= 10n ? 5n * (power / 10n) : 1n
}

function firstTick(minimum: bigint, step: bigint) {
  return (minimum < 0n ? minimum / step : (minimum + step - 1n) / step) * step
}

function formatTick(offset: bigint, step: bigint) {
  const divisor = step < 1_000n ? 1n : step < 1_000_000n ? 1_000n : 1_000_000n
  const digits = divisor === 1n ? 0 : divisor === 1_000n ? 3 : 6
  const unit = divisor === 1n ? 'ns' : divisor === 1_000n ? 'μs' : 'ms'
  const magnitude = offset < 0n ? -offset : offset
  const fraction = digits
    ? String(magnitude % divisor)
        .padStart(digits, '0')
        .replace(/0+$/, '')
    : ''
  return `${offset < 0n ? '-' : '+'}${magnitude / divisor}${fraction ? `.${fraction}` : ''} ${unit}`
}

/** 刻度间距取 1、2、5 乘以 10 的整数次幂，标签按尺度选择时间单位。 */
export function replayTimelineTicks(
  minimum: bigint,
  maximum: bigint,
  width: number,
): TimelineTick[] {
  if (minimum === maximum) {
    return [{ offset: minimum, x: width / 2, label: formatTick(minimum, 1_000_000n) }]
  }
  const span = maximum - minimum
  const divisions = BigInt(
    Math.max(2, Math.floor((width - 2 * replayTimelinePadding(width)) / 150)),
  )
  let step = niceStep((span + divisions / 2n) / divisions)
  let first = firstTick(minimum, step)
  while (first + step > maximum && step > 1n) {
    step = previousStep(step)
    first = firstTick(minimum, step)
  }
  const ticks: TimelineTick[] = []
  for (let offset = first; offset <= maximum; offset += step) {
    ticks.push({
      offset,
      x: replayTimelineX(offset, minimum, maximum, width),
      label: formatTick(offset, step),
    })
  }
  return ticks
}

function lowerBound(entries: TimedEvent[], offset: bigint, inclusive: boolean) {
  let start = 0
  let end = entries.length
  while (start < end) {
    const middle = start + Math.floor((end - start) / 2)
    const current = entries[middle].offset
    if (current < offset || (!inclusive && current === offset)) start = middle + 1
    else end = middle
  }
  return start
}

/** 以全域等宽时间桶统计事件，二分查找避免每次视图更新都遍历全部事件。 */
export function replayTimelineHistogram(prepared: PreparedTimeline, binCount = 80): number[] {
  if (!Number.isFinite(binCount) || binCount < 1) return []
  const count = Math.floor(binCount)
  const bins = Array<number>(count).fill(0)
  const { minimum, maximum, sortedEntries } = prepared
  if (minimum === undefined || maximum === undefined) return bins
  const span = maximum - minimum
  if (span === 0n) {
    bins[Math.floor(count / 2)] = sortedEntries.length
    return bins
  }
  const countBig = BigInt(count)
  let previous = 0
  for (let index = 0; index < count; index++) {
    const next =
      index === count - 1
        ? sortedEntries.length
        : lowerBound(
            sortedEntries,
            minimum + (span * BigInt(index + 1) + countBig - 1n) / countBig,
            true,
          )
    bins[index] = next - previous
    previous = next
  }
  return bins
}

/** 近邻事件按泳道聚合；保留全部索引，避免密集或同刻事件不可访问。 */
export function layoutReplayTimeline(
  prepared: PreparedTimeline,
  width: number,
  range?: TimelineRange,
): TimelineLayout {
  const groupsByLane = new Map<TimelineLane, TimelineGroup[]>()
  const groupByIndex = new Map<number, string>()
  if (prepared.minimum === undefined || prepared.maximum === undefined)
    return { groupsByLane, groupByIndex }
  const { minimum, maximum } = range ?? { minimum: prepared.minimum, maximum: prepared.maximum }
  if (minimum > maximum) return { groupsByLane, groupByIndex }

  const buckets = new Map<TimelineLane, Map<number, TimelineGroup>>()
  const first = lowerBound(prepared.sortedEntries, minimum, true)
  const last = lowerBound(prepared.sortedEntries, maximum, false)
  for (let index = first; index < last; index++) {
    const entry = prepared.sortedEntries[index]
    const x = replayTimelineX(entry.offset, minimum, maximum, width)
    const bucket = Math.floor(x / MARKER_GAP)
    let laneBuckets = buckets.get(entry.lane)
    if (!laneBuckets) {
      laneBuckets = new Map()
      buckets.set(entry.lane, laneBuckets)
    }
    const group = laneBuckets.get(bucket)
    if (!group) {
      laneBuckets.set(bucket, {
        key: `${entry.lane}:${bucket}`,
        lane: entry.lane,
        x,
        firstIndex: entry.index,
        minimum: entry.offset,
        maximum: entry.offset,
        indices: [entry.index],
      })
    } else {
      group.indices.push(entry.index)
      group.firstIndex = Math.min(group.firstIndex, entry.index)
      if (entry.offset < group.minimum) {
        group.minimum = entry.offset
        group.x = x
      }
      if (entry.offset > group.maximum) group.maximum = entry.offset
    }
  }
  for (const [lane, laneBuckets] of buckets) {
    const groups: TimelineGroup[] = []
    const ordered = Array.from(laneBuckets.values()).sort((a, b) => a.x - b.x)
    for (const candidate of ordered) {
      const last = groups[groups.length - 1]
      if (last && candidate.x - last.x < MARKER_GAP) {
        last.firstIndex = Math.min(last.firstIndex, candidate.firstIndex)
        if (candidate.maximum > last.maximum) last.maximum = candidate.maximum
        for (const index of candidate.indices) last.indices.push(index)
      } else {
        groups.push(candidate)
      }
    }
    for (const group of groups) {
      for (const index of group.indices) groupByIndex.set(index, group.key)
    }
    groupsByLane.set(lane, groups)
  }
  return { groupsByLane, groupByIndex }
}
