import { describe, expect, it } from 'vitest'
import {
  MAX_ROUTE_UPDATE_INTERVAL_MS,
  routeUpdateIntervalFromSeconds,
  routeUpdateIntervalSeconds,
  validateRouteUpdateInterval,
} from './routeUpdateInterval'

describe('路由快照同步间隔', () => {
  it.each([
    ['', undefined],
    ['0', 0],
    ['0.001', 1],
    ['10', 10000],
    ['10.001', 10001],
    ['9223372036.854', MAX_ROUTE_UPDATE_INTERVAL_MS],
  ] as const)('秒数 %s 精确转换到毫秒并还原', (seconds, milliseconds) => {
    expect(routeUpdateIntervalFromSeconds(seconds)).toBe(milliseconds)
    expect(routeUpdateIntervalSeconds(milliseconds)).toBe(seconds)
    expect(() => validateRouteUpdateInterval(milliseconds)).not.toThrow()
  })

  it.each(['-1', '1.0001', '9223372036.855', 'abc', '1e3'])('拒绝超界或非法秒数 %s', (value) => {
    expect(() => routeUpdateIntervalFromSeconds(value)).toThrow()
  })

  it.each([-1, 1.5, NaN, MAX_ROUTE_UPDATE_INTERVAL_MS + 1])('发送前拒绝非法毫秒数 %s', (value) => {
    expect(() => validateRouteUpdateInterval(value)).toThrow()
  })
})
