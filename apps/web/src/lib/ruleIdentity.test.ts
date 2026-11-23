import { describe, expect, it } from 'vitest'
import { parseEditableJson, parseRuleKey, ruleIdentity, ruleIdentityLabel, uint64Text } from './ruleIdentity'
import { queryKeys } from './queries'

describe('规范十进制身份', () => {

  it('原始规则数值不丢失精度，旧场景导入拒绝其他不安全整数', () => {
    expect(parseEditableJson('{"ruleId":9007199254740993,"ratio":1.5}')).toEqual({
      ruleId: '9007199254740993',
      ratio: 1.5,
    })
    expect(() => parseEditableJson('{"physicalNodes":[{"id":9007199254740993}]}')).toThrow(
      /无法精确表示的整数/,
    )
    expect(() => parseEditableJson('{"facts":{"value":9223372036854775807}}')).toThrow(
      /无法精确表示的整数/,
    )
  })
  it('精确保留最大值和相邻值，并接受安全整数', () => {
    expect(uint64Text('18446744073709551615')).toBe('18446744073709551615')
    expect(uint64Text('18446744073709551614')).toBe('18446744073709551614')
    expect(uint64Text(42, true)).toBe('42')
    expect(parseRuleKey({ namespace: '0', ruleId: 1 })).toEqual({ namespace: '0', ruleId: '1' })
    expect(parseRuleKey({ ruleId: '2' })).toEqual({ ruleId: '2' })
    expect(
      parseRuleKey({ namespace: '18446744073709551615', ruleId: '18446744073709551614' }),
    ).toEqual({ namespace: '18446744073709551615', ruleId: '18446744073709551614' })
  })

  it('拒绝前导零、旧名称、空白、溢出和无法精确表示的数字', () => {
    for (const value of [
      '',
      '00',
      '01',
      'name',
      '1\n',
      '1\r',
      ' 1',
      '+1',
      '-1',
      '18446744073709551616',
      9007199254740992,
      -1,
      -0,
      null,
    ]) {
      expect(uint64Text(value)).toBeUndefined()
    }
    for (const value of ['0', 0, '01', 'name', '18446744073709551616'])
      expect(parseRuleKey({ ruleId: value })).toBeUndefined()
    for (const value of [null, 0, 1, {}, []])
      expect(parseRuleKey({ namespace: value, ruleId: '1' })).toBeUndefined()
  })

  it('任意文本别名不参与规则身份，但可完整显示', () => {
    for (const namespace of ['', '0', '01', '1\n', '别名/中文:1', '18446744073709551616']) {
      const key = parseRuleKey({ namespace, ruleId: '7' })!
      expect(key).toEqual({ namespace, ruleId: '7' })
      expect(ruleIdentity(key)).toBe('7')
    }
    expect(ruleIdentityLabel({ namespace: '0', ruleId: '7' })).toBe('0/7')
    expect(ruleIdentity({ namespace: 'same', ruleId: '7' })).not.toBe(
      ruleIdentity({ namespace: 'same', ruleId: '8' }),
    )
    expect(queryKeys.logicalNodeFacts({ namespace: '旧/别名', ruleId: '7' }, '3')).toEqual(
      queryKeys.logicalNodeFacts({ namespace: '新:别名', ruleId: '7' }, '3'),
    )
    expect(queryKeys.logicalNodeFacts({ namespace: 'same', ruleId: '7' }, '3')).not.toEqual(
      queryKeys.logicalNodeFacts({ namespace: 'same', ruleId: '8' }, '3'),
    )
  })
})
