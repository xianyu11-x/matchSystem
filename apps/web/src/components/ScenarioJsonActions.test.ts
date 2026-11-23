import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../lib/api'
import { parseScenarioJson, scenarioDocumentText, ScenarioJsonActions } from './ScenarioJsonActions'

afterEach(() => vi.unstubAllGlobals())

it('接受完整场景与 HTTP 外层包装，拒绝规则 JSON', () => {
  const scenario = { schemaVersion: 'simulator-scenario/v1', rules: [] }
  expect(parseScenarioJson(scenario)).toEqual(scenario)
  expect(parseScenarioJson({ scenario, revision: '1' })).toEqual(scenario)
  expect(() => parseScenarioJson({ schemaVersion: 'match-rule/v1' })).toThrow(/simulator-scenario/)
})

it.each([
  ['physicalNodes[0].id', '{"physicalNodes":[{"id":9007199254740992}]}'],
  ['rules[0].physicalNodeId', '{"rules":[{"physicalNodeId":9007199254740992}]}'],
  [
    'rules[0].logicalNode.placementId',
    '{"rules":[{"logicalNode":{"placementId":9007199254740992}}]}',
  ],
  [
    'rules[0].logicalNode.rule.ruleId',
    '{"rules":[{"logicalNode":{"rule":{"ruleId":9007199254740992}}}]}',
  ],
  ['rules[0].rule.ruleKey.ruleId', '{"rules":[{"rule":{"ruleKey":{"ruleId":9007199254740992}}}]}'],
])('导入拒绝 %s 的不安全数值', (path, fields) => {
  const text = `{ "schemaVersion":"simulator-scenario/v1", ${fields.slice(1, -1)} }`
  expect(() => scenarioDocumentText(text)).toThrow(`${path} 不能使用不安全的 JSON 数值`)
  expect(() => scenarioDocumentText(text)).toThrow('十进制字符串')
})

it('导入保留相邻大身份字符串，并接受安全数值', () => {
  const scenario = `{
    "schemaVersion":"simulator-scenario/v1",
    "physicalNodes":[{"id":"9007199254740992"},{"id":"9007199254740993"},{"id":"9"},{"id":7}],
    "rules":[
      {"physicalNodeId":"9007199254740992","logicalNode":{"placementId":"18446744073709551615","rule":{"namespace":"1","ruleId":"9007199254740992"}},"rule":{"ruleKey":{"namespace":"1","ruleId":"9007199254740992"}}},
      {"physicalNodeId":"9007199254740993","logicalNode":{"placementId":8,"rule":{"ruleId":"9007199254740993"}},"rule":{"ruleKey":{"ruleId":"9007199254740993"}}}
    ]
  }`
  expect(scenarioDocumentText(`{"scenario":${scenario}}`)).toBe(scenario)
  const parsed = parseScenarioJson(JSON.parse(scenario))
  const physical = parsed.physicalNodes as Array<{ id: string }>
  expect(physical[0].id).toBe('9007199254740992')
  expect(physical[1].id).toBe('9007199254740993')
  expect(physical[0].id).not.toBe(physical[1].id)
})

it.each([
  ['physicalNodes', { physicalNodes: [{ id: '01' }] }],
  ['physicalNodes', { physicalNodes: [{ id: 'old-node' }] }],
  ['physicalNodes', { physicalNodes: [{ id: '1\n' }] }],
  ['physicalNodeId', { rules: [{ physicalNodeId: '0' }] }],
  ['placementId', { rules: [{ logicalNode: { placementId: 'old-placement' } }] }],
  ['rule.ruleId', { rules: [{ logicalNode: { rule: { ruleId: '01' } } }] }],
  ['rule.namespace', { rules: [{ logicalNode: { rule: { namespace: 1 } } }] }],
  ['ruleKey.namespace', { rules: [{ rule: { ruleKey: { namespace: null } } }] }],
])('导入拒绝 %s 的旧文本或非规范身份', (field, fields) => {
  expect(() => parseScenarioJson({ schemaVersion: 'simulator-scenario/v1', ...fields })).toThrow(
    field,
  )
})

it('导入保留包含斜杠、冒号和 0 的文本别名', () => {
  const text =
    '{"schemaVersion":"simulator-scenario/v1","rules":[{"logicalNode":{"rule":{"namespace":"0/别名:1","ruleId":"7"}},"rule":{"ruleKey":{"namespace":"0","ruleId":"7"}}}]}'
  expect(scenarioDocumentText(text)).toBe(text)
})

it('导出原始响应并精确保留场景内超出安全整数范围的数值', async () => {
  const scenario =
    '{"schemaVersion":"simulator-scenario/v1","rules":[],"large":9223372036854775807,"note":"} \\\" end"}'
  const response = `{ "revision": "r1", "scenario": ${scenario} }`
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(
      new Response(response, { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )
    .mockResolvedValueOnce(
      new Response(response, { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )
  vi.stubGlobal('fetch', fetch)
  expect(await api.getScenarioJsonText()).toBe(response)
  const extracted = scenarioDocumentText(response)
  expect(extracted).toBe(scenario)
  expect(scenarioDocumentText(scenario)).toBe(scenario)
  await api.replaceScenarioJsonText(extracted)
  expect(fetch.mock.calls[1][1].body).toBe(`{"scenario":${scenario}}`)
  expect(fetch.mock.calls[1][1].body).toContain('9223372036854775807')
})

it('运行场景横幅提供独立导入导出操作', () => {
  const html = renderToStaticMarkup(
    createElement(QueryClientProvider, {
      client: new QueryClient(),
      children: createElement(ScenarioJsonActions, {
        scenario: { schemaVersion: 'simulator-scenario/v1', rules: [] },
      }),
    }),
  )
  expect(html).toContain('导入场景')
  expect(html).toContain('导出场景')
})
