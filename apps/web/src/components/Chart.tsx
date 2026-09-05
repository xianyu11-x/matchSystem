import { useEffect, useMemo, useRef, useState } from 'react'
import * as echarts from 'echarts/core'
import { BarChart, LineChart } from 'echarts/charts'
import {
  GridComponent,
  TooltipComponent,
  TitleComponent,
  DataZoomComponent,
  AriaComponent,
} from 'echarts/components'
import { CanvasRenderer, SVGRenderer } from 'echarts/renderers'
import type { ECharts, EChartsOption } from 'echarts'
import { useAllMatches } from '../lib/queries'
import { recentMatchBuckets } from '../lib/analysisChart'
import '../pages/MatchAnalysis.css'
import {
  analysisCSV,
  chartImage,
  downloadAnalysis,
  type AnalysisPoint,
  type AnalysisGrouping,
  type AnalysisStatistic,
} from '../lib/analysisChart'

echarts.use([
  BarChart,
  LineChart,
  GridComponent,
  TooltipComponent,
  TitleComponent,
  DataZoomComponent,
  AriaComponent,
  CanvasRenderer,
  SVGRenderer,
])

export function AnalysisChart({
  points,
  title,
  windowDescription,
  field,
  statistic,
  grouping,
  start,
  end,
  chartType,
}: {
  points: AnalysisPoint[]
  title: string
  windowDescription: string
  field: string
  statistic: AnalysisStatistic
  grouping: AnalysisGrouping
  start?: string
  end?: string
  chartType: 'bar' | 'line'
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<ECharts | undefined>(undefined)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const hasData = points.some((point) => point.value !== null)
  const option = useMemo<EChartsOption>(
    () => ({
      animation: false,
      backgroundColor: '#fff',
      title: {
        text: title,
        subtext: windowDescription,
        left: 16,
        top: 12,
        textStyle: { fontSize: 15 },
        subtextStyle: { fontSize: 10 },
      },
      aria: { enabled: true },
      grid: { left: 30, right: 30, top: 90, bottom: 85, containLabel: true },
      tooltip: { trigger: 'axis', renderMode: 'richText' },
      xAxis: {
        type: 'category',
        data: points.map((point) => point.label),
        axisLabel: { hideOverlap: true },
      },
      yAxis: {
        type: 'value',
        name:
          statistic === 'count'
            ? '样本数'
            : field === 'durationMs'
              ? 'ms'
              : field === 'processingDurationNs'
                ? 'ns'
                : '值',
      },
      dataZoom: [{ type: 'slider', bottom: 12 }],
      series: [
        {
          name: title,
          type: chartType,
          data: points.map((point) => point.value),
          connectNulls: false,
          itemStyle: { color: '#3567f0' },
          barMaxWidth: 36,
        },
      ],
    }),
    [chartType, field, points, statistic, title, windowDescription],
  )

  useEffect(() => {
    if (!containerRef.current || !hasData) return
    const chart = echarts.init(containerRef.current, undefined, { renderer: 'svg' })
    chartRef.current = chart
    const observer = new ResizeObserver(() => chart.resize())
    observer.observe(containerRef.current)
    return () => {
      observer.disconnect()
      chart.dispose()
      chartRef.current = undefined
    }
  }, [hasData])

  useEffect(() => {
    chartRef.current?.setOption(option)
  }, [hasData, option])

  const exportFile = async (format: 'png' | 'svg' | 'csv') => {
    setBusy(true)
    setMessage('')
    setError('')
    try {
      let blob: Blob
      if (format === 'csv') {
        blob = new Blob([analysisCSV(points, { field, statistic, grouping, start, end })], {
          type: 'text/csv;charset=utf-8',
        })
      } else {
        // Render an immutable, full-range snapshot; interactive zoom is navigation only.
        const snapshot = echarts.init(null, undefined, {
          renderer: 'svg',
          ssr: true,
          width: 1400,
          height: 600,
        })
        let svg: string
        try {
          snapshot.setOption(option)
          svg = snapshot.renderToSVGString()
        } finally {
          snapshot.dispose()
        }
        blob = await chartImage(svg, format)
      }
      downloadAnalysis(
        blob,
        `match-analysis-${field.replace(/[^a-zA-Z0-9_-]/g, '_')}-${Date.now()}.${format}`,
      )
      setMessage(`${format.toUpperCase()} 已交给下载管理器，请在下载列表中确认保存。`)
    } catch (cause) {
      setError(`导出失败：${cause instanceof Error ? cause.message : String(cause)}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="match-analysis-chart">
      <div className="match-analysis-export" role="group" aria-label="导出当前分析">
        {(['png', 'svg', 'csv'] as const).map((format) => (
          <button
            key={format}
            className="button button-ghost"
            type="button"
            disabled={busy || points.length === 0 || (format !== 'csv' && !hasData)}
            onClick={() => void exportFile(format)}
          >
            保存 {format.toUpperCase()}
          </button>
        ))}
      </div>
      <p className="analysis-field-description">
        图片和 CSV 导出当前分析范围全部数据；缩放仅调整屏幕视图。Fact
        列表按元素统计，每局图显示所选统计量。
      </p>
      {error && (
        <p role="alert" className="form-error">
          {error}
        </p>
      )}
      <p role="status">{busy ? '正在生成文件…' : message}</p>
      {hasData ? (
        <div
          ref={containerRef}
          className="match-analysis-canvas"
          role="img"
          aria-label={`${title}，${points.length} 组；精确数据见下方表格`}
        />
      ) : (
        <p role="status">
          {points.length === 0
            ? '尚未选择比赛，请勾选比赛或分析窗口全部。'
            : '所选比赛没有此指标的有效样本，缺失值不会补零。'}
        </p>
      )}
      <details>
        <summary>查看图表数据（{points.length} 组）</summary>
        <div className="analysis-table-wrap">
          <table className="analysis-table">
            <caption>{title}</caption>
            <thead>
              <tr>
                <th scope="col">名称</th>
                <th scope="col">比赛数</th>
                <th scope="col">有效样本数</th>
                <th scope="col">值</th>
              </tr>
            </thead>
            <tbody>
              {points.map((point, index) => (
                <tr key={`${index}:${point.label}`}>
                  <th scope="row">{point.label}</th>
                  <td>{point.matchIds.length}</td>
                  <td>{point.samples}</td>
                  <td>{point.value ?? '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  )
}

export function RunMetricsChart() {
  const query = useAllMatches()
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 10_000)
    return () => window.clearInterval(timer)
  }, [])
  const points = useMemo(() => recentMatchBuckets(query.data ?? [], now), [query.data, now])
  if (query.isLoading) return <p role="status">正在读取最近比赛…</p>
  if (query.isError)
    return (
      <div role="alert">
        比赛趋势读取失败。
        <button className="button button-ghost" onClick={() => void query.refetch()}>
          重试
        </button>
      </div>
    )
  if (!points.some((point) => point.value)) return <p role="status">最近 30 分钟没有已保留比赛。</p>
  return (
    <AnalysisChart
      points={points}
      title="成局数量 · 每 5 分钟"
      windowDescription="最近 30 分钟已保留比赛；按成局时间分桶"
      field="matchCount"
      statistic="count"
      grouping="match"
      start={new Date(now - 30 * 60_000).toISOString()}
      end={new Date(now).toISOString()}
      chartType="bar"
    />
  )
}
