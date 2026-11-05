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
    physicalNodes: [{ id: 'server', endpoint: 'inproc://server', enabled: true }],
    rules: [
      {
        logicalNode: { rule: { namespace: 'test', ruleId: 1 }, placementId: 'p1' },
        physicalNodeId: 'server',
        weight: 1,
        enabled: true,
        rule: {
          schemaVersion: 'match-rule/v1',
          ruleKey: { namespace: 'test', ruleId: 1 },
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
