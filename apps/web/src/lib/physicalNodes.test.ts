import { describe, expect, it } from 'vitest'
import {
  keyFromSelection,
  newDeploymentForm,
  newPhysicalNodeForm,
  physicalNodeFormFromScenario,
  physicalNodeRequest,
  physicalNodeRuleOptions,
} from './physicalNodes'
import { newPortableRule } from './ruleLibrary'
import { ruleIdentity } from './ruleIdentity'
import type { JsonObject } from '../types'

const scenario: JsonObject = {
  physicalNodes: [
    { id: '1', endpoint: 'inproc://existing', enabled: true, selector: 'largest_queue' },
    { id: '2', endpoint: 'inproc://other', enabled: true },
  ],
  rules: [
    {
      logicalNode: { rule: { namespace: '1', ruleId: 4 }, placementId: '11' },
      physicalNodeId: '1',
      enabled: false,
      weight: 5,
      rule: { ruleKey: { namespace: '1', ruleId: 4 }, marker: ['do not copy'] },
      tickFacts: { int64s: { wait: 3 } },
      pythonProvider: { script: 'test.py', timeoutMs: 100 },
    },
    {
      logicalNode: { rule: { namespace: '1', ruleId: 5 }, placementId: '12' },
      physicalNodeId: '2',
      enabled: true,
      weight: 1,
    },
  ],
}
const rule4 = newPortableRule({ namespace: '1', ruleId: 4 })
const rule5 = newPortableRule({ namespace: '1', ruleId: 5 })
const key4 = ruleIdentity({ namespace: '1', ruleId: 4 })
const key5 = ruleIdentity({ namespace: '1', ruleId: 5 })

