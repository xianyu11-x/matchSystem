export const MAX_ROUTE_UPDATE_INTERVAL_MS = 9223372036854

export function routeUpdateIntervalFromSeconds(value: string): number | undefined {
  const text = value.trim()
  if (!text) return undefined
  const parts = /^(\d+)(?:\.(\d{1,3}))?$/.exec(text)
  if (!parts) throw new Error('请输入非负秒数，最多保留 3 位小数')
  const milliseconds = BigInt(parts[1]) * 1000n + BigInt((parts[2] ?? '').padEnd(3, '0'))
  if (milliseconds > BigInt(MAX_ROUTE_UPDATE_INTERVAL_MS))
    throw new Error('同步间隔不得超过 9223372036.854 秒')
  return Number(milliseconds)
}

export function routeUpdateIntervalSeconds(milliseconds: number | undefined): string {
  if (milliseconds === undefined) return ''
  const seconds = Math.floor(milliseconds / 1000)
  const fraction = String(milliseconds % 1000)
    .padStart(3, '0')
    .replace(/0+$/, '')
  return fraction ? `${seconds}.${fraction}` : String(seconds)
}

export function validateRouteUpdateInterval(value: number | undefined): void {
  if (
    value !== undefined &&
    (!Number.isSafeInteger(value) || value < 0 || value > MAX_ROUTE_UPDATE_INTERVAL_MS)
  )
    throw new Error('同步间隔必须为 0 至 9223372036854 毫秒之间的整数')
}
