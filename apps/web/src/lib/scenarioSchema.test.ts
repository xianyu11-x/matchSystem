import Ajv2020 from 'ajv/dist/2020'
import { describe, expect, it } from 'vitest'
import contractSchema from '../../../../api/schema/logical-node-contract/v3.schema.json'
import matchRuleSchema from '../../../../api/schema/match-rule/v1.schema.json'
import evaluationSchema from '../../../../api/schema/evaluation/v3.schema.json'
import expressionSchema from '../../../../api/schema/expression-scalar/v3.schema.json'
import prefilterSchema from '../../../../api/schema/prefilter/v3.schema.json'
import scenarioSchema from '../../../../api/schema/simulator-scenario/v1.schema.json'

const ajv = new Ajv2020({ allErrors: true, strict: false, strictNumbers: true })
ajv.addSchema([
  contractSchema,
  matchRuleSchema,
  evaluationSchema,
  expressionSchema,
  prefilterSchema,
])
const validateScenario = ajv.compile(scenarioSchema)

function scenario(pythonProvider?: unknown) {
  const boolExpression = {
    schemaVersion: 'expression-scalar/v3',
    resultType: 'bool',
    expr: { op: 'bool_literal', value: true },
  }
  return {
    schemaVersion: 'simulator-scenario/v1',
    physicalNodes: [{ id: '1', endpoint: 'inproc://server', enabled: true }],
    rules: [
      {
        logicalNode: { rule: { namespace: '3', ruleId: 1 }, placementId: '1' },
        physicalNodeId: '1',
        weight: 1,
        enabled: true,
        rule: {
          schemaVersion: 'match-rule/v1',
          ruleKey: { namespace: '3', ruleId: 1 },
          contract: {
            schemaVersion: 'logical-node-contract/v3',
            attributes: [],
            facts: [],
            indexes: [],
          },
          prefilter: {
            schemaVersion: 'prefilter/v3',
            bitmap: { resultType: 'bitmap', expr: { op: 'none' } },
          },
          evaluation: {
            schemaVersion: 'evaluation/v3',
            canJoin: structuredClone(boolExpression),
            canComplete: structuredClone(boolExpression),
          },
          scoring: { type: 'constant', params: { value: 0 } },
          seedSelection: { type: 'arrival', params: {} },
          runtime: {
            maxPlayers: 8,
            attemptLimitPerProduceMatch: 10,
            attemptLimitPerMatchRound: 20,
          },
        },
        ...(pythonProvider === undefined ? {} : { pythonProvider }),
      },
    ],
  }
}

