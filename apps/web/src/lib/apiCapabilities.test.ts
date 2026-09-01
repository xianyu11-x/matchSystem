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
  })
})
