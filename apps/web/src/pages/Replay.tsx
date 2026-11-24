import { useEffect, useMemo, useRef, useState } from 'react'
import { ReplayTimelineView, type ReplayTimelineItem } from '../components/ReplayTimelineView'
import { PageHeader, SectionTitle } from '../components/States'
import { ApiError, api, formatApiError, isDemoMode } from '../lib/api'
import { useRuleLibrary, useScenario } from '../lib/queries'
import { ruleIdentity, ruleIdentityLabel } from '../lib/ruleIdentity'
import { saveJsonFile } from '../lib/saveJsonFile'
import {
  readReplayTimeline,
  replayOffset,
  replayRows,
  type ReplayEvent,
  type ReplayInstance,
  type ReplayResult,
  type ReplayTimeline,
} from '../lib/replay'
import './Replay.css'

const noEvents: ReplayEvent[] = []

function errorText(error: unknown): string {
  const message = formatApiError(error)
  if (!(error instanceof ApiError)) return message
  const envelope = error.details as { error?: { details?: unknown } } | undefined
  const details = envelope?.error?.details
  if (!Array.isArray(details)) return message
  const issues = details.flatMap((item: unknown) => {
    if (!item || typeof item !== 'object') return []
    const issue = item as { path?: string; message?: string; code?: string }
    return issue.message ? [`${issue.path ? `${issue.path}: ` : ''}${issue.message}`] : []
  })
  return [message, ...issues.filter((issue) => !message.includes(issue))].join('\n')
}

function labelForInstance(instance: ReplayInstance): string {
  const logical = instance.logicalNode
  return `规则 ${ruleIdentityLabel(logical.rule)} · 部署 ${logical.placementId} · 物理节点 ${instance.physicalNodeId} · 实例 ${instance.instanceId}`
}

function eventType(type: string): string {
  const names: Record<string, string> = {
    stream_start: '录制开始',
    stream_end: '录制结束',
    ticket_add: '票据加入',
    ticket_withdraw: '票据主动退出',
    ticket_remove: '票据移除',
    round_begin: '轮次开始',
    produce_match: '匹配调用',
  }
  return names[type] ?? type
}

const statusLabels: Record<string, string> = {
  match: '已成局',
  matched: '已成局',
  no_match: '未成局',
  applied: '已执行',
  already_left: '票据已离池',
  error: '执行出错',
}

interface ReplayTimelineRow extends ReplayTimelineItem {
  event: ReplayEvent
  source?: ReplayEvent
  call?: number
  previousRound?: { round: ReplayEvent['round']; now: ReplayEvent['now'] }
}

function eventTicket(event: ReplayEvent, source?: ReplayEvent) {
  return (
    event.ticketId ??
    event.ticket?.ticketId ??
    event.ticket?.TicketID ??
    source?.ticketId ??
    source?.ticket?.ticketId ??
    source?.ticket?.TicketID
  )
}

/** 原始事件一对一映射到时间轴，序号相同的异常输入也保留独立入口。 */
export function buildReplayTimelineRows(
  events: ReplayEvent[],
  sourceEvents: ReplayEvent[] = noEvents,
  result = false,
): ReplayTimelineRow[] {
  const sources = new Map(sourceEvents.map((event) => [String(event.sequence), event]))
  let previousRound: ReplayTimelineRow['previousRound']
  return replayRows(events).map(({ event, call }, index) => {
    const source = sources.get(String(event.sequence))
    if (event.type === 'round_begin') previousRound = { round: event.round, now: event.now }
    const ticket = eventTicket(event, source)
    const status = result ? event.status : event.result
    const label = [
      `${eventType(event.type)}${call === undefined ? '' : ` ${call}`}`,
      ticket === undefined ? '' : `票据 ${ticket}`,
      (event.type === 'round_begin' || event.type === 'produce_match') && event.round !== undefined
        ? `第 ${event.round} 轮`
        : '',
      status ? (statusLabels[status] ?? status) : '',
    ]
      .filter(Boolean)
      .join(' · ')
    return {
      id: `${String(event.sequence)}-${index}`,
      offsetNs: event.offsetNs,
      kind: event.type,
      label,
      event,
      source,
      call,
      previousRound,
    }
  })
}