describe('scenario JSON Schema', () => {
  it('约束路由算法和权重优先算法的 Ticket 阈值', () => {
    for (const routing of [
      {},
      { algorithm: '' },
      { algorithm: 'weighted_rendezvous' },
      { algorithm: 'queue_weighted_random', ticketLimit: 0 },
      { algorithm: 'weighted_priority', ticketLimit: 1 },
    ]) {
      expect(
        validateScenario({ ...scenario(), routing }),
        JSON.stringify(validateScenario.errors),
      ).toBe(true)
    }
    for (const routing of [
      null,
      { algorithm: 'unknown' },
      { algorithm: null },
      { algorithm: 'weighted_priority' },
      { algorithm: 'weighted_priority', ticketLimit: 0 },
      { ticketLimit: -1 },
      { ticketLimit: 1.5 },
      { ticketLimit: null },
      { ticketLimit: '10' },
      { extra: true },
    ]) {
      expect(validateScenario({ ...scenario(), routing }), JSON.stringify(routing)).toBe(false)
    }
  })

  it('约束物理节点路由快照更新周期', () => {
    const value = scenario()
    for (const routeUpdateIntervalMs of [0, 1, 10000, 9223372036854]) {
      const physicalNodes = [{ ...value.physicalNodes[0], routeUpdateIntervalMs }]
      expect(
        validateScenario({ ...value, physicalNodes }),
        JSON.stringify(validateScenario.errors),
      ).toBe(true)
    }
    for (const routeUpdateIntervalMs of [-1, 1.5, 9223372036855, null, '10000']) {
      const physicalNodes = [{ ...value.physicalNodes[0], routeUpdateIntervalMs }]
      expect(
        validateScenario({ ...value, physicalNodes }),
        JSON.stringify(routeUpdateIntervalMs),
      ).toBe(false)
    }
  })

  it('约束规则和节点的 uint64 边界，命名空间使用自由文本', () => {
    const validators = Object.fromEntries(
      ['namespace', 'ruleId', 'nodeId'].map((name) => [
        name,
        ajv.compile({ $ref: `${matchRuleSchema.$id}#/$defs/${name}` }),
      ]),
    )
    for (const name of ['ruleId', 'nodeId']) {
      for (const value of [1, '1', '18446744073709551614', '18446744073709551615'])
        expect(validators[name](value), `${name}: ${JSON.stringify(value)}`).toBe(true)
      for (const value of [0, '0', '', '00', '01', 'legacy', '1\n', '18446744073709551616'])
        expect(validators[name](value), `${name}: ${JSON.stringify(value)}`).toBe(false)
    }
    for (const value of ['', '0', '01', 'legacy', '别名/1:a', '1\n', '18446744073709551616'])
      expect(validators.namespace(value), JSON.stringify(value)).toBe(true)
    for (const value of [0, 1, null, {}, []])
      expect(validators.namespace(value), JSON.stringify(value)).toBe(false)
  })

  it('场景引用同一身份规则并接受缺省命名空间', () => {
    const value = scenario()
    value.physicalNodes[0].id = '18446744073709551615'
    value.rules[0].physicalNodeId = '18446744073709551615'
    value.rules[0].logicalNode.placementId = '18446744073709551614'
    Object.assign(value.rules[0].logicalNode.rule, {
      namespace: '0',
      ruleId: '18446744073709551615',
    })
    Object.assign(value.rules[0].rule.ruleKey, { namespace: '0', ruleId: '18446744073709551615' })
    expect(validateScenario(value), JSON.stringify(validateScenario.errors)).toBe(true)
    Reflect.deleteProperty(value.rules[0].logicalNode.rule, 'namespace')
    Reflect.deleteProperty(value.rules[0].rule.ruleKey, 'namespace')
    expect(validateScenario(value), JSON.stringify(validateScenario.errors)).toBe(true)
    value.rules[0].logicalNode.placementId = '01'
    expect(validateScenario(value)).toBe(false)
    value.rules[0].logicalNode.placementId = '1'
    value.physicalNodes[0].id = 'legacy'
    expect(validateScenario(value)).toBe(false)
  })

  it('批量身份前缀为空或可与至少一位 Ticket ID 拼接', () => {
    const prefix = ajv.compile({ $ref: `${matchRuleSchema.$id}#/$defs/decimalIdentityPrefix` })
    for (const value of ['', '1', '1844674407370955161']) expect(prefix(value)).toBe(true)
    for (const value of ['0', '00', '01', 'legacy', '1\n', '1844674407370955162'])
      expect(prefix(value), JSON.stringify(value)).toBe(false)
  })

  it.each([
    ['without a Python provider', undefined],
    ['with only the required script', { script: 'provider.py' }],
    ['with host defaults', { script: 'provider.py', python: '', runner: '', timeoutMs: 0 }],
    [
      'with explicit paths and timeout',
      { script: 'provider.py', python: 'python', runner: 'runner.py', timeoutMs: 250 },
    ],
  ])('accepts a valid scenario %s', (_name, pythonProvider) => {
    expect(
      validateScenario(scenario(pythonProvider)),
      JSON.stringify(validateScenario.errors),
    ).toBe(true)
  })

  it.each([
    ['null binding', null, '/rules/0/pythonProvider', 'type'],
    ['missing script', {}, '/rules/0/pythonProvider', 'required'],
    ['empty script', { script: '' }, '/rules/0/pythonProvider/script', 'minLength'],
    ['non-string script', { script: 123 }, '/rules/0/pythonProvider/script', 'type'],
    [
      'non-string Python',
      { script: 'provider.py', python: 123 },
      '/rules/0/pythonProvider/python',
      'type',
    ],
    [
      'non-string runner',
      { script: 'provider.py', runner: 123 },
      '/rules/0/pythonProvider/runner',
      'type',
    ],
    [
      'negative timeout',
      { script: 'provider.py', timeoutMs: -1 },
      '/rules/0/pythonProvider/timeoutMs',
      'minimum',
    ],
    [
      'fractional timeout',
      { script: 'provider.py', timeoutMs: 0.5 },
      '/rules/0/pythonProvider/timeoutMs',
      'type',
    ],
    [
      'unknown binding field',
      { script: 'provider.py', descriptors: {} },
      '/rules/0/pythonProvider',
      'additionalProperties',
    ],
  ])('rejects %s', (_name, pythonProvider, instancePath, keyword) => {
    expect(validateScenario(scenario(pythonProvider))).toBe(false)
    expect(validateScenario.errors).toContainEqual(
      expect.objectContaining({ instancePath, keyword }),
    )
  })

  it.each([
    'factProviderDescriptor',
    'objectFactProviderDescriptor',
    'matchFactProviderDescriptor',
  ])('rejects the removed scenario field %s', (field) => {
    const valid = scenario({ script: 'provider.py' })
    const invalid = {
      ...valid,
      rules: [{ ...valid.rules[0], [field]: { id: 'legacy', version: 'v1' } }],
    }
    expect(validateScenario(invalid)).toBe(false)
    expect(validateScenario.errors).toContainEqual(
      expect.objectContaining({
        instancePath: '/rules/0',
        keyword: 'additionalProperties',
        params: { additionalProperty: field },
      }),
    )
  })

  it('validates the embedded rule and its referenced expression schema', () => {
    const invalid = scenario({ script: 'provider.py' })
    invalid.rules[0].rule.evaluation.canJoin.expr.value = false
    invalid.rules[0].rule.evaluation.canJoin.resultType = 'int64'
    expect(validateScenario(invalid)).toBe(false)
    expect(validateScenario.errors).toContainEqual(
      expect.objectContaining({
        instancePath: '/rules/0/rule/evaluation/canJoin/resultType',
        keyword: 'const',
      }),
    )
  })
})
