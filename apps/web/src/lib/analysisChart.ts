import type { MatchRecord } from '../types'
import { calculateStatistics, valuesForField } from './matchAnalytics'
import { matchInTimeRange } from './matchAnalytics'

export type AnalysisGrouping = 'match' | 'node'
export type AnalysisStatistic = 'mean' | 'p95' | 'min' | 'max' | 'count'
export interface AnalysisPoint {
  label: string
  matchIds: string[]
  samples: number
  value: number | null
}

export function recentMatchBuckets(matches: MatchRecord[], now: number): AnalysisPoint[] {
  const start = now - 30 * 60_000
  return Array.from({ length: 6 }, (_, index) => {
    const from = new Date(start + index * 5 * 60_000)
    const to = new Date(from.getTime() + 5 * 60_000)
    const items = matches.filter((match) => matchInTimeRange(match, from, to))
    return {
      label: from.toLocaleTimeString('zh-CN', {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
      }),
      matchIds: items.map((match) => match.matchId),
      samples: items.length,
      value: items.length,
    }
  })
}

/** One model feeds both the visible chart and exported data. Null is never zero. */
export function analysisPoints(
  matches: MatchRecord[],
  field: string,
  grouping: AnalysisGrouping,
  statistic: AnalysisStatistic,
): AnalysisPoint[] {
  const groups = new Map<string, { label: string; matches: MatchRecord[] }>()
  for (const match of [...matches].sort(
    (a, b) =>
      Date.parse(a.createdAt) - Date.parse(b.createdAt) || a.matchId.localeCompare(b.matchId),
  )) {
    const key =
      grouping === 'match' ? match.matchId : JSON.stringify([match.ruleKey, match.placementId])
    const group = groups.get(key) ?? {
      label: grouping === 'match' ? match.matchId : `${match.ruleKey} / ${match.placementId}`,
      matches: [],
    }
    group.matches.push(match)
    groups.set(key, group)
  }
  return Array.from(groups.values(), (group) => {
    const stats = calculateStatistics(
      group.matches.flatMap((match) => valuesForField(match, field)),
    )
    return {
      label: group.label,
      matchIds: group.matches.map((match) => match.matchId),
      samples: stats?.count ?? 0,
      value: statistic === 'count' ? (stats?.count ?? 0) : (stats?.[statistic] ?? null),
    }
  })
}

function csvCell(value: string | number | null): string {
  const text = value === null ? '' : String(value)
  // Spreadsheet formula injection protection applies only to textual cells.
  const safe = typeof value === 'string' && /^[\s]*[=+\-@]/.test(text) ? `'${text}` : text
  return `"${safe.replaceAll('"', '""')}"`
}

export function analysisCSV(
  points: AnalysisPoint[],
  metadata: { field: string; statistic: string; grouping: string; start?: string; end?: string },
): string {
  const rows: (string | number | null)[][] = [
    [
      '窗口开始（含，UTC）',
      '窗口结束（不含，UTC）',
      '指标',
      '统计量',
      '分组',
      '名称',
      'Match IDs（JSON）',
      '有效样本数',
      '值',
    ],
    ...points.map((point) => [
      metadata.start ?? '',
      metadata.end ?? '',
      metadata.field,
      metadata.statistic,
      metadata.grouping,
      point.label,
      JSON.stringify(point.matchIds),
      point.samples,
      point.value,
    ]),
  ]
  return '\uFEFF' + rows.map((row) => row.map(csvCell).join(',')).join('\r\n') + '\r\n'
}

export function downloadAnalysis(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  try {
    document.body.append(anchor)
    anchor.click()
  } finally {
    anchor.remove()
    window.setTimeout(() => URL.revokeObjectURL(url), 60_000)
  }
}

export async function chartImage(svg: string, format: 'svg' | 'png'): Promise<Blob> {
  const blob = new Blob([svg], { type: 'image/svg+xml;charset=utf-8' })
  if (format === 'svg') return blob
  const url = URL.createObjectURL(blob)
  try {
    const image = new Image()
    image.src = url
    await image.decode()
    const canvas = document.createElement('canvas')
    canvas.width = image.width * 2
    canvas.height = image.height * 2
    const context = canvas.getContext('2d')
    if (!context) throw new Error('无法创建图片画布')
    context.drawImage(image, 0, 0, canvas.width, canvas.height)
    return await new Promise<Blob>((resolve, reject) =>
      canvas.toBlob(
        (result) => (result ? resolve(result) : reject(new Error('PNG 编码失败'))),
        'image/png',
      ),
    )
  } finally {
    URL.revokeObjectURL(url)
  }
}
