import type { JsonObject, JsonValue } from '../types'

export function expressionAt(root: JsonObject, path: string[]): JsonObject | undefined {
  let current: JsonValue | undefined = root
  for (const key of path) {
    if (!current || typeof current !== 'object') return undefined
    current = Array.isArray(current) ? current[Number(key)] : current[key]
  }
  return current && typeof current === 'object' && !Array.isArray(current) ? current : undefined
}

export function replaceExpression(root: JsonObject, path: string[], next: JsonObject): JsonObject {
  function replace(value: JsonValue, remaining: string[]): JsonValue {
    if (!remaining.length) return next
    const [key, ...rest] = remaining
    if (Array.isArray(value))
      return value.map((item, index) => (index === Number(key) ? replace(item, rest) : item))
    const record = value && typeof value === 'object' ? value : {}
    return { ...record, [key]: replace(record[key] ?? {}, rest) }
  }
  return replace(root, path) as JsonObject
}
