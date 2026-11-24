import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type PointerEvent,
  type ReactNode,
} from 'react'
import { replayOffset } from '../lib/replay'
import {
  layoutReplayTimeline,
  moveReplayTimelineRange,
  prepareReplayTimeline,
  replayTimelineHistogram,
  replayTimelineOffsetAtPosition,
  replayTimelinePosition,
  replayTimelineTicks,
  resizeReplayTimelineRange,
  TIMELINE_LABEL_WIDTH,
  TIMELINE_LANES,
  type TimelineRange,
  type ReplayTimelineItem,
} from './replayTimelineLayout'
import './ReplayTimelineView.css'

export type { ReplayTimelineItem } from './replayTimelineLayout'

interface ReplayTimelineViewProps {
  events: ReplayTimelineItem[]
  renderDetail?: (event: ReplayTimelineItem) => ReactNode
  onSelect?: (event: ReplayTimelineItem) => void
  ariaLabel?: string
}

function timeLabel(value: bigint) {
  return replayOffset(value.toString())
}

export function ReplayTimelineView({
  events,
  renderDetail,
  onSelect,
  ariaLabel = '匹配事件时间轴',
}: ReplayTimelineViewProps) {
  const [selection, setSelection] = useState<{
    events: ReplayTimelineItem[]
    index: number
  } | null>(null)
  const [windowSelection, setWindowSelection] = useState<{
    events: ReplayTimelineItem[]
    range: TimelineRange
  } | null>(null)
  const [viewportWidth, setViewportWidth] = useState(720)
  const [jumpInput, setJumpInput] = useState('')
  const viewport = useRef<HTMLDivElement>(null)
  const overviewRail = useRef<HTMLDivElement>(null)
  const drag = useRef<{
    events: ReplayTimelineItem[]
    pointerId: number
    startX: number
    width: number
    range: TimelineRange
    part: 'minimum' | 'maximum' | 'move'
  } | null>(null)
  const prepared = useMemo(() => prepareReplayTimeline(events), [events])
  const minimum = prepared.minimum
  const maximum = prepared.maximum
  const bounds = minimum === undefined || maximum === undefined ? undefined : { minimum, maximum }
  const range = windowSelection?.events === events ? windowSelection.range : bounds
  const plotWidth = Math.max(1, viewportWidth - TIMELINE_LABEL_WIDTH - 2)
  const layout = useMemo(
    () => layoutReplayTimeline(prepared, plotWidth, range),
    [prepared, plotWidth, range?.minimum, range?.maximum],
  )
  const histogram = useMemo(() => replayTimelineHistogram(prepared), [prepared])
  const histogramPeak = Math.max(0, ...histogram)
  const currentIndex =
    selection?.events === events && selection.index < events.length ? selection.index : null
  const current = currentIndex === null ? undefined : events[currentIndex]
  const selectedGroup = currentIndex === null ? undefined : layout.groupByIndex.get(currentIndex)
  const hasTrack = prepared.entries.length > 0
  const isPoint = minimum !== undefined && minimum === maximum

  useEffect(() => {
    setSelection((previous) => (previous?.events === events ? previous : null))
    setWindowSelection((previous) => (previous?.events === events ? previous : null))
    setJumpInput('')
    drag.current = null
  }, [events])

  useEffect(() => {
    const element = viewport.current
    if (!element) return
    const measure = () => setViewportWidth(element.clientWidth || 720)
    measure()
    if (typeof ResizeObserver === 'undefined') {
      window.addEventListener('resize', measure)
      return () => window.removeEventListener('resize', measure)
    }
    const observer = new ResizeObserver(measure)
    observer.observe(element)
    return () => observer.disconnect()
  }, [hasTrack])

  const selectEvent = (index: number) => {
    const event = events[index]
    if (!event) return
    setSelection({ events, index })
    onSelect?.(event)
    const entry = prepared.byIndex.get(index)
    if (!entry || !range || !bounds) return
    if (entry.offset < range.minimum) {
      setWindowSelection({
        events,
        range: moveReplayTimelineRange(range, bounds, entry.offset - range.minimum),
      })
    } else if (entry.offset > range.maximum) {
      setWindowSelection({
        events,
        range: moveReplayTimelineRange(range, bounds, entry.offset - range.maximum),
      })
    }
  }

  const beginDrag = (event: PointerEvent<HTMLElement>, part: 'minimum' | 'maximum' | 'move') => {
    const rail = overviewRail.current
    if (!rail || !range || !bounds || event.button !== 0 || drag.current) return
    const width = rail.getBoundingClientRect().width
    if (!width) return
    event.preventDefault()
    event.stopPropagation()
    event.currentTarget.focus({ preventScroll: true })
    drag.current = { events, pointerId: event.pointerId, startX: event.clientX, width, range, part }
    rail.setPointerCapture(event.pointerId)
  }

  const moveDrag = (event: PointerEvent<HTMLDivElement>) => {
    const gesture = drag.current
    if (!gesture || gesture.events !== events || gesture.pointerId !== event.pointerId || !bounds)
      return
    event.preventDefault()
    const fraction = (event.clientX - gesture.startX) / gesture.width
    const magnitude = replayTimelineOffsetAtPosition(Math.abs(fraction), {
      minimum: 0n,
      maximum: bounds.maximum - bounds.minimum,
    })
    const delta = fraction < 0 ? -magnitude : magnitude
    const next =
      gesture.part === 'move'
        ? moveReplayTimelineRange(gesture.range, bounds, delta)
        : resizeReplayTimelineRange(
            gesture.range,
            bounds,
            gesture.part,
            gesture.range[gesture.part] + delta,
          )
    setWindowSelection({ events, range: next })
  }

  const endDrag = (event: PointerEvent<HTMLDivElement>) => {
    if (drag.current?.pointerId === event.pointerId) drag.current = null
  }

  const changeByKeyboard = (
    event: KeyboardEvent<HTMLElement>,
    part: 'minimum' | 'maximum' | 'move',
  ) => {
    if (!range || !bounds) return
    const width = range.maximum - range.minimum
    const unit = width / 100n > 0n ? width / 100n : 1n
    const step = event.shiftKey ? unit * 10n : unit
    let next: TimelineRange
    if (event.key === 'Home' || event.key === 'End') {
      const end = event.key === 'End'
      next =
        part === 'move'
          ? moveReplayTimelineRange(
              range,
              bounds,
              end ? bounds.maximum - range.maximum : bounds.minimum - range.minimum,
            )
          : resizeReplayTimelineRange(range, bounds, part, end ? bounds.maximum : bounds.minimum)
    } else {
      const direction =
        event.key === 'ArrowRight' || event.key === 'ArrowUp'
          ? 1n
          : event.key === 'ArrowLeft' || event.key === 'ArrowDown'
            ? -1n
            : 0n
      if (direction === 0n) return
      next =
        part === 'move'
          ? moveReplayTimelineRange(range, bounds, direction * step)
          : resizeReplayTimelineRange(range, bounds, part, range[part] + direction * step)
    }
    event.preventDefault()
    setWindowSelection({ events, range: next })
  }

  const ticks = useMemo(() => {
    if (!range) return []
    return replayTimelineTicks(range.minimum, range.maximum, plotWidth)
  }, [range?.minimum, range?.maximum, plotWidth])
  const activeLaneKinds = useMemo(
    () => new Set(prepared.entries.map((entry) => entry.lane)),
    [prepared],
  )
  const activeLanes = TIMELINE_LANES.filter((lane) => activeLaneKinds.has(lane.kind))
  const startPosition = bounds && range ? replayTimelinePosition(range.minimum, bounds) : 0
  const endPosition = bounds && range ? replayTimelinePosition(range.maximum, bounds) : 1
  const percent = (position: number) => Math.round(position * 1000000) / 10000
  const isFullRange =
    !bounds || !range || (range.minimum === bounds.minimum && range.maximum === bounds.maximum)
  const invalidCount = events.length - prepared.entries.length
  const jumpIndex = /^\d+$/.test(jumpInput) ? Number(jumpInput) - 1 : NaN
  const validJump = Number.isSafeInteger(jumpIndex) && jumpIndex >= 0 && jumpIndex < events.length

  if (events.length === 0) return <p className="replay-timeline-empty">没有事件。</p>

  return (
    <div className="replay-timeline-view">
      <div className="replay-timeline-toolbar">
        <div>
          <strong>事件时间轴</strong>
          <p>横轴为相对录制起点的时间偏移。拖动下方时间范围可调整显示。</p>
        </div>
        <button
          type="button"
          className="button button-ghost"
          disabled={isFullRange || isPoint}
          onClick={() => setWindowSelection(null)}
        >
          显示全部时间
        </button>
      </div>
      {invalidCount ? (
        <p className="replay-timeline-warning">
          {invalidCount} 条事件没有可精确使用的时间偏移，未绘制在时间轴上；仍可逐条查看。
        </p>
      ) : null}
      {!bounds || !range ? (
        <p className="replay-timeline-empty">没有可绘制的事件时间。</p>
      ) : (
        <>
          <div
            ref={viewport}
            className="replay-timeline-chart"
            role="region"
            aria-label={ariaLabel}
          >
            <div style={{ width: TIMELINE_LABEL_WIDTH + plotWidth }}>
              <div className="replay-timeline-axis-row">
                <div className="replay-timeline-lane-name replay-timeline-axis-name">相对偏移</div>
                <div className="replay-timeline-axis" style={{ width: plotWidth }}>
                  {ticks.map((tick, index) => (
                    <span
                      className="replay-timeline-tick"
                      data-edge={index === 0 ? 'start' : index === ticks.length - 1 ? 'end' : ''}
                      style={{ left: tick.x }}
                      key={tick.offset.toString()}
                    >
                      <span title={tick.label}>{tick.label}</span>
                    </span>
                  ))}
                </div>
              </div>
              {activeLanes.map((lane) => (
                <div className="replay-timeline-lane" key={lane.kind}>
                  <div className={`replay-timeline-lane-name kind-${lane.className}`}>
                    <b aria-hidden="true">{lane.symbol}</b> {lane.label}
                  </div>
                  <div className="replay-timeline-track" style={{ width: plotWidth }}>
                    {ticks.map((tick) => (
                      <span
                        className="replay-timeline-gridline"
                        style={{ left: tick.x }}
                        key={tick.offset.toString()}
                      />
                    ))}
                    {layout.groupsByLane.get(lane.kind)?.map((group) => {
                      const count = group.indices.length
                      const range =
                        group.minimum === group.maximum
                          ? timeLabel(group.minimum)
                          : `${timeLabel(group.minimum)} 至 ${timeLabel(group.maximum)}`
                      const description =
                        count === 1
                          ? `${events[group.firstIndex].label}，${range}`
                          : `${lane.label}，${count} 条事件，${range}；点击查看第 ${group.firstIndex + 1} 条`
                      return (
                        <button
                          type="button"
                          key={group.key}
                          className={`replay-timeline-marker kind-${lane.className}${count > 1 ? ' is-group' : ''}`}
                          style={{ left: group.x }}
                          aria-label={description}
                          title={description}
                          aria-pressed={selectedGroup === group.key}
                          data-event-count={count}
                          onClick={() => selectEvent(group.firstIndex)}
                        >
                          <span aria-hidden="true">{lane.symbol}</span>
                          <span className="replay-timeline-marker-label" aria-hidden="true">
                            {count > 1
                              ? `${lane.label} × ${count}`
                              : events[group.firstIndex].label}
                          </span>
                        </button>
                      )
                    })}
                  </div>
                </div>
              ))}
            </div>
          </div>
          {!isPoint ? (
            <div className="replay-timeline-overview">
              <div className="replay-timeline-overview-heading">
                <span>全部时间 · 事件密度</span>
                <span aria-live="polite">
                  显示范围：{timeLabel(range.minimum)} 至 {timeLabel(range.maximum)}
                </span>
              </div>
              <div className="replay-timeline-overview-inset">
                <div
                  ref={overviewRail}
                  className="replay-timeline-overview-rail"
                  onPointerMove={moveDrag}
                  onPointerUp={endDrag}
                  onPointerCancel={endDrag}
                  onLostPointerCapture={endDrag}
                >
                  <div className="replay-timeline-density" aria-hidden="true">
                    {histogram.map((count, index) => (
                      <span
                        key={index}
                        style={{
                          height: histogramPeak
                            ? `${(Math.log1p(count) / Math.log1p(histogramPeak)) * 100}%`
                            : '0%',
                        }}
                      />
                    ))}
                  </div>
                  <div
                    className="replay-timeline-overview-selected"
                    style={{
                      left: `${startPosition * 100}%`,
                      width: `${(endPosition - startPosition) * 100}%`,
                    }}
                    role="slider"
                    tabIndex={0}
                    aria-label="平移时间范围"
                    aria-orientation="horizontal"
                    aria-valuemin={0}
                    aria-valuemax={percent(
                      Math.max(startPosition, 1 - (endPosition - startPosition)),
                    )}
                    aria-valuenow={percent(startPosition)}
                    aria-valuetext={`${timeLabel(range.minimum)} 至 ${timeLabel(range.maximum)}`}
                    onPointerDown={(event) => beginDrag(event, 'move')}
                    onKeyDown={(event) => changeByKeyboard(event, 'move')}
                  />
                  <div
                    className="replay-timeline-overview-handle is-start"
                    style={{ left: `${startPosition * 100}%` }}
                    role="slider"
                    tabIndex={0}
                    aria-label="时间范围起点"
                    aria-orientation="horizontal"
                    aria-valuemin={0}
                    aria-valuemax={percent(endPosition)}
                    aria-valuenow={percent(startPosition)}
                    aria-valuetext={timeLabel(range.minimum)}
                    onPointerDown={(event) => beginDrag(event, 'minimum')}
                    onKeyDown={(event) => changeByKeyboard(event, 'minimum')}
                  >
                    <span aria-hidden="true" />
                  </div>
                  <div
                    className="replay-timeline-overview-handle is-end"
                    style={{ left: `${endPosition * 100}%` }}
                    role="slider"
                    tabIndex={0}
                    aria-label="时间范围终点"
                    aria-orientation="horizontal"
                    aria-valuemin={percent(startPosition)}
                    aria-valuemax={100}
                    aria-valuenow={percent(endPosition)}
                    aria-valuetext={timeLabel(range.maximum)}
                    onPointerDown={(event) => beginDrag(event, 'maximum')}
                    onKeyDown={(event) => changeByKeyboard(event, 'maximum')}
                  >
                    <span aria-hidden="true" />
                  </div>
                  <div
                    className="replay-timeline-overview-grip"
                    style={{ left: `${(startPosition + endPosition) * 50}%` }}
                    aria-hidden="true"
                    onPointerDown={(event) => beginDrag(event, 'move')}
                  >
                    ⋮⋮
                  </div>
                </div>
              </div>
            </div>
          ) : null}
          <p className="replay-timeline-help">
            拖动两端手柄调整范围，拖动选区或下方抓手平移；聚合标记可通过下方按钮逐条查看。
          </p>
        </>
      )}
      <div className="replay-timeline-selection">
        <div className="replay-timeline-selection-heading">
          <div aria-live="polite">
            <strong>{current ? current.label : '选择事件查看详情'}</strong>
            <span>
              {currentIndex === null
                ? `共 ${events.length} 条事件`
                : `第 ${currentIndex + 1} / ${events.length} 条 · ${
                    prepared.byIndex.has(currentIndex)
                      ? timeLabel(prepared.byIndex.get(currentIndex)!.offset)
                      : '时间偏移不可用'
                  }`}
            </span>
          </div>
          <div className="replay-timeline-navigation" role="group" aria-label="逐条浏览事件">
            <button
              type="button"
              className="button button-ghost"
              disabled={currentIndex === null || currentIndex === 0}
              onClick={() => selectEvent((currentIndex ?? 0) - 1)}
            >
              上一条
            </button>
            <button
              type="button"
              className="button button-ghost"
              disabled={currentIndex === events.length - 1}
              onClick={() => selectEvent(currentIndex === null ? 0 : currentIndex + 1)}
            >
              下一条
            </button>
            <label className="replay-timeline-jump">
              第
              <input
                type="number"
                min={1}
                max={events.length}
                step={1}
                value={jumpInput}
                onChange={(event) => setJumpInput(event.target.value)}
                aria-label="要跳转的事件条目位置"
              />
              条
            </label>
            <button
              type="button"
              className="button button-ghost"
              disabled={!validJump}
              onClick={() => selectEvent(jumpIndex)}
            >
              跳转
            </button>
          </div>
        </div>
        {current && renderDetail ? (
          <div className="replay-timeline-detail">{renderDetail(current)}</div>
        ) : null}
      </div>
    </div>
  )
}
