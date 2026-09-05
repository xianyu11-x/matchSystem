import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { MatchDetailsDrawer } from '../components/MatchDetailsDrawer'
import {
  EmptyState,
  ErrorState,
  LoadingState,
  MetricCard,
  PageHeader,
  SectionTitle,
} from '../components/States'
import { useAllMatches } from '../lib/queries'
import {
  calculateStatistics,
  matchesForAnalysis,
  matchInTimeRange,
  numericFields,
  numericValues,
  valuesForField,
} from '../lib/matchAnalytics'
import { formatDate, formatNumber } from '../lib/format'
import type { MatchRecord } from '../types'
import { AnalysisChart } from '../components/Chart'
import { analysisPoints, type AnalysisGrouping, type AnalysisStatistic } from '../lib/analysisChart'
import './MatchAnalysis.css'

type RangePreset = '1h' | '6h' | '24h' | '7d' | 'all' | 'custom'

const rangeDurations: Record<Exclude<RangePreset, 'all' | 'custom'>, number> = {
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
  '7d': 7 * 24 * 60 * 60 * 1000,
}

const pad = (value: number) => String(value).padStart(2, '0')

function localDateTimeInput(date: Date): string {
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(
    date.getHours(),
  )}:${pad(date.getMinutes())}`
}

function parseDateInput(value: string): Date | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? undefined : date
}

function formatStat(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) return '—'
  return value.toLocaleString('zh-CN', { maximumFractionDigits: 2 })
}

function selectedValueLabel(match: MatchRecord, fieldKey: string): string {
  const values = valuesForField(match, fieldKey)
  return values.length > 0 ? values.map((value) => formatStat(value)).join(' / ') : '—'
}

function windowLabel(start: Date | undefined, end: Date | undefined): string {
  if (!start && !end) return '全部已保留比赛'
  if (!start) return `截至 ${formatDate(end?.toISOString())}`
  if (!end) return `自 ${formatDate(start.toISOString())}`
  return `${formatDate(start.toISOString())} — ${formatDate(end.toISOString())}`
}

function groupMatches(matches: MatchRecord[], fieldKey: string) {
  const groups = new Map<
    string,
    { label: string; count: number; values: number[]; members: number }
  >()
  for (const match of matches) {
    const label = `${match.ruleKey} / ${match.placementId}`
    const key = JSON.stringify([match.ruleKey, match.placementId])
    const group = groups.get(key) ?? { label, count: 0, values: [], members: 0 }
    group.count += 1
    group.members += match.memberCount
    group.values.push(...valuesForField(match, fieldKey))
    groups.set(key, group)
  }
  return Array.from(groups.entries())
    .map(([key, group]) => ({ ...group, key, stats: calculateStatistics(group.values) }))
    .sort((left, right) => right.count - left.count || left.label.localeCompare(right.label))
}

const statCards = [
  ['count', '样本数', '纳入统计的数值样本'],
  ['mean', '均值', '全部样本的算术平均'],
  ['variance', '方差', '总体方差（除以 N）'],
  ['standardDeviation', '标准差', '波动程度'],
  ['min', '最小值', '窗口内观察到的最低值'],
  ['max', '最大值', '窗口内观察到的最高值'],
  ['range', '极差', '最大值 − 最小值'],
  ['median', '中位数', '排序后的第 50 百分位'],
  ['p95', 'P95', '排序后的第 95 百分位'],
] as const

const MAX_RENDERED_MATCHES = 100

export function MatchAnalysis() {
  const matchesQuery = useAllMatches()
  const [preset, setPreset] = useState<RangePreset>('7d')
  const [customStart, setCustomStart] = useState(() =>
    localDateTimeInput(new Date(Date.now() - rangeDurations['7d'])),
  )
  const [customEnd, setCustomEnd] = useState(() => localDateTimeInput(new Date()))
  const [selectedField, setSelectedField] = useState('durationMs')
  const [selectedMatchId, setSelectedMatchId] = useState<string>()
  const [clockMs, setClockMs] = useState(() => Date.now())
  const [selectedIds, setSelectedIds] = useState<Set<string> | undefined>()
  const [showAllMatches, setShowAllMatches] = useState(false)
  const [chartType, setChartType] = useState<'bar' | 'line'>('bar')
  const [grouping, setGrouping] = useState<AnalysisGrouping>('match')
  const [statistic, setStatistic] = useState<AnalysisStatistic>('mean')

  useEffect(() => {
    if (preset === 'all' || preset === 'custom') return
    const updateClock = () => setClockMs(Date.now())
    updateClock()
    const timer = window.setInterval(updateClock, 1000)
    return () => window.clearInterval(timer)
  }, [preset])

  const { start, end, rangeError } = useMemo(() => {
    if (preset === 'all') return { start: undefined, end: undefined, rangeError: '' }
    if (preset === 'custom') {
      const customStartDate = parseDateInput(customStart)
      const customEndDate = parseDateInput(customEnd)
      if (!customStartDate || !customEndDate)
        return {
          start: customStartDate,
          end: customEndDate,
          rangeError: '请输入有效的开始和结束时间。',
        }
      if (customStartDate >= customEndDate)
        return {
          start: customStartDate,
          end: customEndDate,
          rangeError: '开始时间必须早于结束时间。',
        }
      return { start: customStartDate, end: customEndDate, rangeError: '' }
    }
    const now = new Date(clockMs)
    return { start: new Date(now.getTime() - rangeDurations[preset]), end: now, rangeError: '' }
  }, [clockMs, customEnd, customStart, preset])

  const filteredMatches = useMemo(
    () =>
      (matchesQuery.data ?? []).filter((match) =>
        rangeError ? false : matchInTimeRange(match, start, end),
      ),
    [end, matchesQuery.data, rangeError, start],
  )
  const analysisMatches = useMemo(
    () => matchesForAnalysis(filteredMatches, selectedIds),
    [filteredMatches, selectedIds],
  )
  const toggleSelection = (id: string) =>
    setSelectedIds((current) => {
      const next = new Set(current ?? filteredMatches.map((match) => match.matchId))
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  const fields = useMemo(() => numericFields(filteredMatches), [filteredMatches])

  useEffect(() => {
    if (fields.length > 0 && !fields.some((field) => field.key === selectedField))
      setSelectedField(
        fields.some((field) => field.key === 'durationMs') ? 'durationMs' : fields[0].key,
      )
  }, [fields, selectedField])

  useEffect(() => {
    if (!selectedMatchId) return
    if (!filteredMatches.some((match) => match.matchId === selectedMatchId))
      setSelectedMatchId(undefined)
  }, [filteredMatches, selectedMatchId])

  const field = fields.find((item) => item.key === selectedField)
  const values = useMemo(
    () => (field ? numericValues(analysisMatches, field.key) : []),
    [field, analysisMatches],
  )
  const statistics = useMemo(() => calculateStatistics(values), [values])
  const points = useMemo(
    () => analysisPoints(analysisMatches, selectedField, grouping, statistic),
    [analysisMatches, selectedField, grouping, statistic],
  )
  const groupedMatches = useMemo(
    () => (field ? groupMatches(analysisMatches, field.key) : []),
    [field, analysisMatches],
  )
  const totalMembers = analysisMatches.reduce((sum, match) => sum + match.memberCount, 0)
  const averageMembers =
    analysisMatches.length > 0 ? totalMembers / analysisMatches.length : undefined
  const averageDuration = useMemo(() => {
    const durationValues = analysisMatches.flatMap((match) =>
      typeof match.durationMs === 'number' && Number.isSafeInteger(match.durationMs)
        ? [match.durationMs]
        : [],
    )
    return durationValues.length > 0
      ? durationValues.reduce((sum, value) => sum + value, 0) / durationValues.length
      : undefined
  }, [analysisMatches])
  const renderedMatches = showAllMatches
    ? filteredMatches
    : filteredMatches.slice(0, MAX_RENDERED_MATCHES)
  const excludedNumericSamples = analysisMatches.reduce(
    (total, match) => total + (match.excludedNumericSamples ?? 0),
    0,
  )

  const waitStats = calculateStatistics(numericValues(analysisMatches, 'durationMs'))
  const processingStats = calculateStatistics(
    numericValues(analysisMatches, 'processingDurationNs'),
  )
  return (
    <div className="page-stack">
      <PageHeader
        eyebrow="SIMULATOR / ANALYTICS"
        title="最近比赛汇总"
        description="按时间窗口汇总已保留的 Match，并对成员数、耗时和 Match Facts 等数值属性进行分布分析。"
        actions={
          <Link className="button button-ghost" to="/">
            返回运行总览
          </Link>
        }
      />

      <section className="panel analysis-filter-panel">
        <SectionTitle
          title="分析范围"
          detail={matchesQuery.data ? `${windowLabel(start, end)} · 自动刷新 10 秒` : undefined}
        />
        <div className="analysis-controls">
          <div className="range-preset" role="group" aria-label="时间范围">
            {(
              [
                ['1h', '最近 1 小时'],
                ['6h', '最近 6 小时'],
                ['24h', '最近 24 小时'],
                ['7d', '最近 7 天'],
                ['all', '全部'],
                ['custom', '自定义'],
              ] as const
            ).map(([value, label]) => (
              <button
                className={`range-button ${preset === value ? 'active' : ''}`}
                key={value}
                type="button"
                onClick={() => setPreset(value)}
                aria-pressed={preset === value}
              >
                {label}
              </button>
            ))}
          </div>
          {preset === 'custom' ? (
            <div className="analysis-custom-range">
              <label className="field-label">
                开始时间
                <input
                  className="text-input"
                  type="datetime-local"
                  value={customStart}
                  onChange={(event) => setCustomStart(event.target.value)}
                  aria-invalid={Boolean(rangeError)}
                  aria-describedby={rangeError ? 'analysis-range-error' : undefined}
                />
              </label>
              <span className="range-arrow" aria-hidden="true">
                →
              </span>
              <label className="field-label">
                结束时间
                <input
                  className="text-input"
                  type="datetime-local"
                  value={customEnd}
                  onChange={(event) => setCustomEnd(event.target.value)}
                  aria-invalid={Boolean(rangeError)}
                  aria-describedby={rangeError ? 'analysis-range-error' : undefined}
                />
              </label>
            </div>
          ) : null}
          {rangeError ? (
            <p className="form-error analysis-error" id="analysis-range-error" role="alert">
              {rangeError}
            </p>
          ) : null}
        </div>
      </section>

      {matchesQuery.isLoading ? <LoadingState label="正在读取 Match history…" /> : null}
      {matchesQuery.isError ? (
        <ErrorState error={matchesQuery.error} onRetry={() => matchesQuery.refetch()} />
      ) : null}
      {matchesQuery.data && matchesQuery.data.length === 0 ? (
        <EmptyState
          title="暂无已保留的 Match"
          detail="运行一轮 Match Round 后，分析结果会显示在这里。"
        />
      ) : null}

      {matchesQuery.data && matchesQuery.data.length > 0 ? (
        <>
          <section className="metric-grid analysis-summary-grid">
            <MetricCard
              label={selectedIds === undefined ? '窗口内比赛' : '已选比赛'}
              value={formatNumber(analysisMatches.length)}
              detail={windowLabel(start, end)}
              tone="positive"
            />
            <MetricCard
              label="参与 Tickets"
              value={formatNumber(totalMembers)}
              detail="按 Match memberCount 汇总"
            />
            <MetricCard
              label="平均成员数"
              value={formatStat(averageMembers)}
              detail="每场 Match 的成员均值"
            />
            <MetricCard
              label="平均队列等待"
              value={averageDuration === undefined ? '—' : `${formatStat(averageDuration)} ms`}
              detail="最早成员创建到轮次时间；每局一个样本"
            />
            <MetricCard
              label="队列等待 P95"
              value={waitStats ? `${formatStat(waitStats.p95)} ms` : '—'}
              detail={`${waitStats?.count ?? 0} / ${analysisMatches.length} 场有等待数据`}
            />
            <MetricCard
              label="平均匹配处理耗时"
              value={processingStats ? `${formatStat(processingStats.mean / 1e6)} ms` : '—'}
              detail={`${processingStats?.count ?? 0} / ${analysisMatches.length} 场有实测数据`}
            />
            <MetricCard
              label="匹配处理 P95"
              value={processingStats ? `${formatStat(processingStats.p95 / 1e6)} ms` : '—'}
              detail="成功调用的墙钟耗时，不含此前失败尝试"
            />
          </section>
          <section className="panel analysis-selection-panel">
            <SectionTitle
              title="多局聚合范围"
              detail={`窗口 ${filteredMatches.length} 场 · 分析 ${analysisMatches.length} 场`}
            />
            <div className="range-preset" role="group" aria-label="比赛选择范围">
              <button
                type="button"
                className="button button-ghost"
                onClick={() => setSelectedIds(undefined)}
              >
                分析窗口全部
              </button>
              <button
                type="button"
                className="button button-ghost"
                onClick={() =>
                  setSelectedIds(new Set(filteredMatches.map((match) => match.matchId)))
                }
              >
                选中窗口全部
              </button>
              <button
                type="button"
                className="button button-ghost"
                onClick={() => setSelectedIds(new Set())}
              >
                清空选择
              </button>
            </div>
            <p role="status">
              {selectedIds === undefined
                ? '当前分析窗口全部比赛。取消勾选后固定所选集合；也可清空后逐局勾选。'
                : `已选模式：当前窗口内 ${analysisMatches.length} 场参与统计；窗口外和已淘汰记录不参与。`}
            </p>
            <details className="analysis-matches-panel" open>
              <summary>逐局选择与详情</summary>
              <SectionTitle
                title="窗口内比赛"
                detail={`${formatNumber(filteredMatches.length)} 场 · 点击一行查看详情`}
              />
              <div className="analysis-match-list">
                {renderedMatches.map((match) => (
                  <div className="analysis-selectable-match" key={match.matchId}>
                    <input
                      type="checkbox"
                      aria-label={`选择 Match ${match.matchId}`}
                      checked={selectedIds?.has(match.matchId) ?? true}
                      onChange={() => toggleSelection(match.matchId)}
                    />
                    <button
                      className="analysis-match-row"
                      type="button"
                      key={match.matchId}
                      onClick={() => setSelectedMatchId(match.matchId)}
                      aria-label={`打开 Match ${match.matchId} 详情，${match.ruleKey} ${match.placementId}，${formatDate(match.createdAt)}`}
                    >
                      <span className="analysis-match-primary">
                        <strong>{match.matchId}</strong>
                        <small>
                          {match.ruleKey} / {match.placementId}
                        </small>
                      </span>
                      <span>{formatDate(match.createdAt)}</span>
                      <span>{formatNumber(match.memberCount)} 人</span>
                      <span className="analysis-match-value">
                        {selectedValueLabel(match, selectedField)}
                      </span>
                      <span className="analysis-match-open">详情 →</span>
                    </button>
                  </div>
                ))}
              </div>
              {filteredMatches.length > MAX_RENDERED_MATCHES ? (
                <div className="analysis-list-footer">
                  <span role="status">
                    {showAllMatches
                      ? `已显示全部 ${formatNumber(filteredMatches.length)} 场比赛`
                      : `为保证滚动性能，当前显示最近 ${MAX_RENDERED_MATCHES} 场`}
                  </span>
                  <button
                    className="button button-ghost"
                    type="button"
                    aria-expanded={showAllMatches}
                    onClick={() => setShowAllMatches((current) => !current)}
                  >
                    {showAllMatches ? '收起列表' : '显示全部'}
                  </button>
                </div>
              ) : null}
            </details>
            <details className="advanced-options">
              <summary>等待与处理耗时如何计算</summary>{' '}
              <p>
                等待按轮次时间计算，每局采用最早成员等待量。处理耗时仅计成功
                ProduceMatch（成局调用），含 owner
                命令投递、调度、Provider、核心提交与返回；不含轮次准备、锁等待、历史存储、HTTP
                和此前失败尝试。
              </p>
            </details>
          </section>
          {excludedNumericSamples > 0 ? (
            <div className="analysis-unsafe-note" role="status" aria-live="polite">
              已排除 {formatNumber(excludedNumericSamples)} 个无效或超出 JavaScript 安全整数范围的
              int64/uint64 样本（±{Number.MAX_SAFE_INTEGER.toLocaleString('zh-CN')}
              ），这些值不会进入统计。
            </div>
          ) : null}

          {filteredMatches.length === 0 ? (
            <section className="panel">
              <EmptyState title="当前时间范围没有 Match" detail="可以扩大时间范围或选择“全部”。" />
            </section>
          ) : fields.length === 0 ? (
            <section className="panel">
              <EmptyState
                title="当前范围没有可分析的数值属性"
                detail="Match 至少需要 memberCount、round、durationMs 或数值型 Match Fact。"
              />
            </section>
          ) : (
            <>
              <section className="panel analysis-stat-panel">
                <SectionTitle
                  title="数值属性分析"
                  detail={`${analysisMatches.length} 场比赛 · ${values.length} 个数值样本`}
                  action={
                    <label className="analysis-field-picker">
                      <span>分析属性</span>
                      <select
                        className="filter-select"
                        value={selectedField}
                        onChange={(event) => setSelectedField(event.target.value)}
                        aria-label="选择数值分析属性"
                      >
                        {fields.map((item) => (
                          <option value={item.key} key={item.key}>
                            {item.label}
                          </option>
                        ))}
                      </select>
                    </label>
                  }
                />
                <p className="analysis-field-description">{field?.description}</p>
                <div className="match-analysis-form">
                  <label className="field-label">
                    图表类型
                    <select
                      className="filter-select"
                      value={chartType}
                      onChange={(event) => setChartType(event.target.value as 'bar' | 'line')}
                    >
                      <option value="bar">柱状图</option>
                      <option value="line">折线图</option>
                    </select>
                  </label>
                  <label className="field-label">
                    分组方式
                    <select
                      className="filter-select"
                      value={grouping}
                      onChange={(event) => setGrouping(event.target.value as AnalysisGrouping)}
                    >
                      <option value="match">逐局（按成局时间）</option>
                      <option value="node">Rule / Placement（规则 / 放置组）</option>
                    </select>
                  </label>
                  <label className="field-label">
                    统计量
                    <select
                      className="filter-select"
                      value={statistic}
                      onChange={(event) => setStatistic(event.target.value as AnalysisStatistic)}
                    >
                      <option value="mean">均值</option>
                      <option value="p95">P95</option>
                      <option value="min">最小值</option>
                      <option value="max">最大值</option>
                      <option value="count">有效样本数</option>
                    </select>
                  </label>
                </div>
                <AnalysisChart
                  points={points}
                  title={`${field?.label ?? selectedField} · ${{ mean: '均值', p95: 'P95', min: '最小值', max: '最大值', count: '有效样本数' }[statistic]}`}
                  windowDescription={`${windowLabel(start, end)} · ${analysisMatches.length} 场`}
                  field={selectedField}
                  statistic={statistic}
                  grouping={grouping}
                  start={start?.toISOString()}
                  end={end?.toISOString()}
                  chartType={chartType}
                />
                <details className="advanced-options">
                  <summary>详细统计 · 方差、分位数与分布</summary>
                  <div className="analysis-stat-grid">
                    {statCards.map(([key, label, detail]) => (
                      <article className="analysis-stat-card" key={key}>
                        <span>{label}</span>
                        <strong>{formatStat(statistics?.[key])}</strong>
                        <small>{detail}</small>
                      </article>
                    ))}
                  </div>
                  <div className="analysis-stat-note">
                    <span className="analysis-note-mark">i</span>
                    <p>
                      方差与标准差基于当前分析范围内的数值样本计算；数值型 Fact
                      列表会按每个元素作为一个样本。 P95 使用排序后的线性插值。
                    </p>
                  </div>
                </details>
              </section>

              <section className="content-grid analysis-lower-grid">
                <div className="panel analysis-groups-panel">
                  <SectionTitle
                    title="按 Rule / Placement 聚合"
                    detail="比较各逻辑节点的样本规模与均值"
                  />
                  <div className="analysis-table-wrap">
                    <table className="analysis-table">
                      <caption className="sr-only">
                        按 Rule / Placement 聚合的当前数值属性统计
                      </caption>
                      <thead>
                        <tr>
                          <th scope="col">Rule / Placement</th>
                          <th scope="col">比赛数</th>
                          <th scope="col">样本数</th>
                          <th scope="col">均值</th>
                          <th scope="col">极差</th>
                        </tr>
                      </thead>
                      <tbody>
                        {groupedMatches.map((group) => (
                          <tr key={group.key}>
                            <td>
                              <strong>{group.label}</strong>
                            </td>
                            <td>{formatNumber(group.count)}</td>
                            <td>{formatNumber(group.stats?.count ?? 0)}</td>
                            <td>{formatStat(group.stats?.mean)}</td>
                            <td>{formatStat(group.stats?.range)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </div>

                <div className="panel analysis-insight-panel">
                  <SectionTitle title="窗口洞察" detail="基于当前所选属性" />
                  <div className="analysis-insights">
                    <div>
                      <span>波动系数</span>
                      <strong>
                        {statistics && statistics.mean !== 0
                          ? `${formatStat((statistics.standardDeviation / Math.abs(statistics.mean)) * 100)}%`
                          : '—'}
                      </strong>
                      <small>标准差 ÷ |均值|</small>
                    </div>
                    <div>
                      <span>中位数偏差</span>
                      <strong>
                        {statistics ? formatStat(statistics.mean - statistics.median) : '—'}
                      </strong>
                      <small>均值 − 中位数</small>
                    </div>
                    <div>
                      <span>最新比赛</span>
                      <strong>{formatDate(analysisMatches[0]?.createdAt)}</strong>
                      <small>{analysisMatches[0]?.matchId ?? '—'}</small>
                    </div>
                  </div>
                </div>
              </section>
            </>
          )}
        </>
      ) : null}

      <MatchDetailsDrawer matchId={selectedMatchId} onClose={() => setSelectedMatchId(undefined)} />
    </div>
  )
}
