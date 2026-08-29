import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { BarChart, LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { ECharts, EChartsOption } from 'echarts'

echarts.use([BarChart, LineChart, GridComponent, TooltipComponent, CanvasRenderer])

export function RunMetricsChart() {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<ECharts | undefined>(undefined)

  useEffect(() => {
    if (!containerRef.current) return undefined
    const chart = echarts.init(containerRef.current, undefined, { renderer: 'canvas' })
    chartRef.current = chart
    const option: EChartsOption = {
      animation: false,
      grid: { left: 12, right: 12, top: 16, bottom: 8, containLabel: true },
      tooltip: { trigger: 'axis' },
      xAxis: { type: 'category', data: ['08:30', '08:35', '08:40', '08:45', '08:50', '08:55'] },
      yAxis: { type: 'value', splitLine: { lineStyle: { color: '#e6eaf0' } } },
      series: [
        {
          name: '匹配数',
          type: 'bar',
          data: [18, 22, 16, 31, 28, 36],
          barMaxWidth: 16,
          itemStyle: { color: '#3567f0', borderRadius: [4, 4, 0, 0] },
        },
        {
          name: 'P95 延迟',
          type: 'line',
          yAxisIndex: 0,
          data: [34, 39, 36, 42, 38, 41],
          smooth: true,
          symbol: 'none',
          lineStyle: { color: '#e58a3a', width: 2 },
        },
      ],
    }
    chart.setOption(option)
    const resize = () => chart.resize()
    window.addEventListener('resize', resize)
    return () => {
      window.removeEventListener('resize', resize)
      chart.dispose()
      chartRef.current = undefined
    }
  }, [])

  return <div className="chart-wrap" ref={containerRef} aria-label="最近运行轮次匹配趋势" />
}
