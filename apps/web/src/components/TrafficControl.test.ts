import { expect, it } from 'vitest'
import { fixedVirtualEnd } from './TrafficControl'

it('rejects fixed virtual ranges that exceed JavaScript safe integer precision', () => {
  expect(fixedVirtualEnd(1_700_000_000_000, 3, 1_000)).toBe(1_700_000_002_000)
  expect(fixedVirtualEnd(Number.MAX_SAFE_INTEGER - 1, 2, 1)).toBe(Number.MAX_SAFE_INTEGER)
  expect(fixedVirtualEnd(Number.MAX_SAFE_INTEGER, 2, 1)).toBeUndefined()
  expect(fixedVirtualEnd(0, Number.MAX_SAFE_INTEGER, 2)).toBeUndefined()
})