describe('运行时物理节点请求', () => {
  it('creates multiple independent deployments from library keys without embedding rule documents', () => {
    const form = {
      ...newPhysicalNodeForm(),
      id: '4',
      deployments: [
        { ...newDeploymentForm(), ruleId: key4, placementId: '13', weight: '2' },
        { ...newDeploymentForm(), ruleId: key5, placementId: '14', enabled: false },
      ],
    }
    expect(physicalNodeRuleOptions([rule4, rule5])).toEqual([
      { value: key4, label: '1/4' },
      { value: key5, label: '1/5' },
    ])
    expect(physicalNodeRequest(form, scenario, [rule4, rule5])).toEqual({
      physicalNode: {
        id: form.id,
        endpoint: 'inproc://4',
        enabled: true,
        selector: 'round_robin',
      },
      deployments: [
        {
          logicalNode: { rule: { namespace: '1', ruleId: '4' }, placementId: '13' },
          enabled: true,
          weight: 2,
        },
        {
          logicalNode: { rule: { namespace: '1', ruleId: '5' }, placementId: '14' },
          enabled: false,
          weight: 1,
        },
      ],
    })
  })

  it('新增节点使用完整的 uint64 十进制标识生成进程内端点', () => {
    const id = '18446744073709551615'
    const form = { ...newPhysicalNodeForm(), id }
    expect(form).not.toHaveProperty('endpoint')
    expect(physicalNodeRequest(form, scenario, []).physicalNode).toEqual({
      id,
      endpoint: `inproc://${id}`,
      enabled: true,
      selector: 'round_robin',
    })
  })

  it('preserves deployment facts and provider with a missing library rule, and reloads only explicitly', () => {
    const before = structuredClone(scenario)
    const form = physicalNodeFormFromScenario(scenario, '1')
    const request = physicalNodeRequest(form, scenario, [])
    expect(request.physicalNode).toEqual({
      id: '1',
      endpoint: 'inproc://existing',
      enabled: true,
      selector: 'largest_queue',
    })
    expect(request.deployments).toEqual([
      {
        logicalNode: { rule: { namespace: '1', ruleId: '4' }, placementId: '11' },
        enabled: false,
        weight: 5,
        keepTickFacts: true,
        pythonProvider: { script: 'test.py', timeoutMs: 100 },
      },
    ])
    expect(scenario).toEqual(before)
    form.deployments[0].applyLatest = true
    expect(() => physicalNodeRequest(form, scenario, [])).toThrow('不在规则库')
    expect(physicalNodeRequest(form, scenario, [rule4]).deployments[0].applyLatest).toBe(true)
    form.deployments[0].placementId = '3'
    const changedIdentity = physicalNodeRequest(form, scenario, [rule4]).deployments[0]
    expect(changedIdentity).not.toHaveProperty('applyLatest')
    expect(changedIdentity).not.toHaveProperty('keepTickFacts')
    expect(changedIdentity.tickFacts).toEqual({ int64s: { wait: 3 } })
    expect(() => physicalNodeRequest(form, scenario, [])).toThrow('不在规则库')
  })

  it('编辑时从当前场景保留原始自定义端点', () => {
    const snapshot = structuredClone(scenario)
    ;(snapshot.physicalNodes as JsonObject[])[0].endpoint = ' custom://节点 '
    const form = physicalNodeFormFromScenario(snapshot, '1')
    expect(form).not.toHaveProperty('endpoint')
    expect(physicalNodeRequest(form, snapshot, []).physicalNode.endpoint).toBe(' custom://节点 ')
  })

  it('新增、编辑和清空节点同步间隔时正确生成请求', () => {
    const created = { ...newPhysicalNodeForm(), id: '3', routeUpdateIntervalMs: 10001 }
    expect(physicalNodeRequest(created, scenario, []).physicalNode.routeUpdateIntervalMs).toBe(
      10001,
    )
    created.routeUpdateIntervalMs = 0
    expect(physicalNodeRequest(created, scenario, []).physicalNode.routeUpdateIntervalMs).toBe(0)
    created.routeUpdateIntervalMs = 9223372036854
    expect(physicalNodeRequest(created, scenario, []).physicalNode.routeUpdateIntervalMs).toBe(
      9223372036854,
    )
    for (const value of [-1, 1.5, NaN, 9223372036855]) {
      created.routeUpdateIntervalMs = value
      expect(() => physicalNodeRequest(created, scenario, [])).toThrow('同步间隔')
    }
    const current = structuredClone(scenario)
    ;(current.physicalNodes as JsonObject[])[0].routeUpdateIntervalMs = 1500
    const editing = physicalNodeFormFromScenario(current, '1')
    expect(editing.routeUpdateIntervalMs).toBe(1500)
    expect(physicalNodeRequest(editing, current, []).physicalNode.routeUpdateIntervalMs).toBe(1500)
    editing.routeUpdateIntervalMs = undefined
    expect(physicalNodeRequest(editing, current, []).physicalNode).not.toHaveProperty(
      'routeUpdateIntervalMs',
    )
  })

  it('preserves untouched large facts and rejects edited numbers that may lose precision', () => {
    const large = structuredClone(scenario)
    ;(large.rules as JsonObject[])[0].tickFacts = {
      uint64s: { values: [Number('18446744073709551615')] },
    }
    const form = physicalNodeFormFromScenario(large, '1')
    const retained = physicalNodeRequest(form, large, [rule4]).deployments[0]
    expect(retained.keepTickFacts).toBe(true)
    expect(retained).not.toHaveProperty('tickFacts')

    form.deployments[0].tickFacts = '{"uint64s":{"values":[18446744073709551615]}}'
    expect(() => physicalNodeRequest(form, large, [rule4])).toThrow('安全范围')
    form.deployments[0].tickFacts = '{"int64s":{"value":9007199254740991.2}}'
    expect(() => physicalNodeRequest(form, large, [rule4])).toThrow('安全范围')
    form.deployments[0].tickFacts =
      '  {"strings":{"id":["18446744073709551615"]},"int64s":{"value":7}}  '
    const edited = physicalNodeRequest(form, large, [rule4]).deployments[0]
    expect(edited).not.toHaveProperty('keepTickFacts')
    expect(edited.tickFacts).toEqual({
      strings: { id: ['18446744073709551615'] },
      int64s: { value: 7 },
    })
  })

  it('rejects duplicate rules on the same node, global placement collisions, and invalid configs', () => {
    const form = {
      ...newPhysicalNodeForm(),
      id: '3',
      deployments: [
        { ...newDeploymentForm(), ruleId: key4, placementId: '15' },
        { ...newDeploymentForm(), ruleId: key4, placementId: '16' },
      ],
    }
    expect(() => physicalNodeRequest(form, scenario, [rule4, rule5])).toThrow('不能重复部署')
    form.deployments[1].ruleId = key5
    form.deployments[1].placementId = '12'
    expect(() => physicalNodeRequest(form, scenario, [rule4, rule5])).toThrow('已存在')
    form.deployments[1].placementId = '17'
    form.deployments[0].tickFacts = '[]'
    expect(() => physicalNodeRequest(form, scenario, [rule4, rule5])).toThrow('JSON 对象')
    form.deployments[0].tickFacts = ''
    form.deployments[0].weight = '4294967296'
    expect(() => physicalNodeRequest(form, scenario, [rule4, rule5])).toThrow('路由权重')
  })

  it('detects concurrent node edits before replacing deployments', () => {
    const form = physicalNodeFormFromScenario(scenario, '1')
    const changed = structuredClone(scenario)
    ;(changed.physicalNodes as JsonObject[])[0].endpoint = 'inproc://changed'
    expect(() => physicalNodeRequest(form, changed, [rule4])).toThrow('节点配置已变化')
    ;(changed.physicalNodes as JsonObject[]).shift()
    expect(() => physicalNodeRequest(form, changed, [rule4])).toThrow('节点配置已变化')
  })

  it('edits canonical uint64 identities without losing precision and normalizes old safe IDs', () => {
    const namespace = '18446744073709551615'
    const ruleId = '9007199254740993'
    const placementId = '18446744073709551614'
    const id = '18446744073709551613'
    const snapshot: JsonObject = {
      physicalNodes: [{ id, endpoint: 'inproc://large', enabled: true }],
      rules: [
        {
          logicalNode: { rule: { namespace, ruleId }, placementId },
          physicalNodeId: id,
          weight: 1,
          enabled: true,
        },
      ],
    }
    const rule = newPortableRule({ namespace, ruleId })
    const form = physicalNodeFormFromScenario(snapshot, id)
    const options = physicalNodeRuleOptions([rule])
    expect(options).toEqual([
      { value: ruleIdentity({ namespace, ruleId }), label: `${namespace}/${ruleId}` },
    ])
    expect(form.deployments[0].ruleId).toBe(options[0].value)
    form.deployments[0].applyLatest = true
    const saved = physicalNodeRequest(form, snapshot, [rule])
    expect(saved.physicalNode.id).toBe(id)
    expect(saved.deployments[0]).toEqual({
      logicalNode: { rule: { namespace, ruleId }, placementId },
      weight: 1,
      enabled: true,
      keepTickFacts: true,
      applyLatest: true,
    })
    expect(keyFromSelection('01')).toBeUndefined()
    expect(keyFromSelection('18446744073709551615')).toEqual({
      ruleId: '18446744073709551615',
    })
    expect(keyFromSelection('18446744073709551616')).toBeUndefined()
    expect(ruleIdentity({ ruleId: 5 })).toBe(ruleIdentity({ ruleId: '5' }))
    expect(keyFromSelection('5')).toEqual({ ruleId: '5' })
  })

  it('规则库别名变更或规则缺库时，保留原部署别名，应用最新时使用库别名', () => {
    const previous = newPortableRule({ namespace: '旧/别名:0', ruleId: '7' })
    const renamed = newPortableRule({ namespace: '新/别名:1', ruleId: '7' })
    const snapshot: JsonObject = {
      physicalNodes: [{ id: '1', endpoint: 'inproc://1', enabled: true }],
      rules: [
        {
          logicalNode: { rule: { namespace: '旧/别名:0', ruleId: '7' }, placementId: '3' },
          physicalNodeId: '1',
          weight: 1,
          enabled: true,
        },
      ],
    }
    const form = physicalNodeFormFromScenario(snapshot, '1')
    expect(form.deployments[0].ruleId).toBe('7')
    expect(physicalNodeRequest(form, snapshot, []).deployments[0].logicalNode.rule).toEqual(
      previous.ruleKey,
    )
    expect(physicalNodeRequest(form, snapshot, [renamed]).deployments[0].logicalNode.rule).toEqual(
      previous.ruleKey,
    )
    form.deployments[0].applyLatest = true
    expect(physicalNodeRequest(form, snapshot, [renamed]).deployments[0].logicalNode.rule).toEqual(
      renamed.ruleKey,
    )
    expect(
      physicalNodeRuleOptions([
        previous,
        newPortableRule({ namespace: previous.ruleKey.namespace, ruleId: '8' }),
      ]).map((item) => item.value),
    ).toEqual(['7', '8'])
  })

  it('拒绝节点或部署标识的旧名称、前导零、零和溢出', () => {
    const form = newPhysicalNodeForm()
    for (const id of ['18446744073709551616', 'legacy-node', '01', '0']) {
      form.id = id
      expect(() => physicalNodeRequest(form, {}, [])).toThrow(/uint64/)
    }
    form.id = '1'
    form.deployments = [
      { ...newDeploymentForm(), ruleId: ruleIdentity(rule4.ruleKey), placementId: '0' },
    ]
    for (const id of ['0', '18446744073709551616', 'legacy-placement', '01']) {
      form.deployments[0].placementId = id
      expect(() => physicalNodeRequest(form, {}, [rule4])).toThrow(/PlacementID/)
    }
  })
})