export function ReplayEventDetail({ row, result }: { row: ReplayTimelineRow; result: boolean }) {
  const { event, source, call, previousRound } = row
  const ticket = eventTicket(event, source)
  const createdAt = event.ticket?.CreatedAt ?? source?.ticket?.CreatedAt
  const isRoundEvent = event.type === 'round_begin' || event.type === 'produce_match'
  const sourceMembers = source?.matchTicketIds ?? event.sourceTicketIds
  return (
    <section className="replay-event-detail" aria-label="事件详情">
      <div className="replay-detail-heading">
        <h3>
          {eventType(event.type)}
          {call === undefined ? '' : ` ${call}`}
        </h3>
        <span>序号 {String(event.sequence)}</span>
      </div>
      <dl className="replay-detail-fields">
        <div>
          <dt>事件时点</dt>
          <dd>{replayOffset(event.offsetNs)}</dd>
        </div>
        {isRoundEvent && event.round !== undefined ? (
          <div>
            <dt>轮次</dt>
            <dd>第 {event.round} 轮</dd>
          </div>
        ) : previousRound?.round !== undefined ? (
          <div>
            <dt>最近轮次</dt>
            <dd>第 {previousRound.round} 轮</dd>
          </div>
        ) : null}
        {isRoundEvent && event.now !== undefined ? (
          <div>
            <dt>逻辑时间</dt>
            <dd>now={String(event.now)}</dd>
          </div>
        ) : previousRound?.now !== undefined ? (
          <div>
            <dt>最近轮次逻辑时间</dt>
            <dd>now={String(previousRound.now)}</dd>
          </div>
        ) : null}
        {ticket !== undefined ? (
          <div>
            <dt>票据</dt>
            <dd>{String(ticket)}</dd>
          </div>
        ) : null}
        {event.type === 'ticket_add' && createdAt !== undefined ? (
          <div>
            <dt>创建时间</dt>
            <dd>{String(createdAt)}</dd>
          </div>
        ) : null}
        {event.type === 'produce_match' ? (
          <div>
            <dt>尝试种子</dt>
            <dd>{String(event.seedAttempts ?? 0)}</dd>
          </div>
        ) : null}
        {result ? (
          <div>
            <dt>执行结果</dt>
            <dd>{statusLabels[event.status ?? ''] ?? event.status ?? '—'}</dd>
          </div>
        ) : event.result ? (
          <div>
            <dt>源结果</dt>
            <dd>{statusLabels[event.result] ?? event.result}</dd>
          </div>
        ) : null}
        {(result ? event.ticketIds : event.matchTicketIds)?.length ? (
          <div>
            <dt>成员</dt>
            <dd>{(result ? event.ticketIds : event.matchTicketIds)?.join(', ')}</dd>
          </div>
        ) : null}
      </dl>
      {event.error ? <p className="replay-row-error">{event.error}</p> : null}
      {result && event.type === 'produce_match' && (source || sourceMembers?.length) ? (
        <p className="replay-source-result">
          源调用：{statusLabels[source?.result ?? ''] ?? source?.result ?? '—'} · 尝试种子{' '}
          {source?.seedAttempts === undefined ? '—' : String(source.seedAttempts)}
          {sourceMembers?.length ? ` · 成员 ${sourceMembers.join(', ')}` : ''}
        </p>
      ) : null}
      {result && source?.error ? (
        <p className="replay-row-error">源事件错误：{source.error}</p>
      ) : null}
      <details>
        <summary>查看完整事件</summary>
        <pre>{JSON.stringify(event, null, 2)}</pre>
      </details>
      {result && source ? (
        <details>
          <summary>查看完整源事件</summary>
          <pre>{JSON.stringify(source, null, 2)}</pre>
        </details>
      ) : null}
    </section>
  )
}

export function EventTimeline({
  events,
  result = false,
  sourceEvents = noEvents,
}: {
  events: ReplayEvent[]
  result?: boolean
  sourceEvents?: ReplayEvent[]
}) {
  const rows = useMemo(
    () => buildReplayTimelineRows(events, sourceEvents, result),
    [events, sourceEvents, result],
  )
  const rowsById = useMemo(() => new Map(rows.map((row) => [row.id, row])), [rows])
  return (
    <ReplayTimelineView
      events={rows}
      ariaLabel={result ? '回放结果时间轴' : '提取时间线时间轴'}
      renderDetail={(item) => {
        const row = rowsById.get(item.id)
        return row ? <ReplayEventDetail row={row} result={result} /> : null
      }}
    />
  )
}

