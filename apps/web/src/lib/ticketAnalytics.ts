import type { MatchRecord } from '../types'
import type { AnalysisPoint } from './analysisChart'

export function ticketAttributeFields(matches: MatchRecord[]) {
  const fields = new Map<
    string,
    { key: string; name: string; type: 'int64' | 'uint64s' | 'strings' }
  >()
  for (const match of matches)
    for (const member of match.members ?? []) {
      for (const type of ['int64', 'uint64s', 'strings'] as const)
        for (const name of Object.keys(member.attributes[type])) {
          const key = JSON.stringify([type, name])
          fields.set(key, { key, name, type })
        }
    }
  return [...fields.values()].sort(
    (a, b) => a.name.localeCompare(b.name) || a.type.localeCompare(b.type),
  )
}

export function ticketAttributeAnalysis(
  matches: MatchRecord[],
  field: ReturnType<typeof ticketAttributeFields>[number],
) {
  const numbers: number[] = []
  const categories = new Map<string, { count: number; ids: Set<string> }>()
  let present = 0,
    missing = 0,
    empty = 0,
    excluded = 0
  for (const match of matches)
    for (const member of match.members ?? []) {
      const value = member.attributes[field.type][field.name]
      if (value === undefined) {
        missing++
        continue
      }
      present++
      const values = Array.isArray(value) ? value : [value]
      if (!values.length) empty++
      if (field.type === 'strings') {
        // A member contributes at most once to each category, including an empty string.
        for (const category of new Set(values.map(String))) {
          const bucket = categories.get(category) ?? { count: 0, ids: new Set<string>() }
          bucket.count++
          bucket.ids.add(match.matchId)
          categories.set(category, bucket)
        }
      } else
        for (const item of values) {
          if (typeof item === 'number' && Number.isSafeInteger(item)) numbers.push(item)
          else excluded++
        }
    }
  const points: AnalysisPoint[] = [...categories.entries()]
    .sort((a, b) => b[1].count - a[1].count || a[0].localeCompare(b[0]))
    .map(([label, bucket]) => ({
      label: label === '' ? '""（空字符串）' : JSON.stringify(label),
      matchIds: [...bucket.ids],
      samples: bucket.count,
      value: bucket.count,
    }))
  const unavailable = matches.reduce(
    (sum, match) => sum + Math.max(0, match.memberCount - (match.members?.length ?? 0)),
    0,
  )
  return { numbers, points, present, missing, empty, excluded, unavailable }
}
