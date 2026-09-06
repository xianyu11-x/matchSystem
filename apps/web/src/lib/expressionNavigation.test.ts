import { describe, expect, it } from 'vitest'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { ExpressionEditor } from '../components/ExpressionEditor'
import { demoRule } from './mockData'
import { expressionAt, replaceExpression } from './expressionNavigation'

describe('single-level expression navigation', () => {
  it('updates deep array and envelope paths without losing siblings or metadata', () => {
    const root = {
      op: 'and',
      children: [
        {
          op: 'lookup_range',
          min: {
            schemaVersion: 'expression-scalar/v3',
            resultType: 'int64',
            expr: { op: 'int64_literal', value: 1 },
          },
          max: { op: 'int64_literal', value: 9 },
        },
      ],
    }
    const next = replaceExpression(root, ['children', '0', 'min', 'expr'], {
      op: 'int64_literal',
      value: 5,
    })
    expect(expressionAt(next, ['children', '0', 'min', 'expr'])?.value).toBe(5)
    expect(expressionAt(root, ['children', '0', 'min', 'expr'])?.value).toBe(1)
    expect(expressionAt(next, ['children', '0', 'min'])?.schemaVersion).toBe('expression-scalar/v3')
    expect(expressionAt(next, ['children', '0', 'max'])?.value).toBe(9)
    expect(expressionAt(next, ['children', '8'])).toBeUndefined()
  })
  it('renders child links instead of nested operation forms', () => {
    const html = renderToStaticMarkup(
      createElement(ExpressionEditor, {
        value: {
          op: 'int64_sub',
          left: { op: 'int64_ref', source: 'seed_attributes', name: 'score' },
          right: { op: 'int64_literal', value: 2 },
        },
        type: 'int64',
        contract: demoRule.contract,
        onChange: () => {},
      }),
    )
    expect(html.match(/class="rule-expression"/g)).toHaveLength(1)
    expect(html).toContain('跳转到左侧输入')
    expect(html).toContain('跳转到右侧输入')
    expect(html).not.toContain('关联字段')
  })
})