export function Replay() {
  const library = useRuleLibrary()
  const scenario = useScenario()
  const [source, setSource] = useState<'live' | 'logs'>('live')
  const [logs, setLogs] = useState('')
  const [fileNames, setFileNames] = useState('')
  const [instances, setInstances] = useState<ReplayInstance[]>([])
  const [instanceId, setInstanceId] = useState('')
  const [timelineJson, setTimelineJson] = useState('')
  const [timeline, setTimeline] = useState<ReplayTimeline>()
  const [targetRuleId, setTargetRuleId] = useState('')
  const [providerId, setProviderId] = useState('')
  const [result, setResult] = useState<ReplayResult>()
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  const [notice, setNotice] = useState('')
  const requestId = useRef(0)
  const timelineInput = useRef<HTMLInputElement>(null)

  const deployments = useMemo(
    () =>
      (scenario.data?.rules ?? []).filter(
        (rule) => rule.apiRule && ruleIdentity(rule.apiRule) === targetRuleId,
      ),
    [scenario.data?.rules, targetRuleId],
  )
  const selectedInstance = instances.find((instance) => instance.instanceId === instanceId)

  const clearTimeline = () => {
    setTimelineJson('')
    setTimeline(undefined)
    setResult(undefined)
    setMessage('')
    setNotice('')
  }

  const listInstances = async (sourceLogs?: string) => {
    const request = ++requestId.current
    setBusy(true)
    setMessage('')
    setNotice('')
    try {
      const response = await api.extractTimeline(
        sourceLogs === undefined ? {} : { logs: sourceLogs },
      )
      if (request !== requestId.current) return
      setInstances(response.instances ?? [])
      setInstanceId('')
      clearTimeline()
    } catch (error) {
      if (request === requestId.current) {
        setInstances([])
        setMessage(errorText(error))
      }
    } finally {
      if (request === requestId.current) setBusy(false)
    }
  }

  useEffect(() => {
    void listInstances()
    return () => {
      requestId.current++
    }
  }, [])

  const changeSource = (next: 'live' | 'logs') => {
    setSource(next)
    setInstances([])
    setInstanceId('')
    clearTimeline()
    if (next === 'live') void listInstances()
    else if (logs) void listInstances(logs)
  }

  const loadLogs = async (files: FileList | null) => {
    if (!files?.length) return
    setBusy(true)
    clearTimeline()
    setInstances([])
    setInstanceId('')
    setLogs('')
    setFileNames('')
    try {
      const selected = Array.from(files)
      const content = (await Promise.all(selected.map((file) => file.text()))).join('\n')
      if (!content.trim()) throw new Error('所选日志为空。')
      setLogs(content)
      setFileNames(selected.map((file) => file.name).join('、'))
      await listInstances(content)
    } catch (error) {
      setMessage(errorText(error))
      setBusy(false)
    }
  }

  const extract = async () => {
    if (!instanceId) return
    setBusy(true)
    clearTimeline()
    try {
      const response = await api.extractTimeline({
        ...(source === 'logs' ? { logs } : {}),
        instanceId,
      })
      if (!response.timelineJson) throw new Error('服务未返回时间线。')
      setTimeline(readReplayTimeline(response.timelineJson))
      setTimelineJson(response.timelineJson)
      setInstances(response.instances ?? instances)
    } catch (error) {
      setMessage(errorText(error))
    } finally {
      setBusy(false)
    }
  }

  const importTimeline = async (file?: File) => {
    if (!file) return
    clearTimeline()
    try {
      const raw = await file.text()
      setTimeline(readReplayTimeline(raw))
      setTimelineJson(raw)
      setInstanceId('')
      setNotice(`已载入 ${file.name}。回放前会再次校验时间线和目标规则。`)
    } catch (error) {
      setMessage(errorText(error))
    } finally {
      if (timelineInput.current) timelineInput.current.value = ''
    }
  }

  const exportTimeline = async () => {
    if (!timelineJson) return
    try {
      const safeName = (timeline?.instanceId ?? 'timeline').replace(/[^a-zA-Z0-9._-]+/g, '-')
      const saved = await saveJsonFile(`match-timeline-${safeName}.json`, () => timelineJson)
      if (saved) setNotice(`时间线已保存：${saved}`)
    } catch (error) {
      setMessage(errorText(error))
    }
  }

  const replay = async () => {
    if (!timelineJson || !targetRuleId) return
    setBusy(true)
    setMessage('')
    setResult(undefined)
    try {
      const provider = deployments.find((item) => item.placementId === providerId)
      const response = await api.replayTimeline({
        timelineJson,
        targetRuleId,
        ...(provider?.apiRule
          ? { providerLogicalNode: { rule: provider.apiRule, placementId: provider.placementId } }
          : {}),
      })
      setResult(response)
    } catch (error) {
      setMessage(errorText(error))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="page-stack replay-page">
      <PageHeader
        eyebrow="SIMULATOR / REPLAY"
        title="匹配时间线回放"
        description="选择具体逻辑节点实例，查看其票据与匹配调用，并用规则库中的规则在隔离节点重放。"
      />
      <section className="panel replay-panel">
        <SectionTitle title="1. 选择日志来源" detail="模拟器录制与上传日志均按逻辑节点实例区分。" />
        <div className="replay-body">
          <div className="mode-switch replay-source" role="group" aria-label="日志来源">
            <button
              type="button"
              className={source === 'live' ? 'active' : ''}
              disabled={busy}
              onClick={() => changeSource('live')}
            >
              当前模拟器
            </button>
            <button
              type="button"
              className={source === 'logs' ? 'active' : ''}
              disabled={busy}
              onClick={() => changeSource('logs')}
            >
              上传日志
            </button>
          </div>
          {source === 'logs' ? (
            <label className="field-label replay-upload">
              选择录制的 .log、宿主日志或历史 JSONL（UTF-8，可多选，合计不超过 32 MiB）
              <input
                className="text-input"
                type="file"
                multiple
                accept=".log,.json,.jsonl,.txt,application/json,text/plain"
                disabled={busy}
                onChange={(event) => void loadLogs(event.target.files)}
              />
              {fileNames ? <span>{fileNames}</span> : null}
            </label>
          ) : (
            <p className="replay-hint">
              读取本次服务启动以来保留的录制数据。缓存达到上限后，较早记录可能被丢弃。
            </p>
          )}
          <div className="replay-controls">
            <label className="field-label replay-grow">
              逻辑节点实例
              <select
                className="filter-select"
                value={instanceId}
                disabled={busy || instances.length === 0}
                onChange={(event) => {
                  setInstanceId(event.target.value)
                  clearTimeline()
                }}
              >
                <option value="">{instances.length ? '请选择实例' : '没有可选实例'}</option>
                {instances.map((instance) => (
                  <option key={instance.instanceId} value={instance.instanceId}>
                    {labelForInstance(instance)} · {instance.eventCount} 条
                  </option>
                ))}
              </select>
            </label>
            <button
              type="button"
              className="button button-ghost"
              disabled={busy || (source === 'logs' && !logs)}
              onClick={() => void listInstances(source === 'live' ? undefined : logs)}
            >
              刷新实例
            </button>
            <button
              type="button"
              className="button button-primary"
              disabled={busy || !instanceId}
              onClick={() => void extract()}
            >
              提取时间线
            </button>
          </div>
          {selectedInstance ? (
            <p className="replay-hint">
              序号 {selectedInstance.firstSequence}–{selectedInstance.lastSequence} ·{' '}
              {selectedInstance.replayable ? '可供回放' : '需检查完整性'}
              {selectedInstance.issue ? ` · ${selectedInstance.issue}` : ''}
            </p>
          ) : null}
          <div className="replay-actions">
            <input
              ref={timelineInput}
              className="visually-hidden"
              type="file"
              accept=".json,application/json"
              aria-label="选择时间线 JSON 文件"
              onChange={(event) => void importTimeline(event.target.files?.[0])}
            />
            <button
              type="button"
              className="button button-ghost"
              disabled={busy}
              onClick={() => timelineInput.current?.click()}
            >
              载入时间线文件
            </button>
            <button
              type="button"
              className="button button-ghost"
              disabled={busy || !timelineJson}
              onClick={() => void exportTimeline()}
            >
              保存时间线
            </button>
          </div>
          {isDemoMode ? (
            <p className="replay-hint">演示模式不提供时间线录制和回放，请连接模拟器服务。</p>
          ) : null}
        </div>
      </section>

      <section className="panel replay-panel">
        <SectionTitle
          title="2. 查看时间线"
          detail={
            timeline
              ? `实例 ${timeline.instanceId} · ${timeline.events.length} 条事件 · 事件时点为相对录制起点的偏移${typeof timeline.events[0]?.baseTime === 'string' ? ` · 起点 ${timeline.events[0].baseTime}` : ''}`
              : '提取或载入时间线后，在这里查看票据与每轮调用。'
          }
        />
        {timeline ? (
          <EventTimeline events={timeline.events} />
        ) : (
          <p className="replay-empty">尚未载入时间线。</p>
        )}
      </section>

      <section className="panel replay-panel">
        <SectionTitle
          title="3. 校验并回放"
          detail="选择已保存的规则；提交后校验属性、Fact 和时间线完整性。"
        />
        <div className="replay-body">
          <div className="replay-controls">
            <label className="field-label replay-grow">
              目标规则
              <select
                className="filter-select"
                value={targetRuleId}
                disabled={busy || library.isLoading}
                onChange={(event) => {
                  setTargetRuleId(event.target.value)
                  setProviderId('')
                  setResult(undefined)
                }}
              >
                <option value="">
                  {library.data?.rules.length ? '请选择规则' : '规则库中没有规则'}
                </option>
                {(library.data?.rules ?? []).map((rule) => (
                  <option key={ruleIdentity(rule.ruleKey)} value={ruleIdentity(rule.ruleKey)}>
                    {ruleIdentityLabel(rule.ruleKey)}
                  </option>
                ))}
              </select>
            </label>
            <label className="field-label replay-grow">
              Fact Provider 来源（可选）
              <select
                className="filter-select"
                value={providerId}
                disabled={busy || !targetRuleId}
                onChange={(event) => {
                  setProviderId(event.target.value)
                  setResult(undefined)
                }}
              >
                <option value="">不指定部署</option>
                {deployments.map((rule) => (
                  <option key={rule.placementId} value={rule.placementId}>
                    部署 {rule.placementId} · 物理节点 {rule.physicalNodeId ?? '—'}
                  </option>
                ))}
              </select>
            </label>
            <button
              type="button"
              className="button button-primary"
              disabled={busy || !timelineJson || !targetRuleId || isDemoMode}
              onClick={() => void replay()}
            >
              {busy ? '处理中…' : '校验并回放'}
            </button>
          </div>
          <p className="replay-hint">
            目标规则需要运行时 Fact 时，可选择其现有部署作为 Provider
            配置来源。回放不修改现有节点的等待队列。
          </p>
          {library.isError ? (
            <p className="form-error" role="alert">
              读取规则库失败：{errorText(library.error)}
            </p>
          ) : null}
          {scenario.isError ? (
            <p className="form-error" role="alert">
              读取部署失败：{errorText(scenario.error)}
            </p>
          ) : null}
        </div>
      </section>

      {message ? (
        <p className="replay-message replay-message-error" role="alert">
          {message}
        </p>
      ) : null}
      {notice ? (
        <p className="replay-message" role="status">
          {notice}
        </p>
      ) : null}

      {result ? (
        <section className="panel replay-panel">
          <SectionTitle
            title="回放结果"
            detail={`源实例 ${result.instanceId} · 目标规则 ${result.targetRuleId}`}
          />
          {result.status === 'interrupted' ? (
            <p className="replay-message replay-message-error" role="alert">
              回放在序号 {result.interruptedAt ?? '未知'} 中断：{result.error ?? '请查看事件详情。'}
              。以下为已执行的部分结果。
            </p>
          ) : (
            <p className="replay-result-status">回放已完成</p>
          )}
          <div className="replay-stats">
            <div>
              <span>轮次</span>
              <strong>{result.rounds}</strong>
            </div>
            <div>
              <span>匹配调用</span>
              <strong>{result.calls}</strong>
            </div>
            <div>
              <span>成局</span>
              <strong>{result.matches}</strong>
            </div>
            <div>
              <span>种子尝试</span>
              <strong>{result.seedAttempts}</strong>
            </div>
          </div>
          <EventTimeline
            events={result.events ?? noEvents}
            result
            sourceEvents={timeline?.events}
          />
        </section>
      ) : null}
    </div>
  )
}
