import { useState } from 'react'
import type { MatchRecord } from '../types'
import { ticketAttributeAnalysis, ticketAttributeFields } from '../lib/ticketAnalytics'
import { calculateStatistics } from '../lib/matchAnalytics'
import { analysisPoints, type AnalysisStatistic } from '../lib/analysisChart'
import { AnalysisChart } from './Chart'

export function TicketAttributeAnalysis({
  matches,
  available,
  start,
  end,
}: {
  matches: MatchRecord[]
  available: MatchRecord[]
  start?: Date
  end?: Date
}) {
  const fields = ticketAttributeFields(available)
  const [key, setKey] = useState('')
  const [statistic, setStatistic] = useState<AnalysisStatistic>('mean')
  const field = fields.find((item) => item.key === key) ?? fields[0]
  const data = field && ticketAttributeAnalysis(matches, field)
  const stats = data && calculateStatistics(data.numbers)
  const categorical = field?.type === 'strings'
  const fieldKey = field ? `ticket:${field.key}` : ''
  const points =
    !field || !data
      ? []
      : categorical
        ? data.points
        : analysisPoints(matches, fieldKey, 'match', statistic)
  return (
    <section className="panel ticket-attribute-analysis">
      <h2>Ticket 属性分析</h2>
      <p>
        统计当前规则、时间范围及所选比赛中的成员属性快照；不包含尚未成局的等待队列。属性名来自实际历史数据。
      </p>
      {!field ? (
        <p role="status">
          当前范围尚无成员属性快照。生成带属性的 Ticket 并成局后可分析 score、Level、region 等字段。
        </p>
      ) : (
        <>
          <div className="form-grid">
            <label className="field-label">
              Ticket 属性
              <select
                aria-label="Ticket 属性"
                className="filter-select"
                value={field.key}
                onChange={(event) => setKey(event.target.value)}
              >
                {fields.map((item) => (
                  <option key={item.key} value={item.key}>
                    {item.name} · {item.type}
                  </option>
                ))}
              </select>
            </label>
            {!categorical && (
              <label className="field-label">
                每局统计量
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
            )}
          </div>
          <p role="status">
            有属性 {data!.present} 人 · 缺失属性 {data!.missing} 人 · 空列表 {data!.empty} 人 ·
            不可用成员快照 {data!.unavailable} 人 · 排除非精确数值 {data!.excluded} 项
          </p>
          {categorical ? (
            <p>
              柱高为包含该类别的成员数；同一成员的重复值只计一次，多类别成员分别计入各类别。占比以有该属性的成员为分母，合计可超过
              100%。
            </p>
          ) : (
            <p>
              有效样本 {stats?.count ?? 0} · 均值 {stats?.mean.toFixed(2) ?? '—'} · 最小值{' '}
              {stats?.min ?? '—'} · 最大值 {stats?.max ?? '—'} · P95 {stats?.p95.toFixed(2) ?? '—'}
              。数值列表按元素取样，缺失值不补零。
            </p>
          )}
          <AnalysisChart
            points={points}
            title={`${field.name} · ${categorical ? '类别成员分布' : '成员属性统计'}`}
            windowDescription={`${matches.length} 场已选比赛 · Ticket 属性快照`}
            field={fieldKey}
            statistic={categorical ? 'count' : statistic}
            grouping={categorical ? 'category' : 'match'}
            start={start?.toISOString()}
            end={end?.toISOString()}
            chartType="bar"
          />
          {categorical && (
            <div className="analysis-table-wrap">
              <table className="analysis-table">
                <caption>{field.name} 类别占比</caption>
                <thead>
                  <tr>
                    <th>类别</th>
                    <th>成员数</th>
                    <th>占有属性成员比例</th>
                  </tr>
                </thead>
                <tbody>
                  {points.map((point) => (
                    <tr key={point.label}>
                      <td>{point.label}</td>
                      <td>{point.value}</td>
                      <td>
                        {data!.present
                          ? (((point.value ?? 0) / data!.present) * 100).toFixed(2)
                          : '0'}
                        %
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </section>
  )
}
