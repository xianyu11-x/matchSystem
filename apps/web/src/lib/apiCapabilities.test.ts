import { describe, expect, it } from 'vitest'
import { capabilitiesFromWire } from './api'

describe('wire capability graph metadata', () => {
  it('uses the expression schema arity and exposes variadic metadata', () => {
    const capabilities = capabilitiesFromWire({
      schemaVersions: ['expression-scalar/v3'],
      scalarOperators: [
        {
          name: 'int64_step',
          resultType: 'int64',
          inputs: ['int64'],
          fields: ['op', 'input', 'steps'],
        },
        {
          name: 'strings_contains',
          resultType: 'bool',
          inputs: ['strings'],
          fields: ['op', 'values', 'needle'],
        },
        {
          name: 'uint64s_contains',
          resultType: 'bool',
          inputs: ['uint64s'],
          fields: ['op', 'values', 'needle'],
        },
        {
          name: 'strings_is_empty',
          resultType: 'bool',
          inputs: ['strings'],
          fields: ['op', 'values'],
        },
        {
          name: 'uint64s_is_empty',
          resultType: 'bool',
          inputs: ['uint64s'],
          fields: ['op', 'values'],
        },
        { name: 'bool_and', resultType: 'bool', inputs: ['bool[]'], fields: ['op', 'children'] },
      ],
      bitmapOperators: [],
    })
    const find = (op: string) => capabilities.nodeTypes.find((node) => node.op === op)!

    expect(find('int64_step')).toMatchObject({
      inputTypes: ['int64'],
      maxInputs: 1,
      variadic: false,
    })
    expect(find('strings_contains')).toMatchObject({
      type: 'compare.strings',
      inputTypes: ['strings'],
      maxInputs: 1,
      variadic: false,
    })
    expect(find('uint64s_contains')).toMatchObject({
      type: 'compare.uint64',
      inputTypes: ['uint64s'],
      maxInputs: 1,
      variadic: false,
    })
    expect(find('strings_is_empty')).toMatchObject({ inputTypes: ['strings'], maxInputs: 1 })
    expect(find('uint64s_is_empty')).toMatchObject({ inputTypes: ['uint64s'], maxInputs: 1 })
    expect(find('bool_and')).toMatchObject({
      inputTypes: ['bool'],
      maxInputs: 16,
      variadic: true,
      variadicInputType: 'bool',
    })
    expect(find('strings_contains').description).toContain('needle')
  })

  it('gives lookup_range and unknown server operators actionable descriptions', () => {
    const capabilities = capabilitiesFromWire({
      schemaVersions: ['prefilter/v3'],
      scalarOperators: [
        {
          name: 'future_scalar_op',
          resultType: 'bool',
          inputs: ['int64'],
          fields: ['op', 'threshold'],
        },
      ],
      bitmapOperators: [
        {
          name: 'lookup_range',
          resultType: 'bitmap',
          inputs: ['int64', 'int64'],
          fields: ['op', 'index', 'min', 'max'],
        },
      ],
    })
    const lookup = capabilities.nodeTypes.find((node) => node.op === 'lookup_range')!
    const unknown = capabilities.nodeTypes.find((node) => node.op === 'future_scalar_op')!
    expect(lookup.description).toContain('int64_range')
    expect(lookup.description).toContain('最小值')
    expect(lookup.description).toContain('最大值')
    expect(unknown.description).toContain('输入')
    expect(unknown.description).toContain('输出')
    expect(unknown.description).not.toBe('服务端 capability：future_scalar_op')
  })
})
