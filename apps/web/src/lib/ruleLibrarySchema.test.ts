import Ajv2020 from 'ajv/dist/2020'
import { expect, it } from 'vitest'
import contractSchema from '../../../../api/schema/logical-node-contract/v3.schema.json'
import matchRuleSchema from '../../../../api/schema/match-rule/v1.schema.json'
import evaluationSchema from '../../../../api/schema/evaluation/v3.schema.json'
import expressionSchema from '../../../../api/schema/expression-scalar/v3.schema.json'
import prefilterSchema from '../../../../api/schema/prefilter/v3.schema.json'
import librarySchema from '../../../../api/schema/match-rule-library/v1.schema.json'
import { newPortableRule } from './ruleLibrary'

it('规则库 Schema 引用完整规则格式并约束根文档', () => {
  const ajv = new Ajv2020({ allErrors: true, strict: false, strictNumbers: true })
  ajv.addSchema([
    contractSchema,
    matchRuleSchema,
    evaluationSchema,
    expressionSchema,
    prefilterSchema,
  ])
  const validate = ajv.compile(librarySchema)
  const library = {
    schemaVersion: 'match-rule-library/v1',
    rules: [newPortableRule({ ruleId: 1 })],
  }
  expect(validate(library), JSON.stringify(validate.errors)).toBe(true)
  expect(validate({ ...library, schemaVersion: 'match-rule-library/v2' })).toBe(false)
  expect(validate({ ...library, extra: true })).toBe(false)
  expect(validate({ ...library, rules: [{ schemaVersion: 'match-rule/v1' }] })).toBe(false)
})
