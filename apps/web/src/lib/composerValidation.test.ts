import { describe, expect, it } from 'vitest'
import { parseInputValue, parseSafeInteger, validateBatch } from './composerValidation'
import type { BatchGeneratorSpec } from '../types'

const spec = (
  attributeGenerators: BatchGeneratorSpec['attributeGenerators'],
): BatchGeneratorSpec => ({
  count: 10,
  seed: 42,
  startTicketId: 100,
  ruleKey: 'r',
  attributeGenerators,
})

describe('composer input validation', () => {
  it('validates configurable normal parameters for batch and continuous sampling', () => {
    const g = {
      type: 'int64' as const,
      min: '0',
      max: '100',
      distribution: 'normal' as const,
      mean: 50,
      stdDev: 10,
    }
    expect(() => validateBatch(spec({ level: g }))).not.toThrow()
    expect(() => validateBatch(spec({ level: g }), true)).not.toThrow()
    for (const stdDev of [0, -1, NaN, Infinity])
      expect(() => validateBatch(spec({ level: { ...g, stdDev } }))).toThrow()
    expect(() => validateBatch(spec({ level: { ...g, mean: 101 } }))).toThrow()
    expect(() => validateBatch(spec({ level: { ...g, max: '9223372036854775807' } }))).toThrow()
  })
  it('keeps exact string candidates including empty strings, commas and whitespace', () => {
    expect(() =>
      validateBatch(spec({ tags: { type: 'strings', values: ['', 'a,b', ' a', 'a'], count: 4 } })),
    ).not.toThrow()
    expect(() =>
      validateBatch(spec({ tags: { type: 'strings', values: ['a', 'a'], count: 2 } })),
    ).toThrow('超过')
  })
  it('accepts Chinese list separators without dropping invalid numeric values', () => {
    expect(parseInputValue('1，2,3', 'uint64s', 'IDs')).toEqual([1, 2, 3])
    expect(() => parseInputValue('1,wrong,3', 'uint64s', 'IDs')).toThrow('IDs')
    expect(() => parseInputValue('9007199254740993', 'int64', 'level')).toThrow('level')
    expect(() => parseSafeInteger('', '数量', 1)).toThrow()
  })
  it('supports exact interval cardinality even with overlapping uint64 ranges', () => {
    const g = {
      type: 'uint64s' as const,
      set: '18446744073709551613-18446744073709551615，18446744073709551614',
      count: 3,
    }
    expect(() => validateBatch(spec({ ids: g }))).not.toThrow()
    expect(() => validateBatch(spec({ ids: { ...g, count: 4 } }))).toThrow('超过')
    expect(() => validateBatch(spec({ ids: { ...g, count: 4, replacement: true } }))).not.toThrow()
  })
  it('rejects shared cycles and disabled sources and preserves valid chains', () => {
    expect(() =>
      validateBatch(spec({ a: { type: 'strings', source: 'shared', ref: 'b' } })),
    ).toThrow('来源')
    expect(() =>
      validateBatch(
        spec({
          a: { type: 'strings', source: 'shared', ref: 'b' },
          b: { type: 'strings', source: 'shared', ref: 'a' },
        }),
      ),
    ).toThrow('循环')
    expect(() =>
      validateBatch(
        spec({
          a: { type: 'strings', source: 'shared', ref: 'b' },
          b: { type: 'strings', source: 'ticketId' },
        }),
      ),
    ).not.toThrow()
  })
  it('checks signed bounds and end ticket ID and ignores batch count only for traffic', () => {
    expect(() =>
      validateBatch(
        spec({ level: { type: 'int64', min: '-9223372036854775808', max: '9223372036854775807' } }),
      ),
    ).not.toThrow()
    expect(() => validateBatch(spec({ level: { type: 'int64', min: '9', max: '1' } }))).toThrow(
      '最小值',
    )
    expect(() => validateBatch({ ...spec({}), startTicketId: Number.MAX_SAFE_INTEGER })).toThrow(
      '最后一条',
    )
    expect(() => validateBatch({ ...spec({}), count: 0 })).toThrow('数量')
    expect(() => validateBatch({ ...spec({}), count: 0 }, true)).not.toThrow()
  })
})
