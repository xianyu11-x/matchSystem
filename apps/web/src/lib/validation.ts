import Ajv2020, { type ErrorObject, type ValidateFunction } from 'ajv/dist/2020'
import { validateGraph } from './graph'
import contractSchemaDocument from '../../../../api/schema/logical-node-contract/v3.schema.json'
import matchRuleSchema from '../../../../api/schema/match-rule/v1.schema.json'
import evaluationSchema from '../../../../api/schema/evaluation/v3.schema.json'
import expressionSchemaDocument from '../../../../api/schema/expression-scalar/v3.schema.json'
import prefilterSchema from '../../../../api/schema/prefilter/v3.schema.json'
import type {
  ApiRuleKey,
  Capabilities,
  MatchRuleDocument,
  RuleDocument,
  ValidationIssue,
  ValidationResponse,
} from '../types'

const ajv = new Ajv2020({ allErrors: true, strict: false })

const schemaObject = (value: unknown): Record<string, any> =>
  value as Record<string, any>
const registerSchemaWithFileRefAlias = (value: unknown) => {
  const schema = schemaObject(value)
  const id = typeof schema.$id === 'string' ? schema.$id : undefined
  if (!id) return
  ajv.addSchema(schema, id)
  ajv.addSchema({ ...schema, $id: `${id}.schema.json` }, `${id}.schema.json`)
}
registerSchemaWithFileRefAlias(contractSchemaDocument)
registerSchemaWithFileRefAlias(prefilterSchema)
registerSchemaWithFileRefAlias(evaluationSchema)
registerSchemaWithFileRefAlias(expressionSchemaDocument)
const matchRuleValidator: ValidateFunction<MatchRuleDocument> = ajv.compile<MatchRuleDocument>(
  schemaObject(matchRuleSchema),
)

const ajvIssue = (error: ErrorObject): ValidationIssue => ({
  path: error.instancePath || '/',
  message: error.message ?? '结构不合法',
  keyword: error.keyword,
  source: 'schema',
  severity: 'error',
})

const semanticIssue = (path: string, message: string): ValidationIssue => ({
  path,
  message,
  keyword: 'contract',
  source: 'schema',
  severity: 'error',
})

export function validateContractSemantics(contract: RuleDocument['contract']): ValidationIssue[] {
  const errors: ValidationIssue[] = []
  const names = new Map<string, string>()
  contract.attributes.forEach((attribute, index) => {
    const previous = names.get(attribute.name)
    if (previous)
      errors.push(
        semanticIssue(
          `/contract/attributes/${index}/name`,
          `名称「${attribute.name}」已被 ${previous} 使用`,
        ),
      )
    else if (attribute.name) names.set(attribute.name, 'Attribute')
  })
  contract.facts.forEach((fact, index) => {
    const previous = names.get(fact.name)
    if (previous)
      errors.push(
        semanticIssue(`/contract/facts/${index}/name`, `名称「${fact.name}」已被 ${previous} 使用`),
      )
    else if (fact.name) names.set(fact.name, 'Fact')
  })

  const indexNames = new Set<string>()
  contract.indexes.forEach((indexSpec, index) => {
    const path = `/contract/indexes/${index}`
    if (indexNames.has(indexSpec.name))
      errors.push(semanticIssue(`${path}/name`, `索引「${indexSpec.name}」重复`))
    indexNames.add(indexSpec.name)
    const attribute = contract.attributes.find((item) => item.name === indexSpec.name)
    if (!attribute) {
      errors.push(semanticIssue(`${path}/name`, '索引必须引用已声明的 Attribute'))
      return
    }
    if (indexSpec.type === 'int64_range' && attribute.type !== 'int64')
      errors.push(semanticIssue(`${path}/type`, 'int64_range 只能用于 int64 Attribute'))
    if (indexSpec.type === 'multi_value') {
      const expected = attribute.type === 'uint64s' ? 'uint64' : 'string'
      if (attribute.type === 'int64')
        errors.push(semanticIssue(`${path}/type`, 'int64 Attribute 不能使用 multi_value 索引'))
      else if (indexSpec.keyType !== expected)
        errors.push(semanticIssue(`${path}/keyType`, `键类型必须为 ${expected}`))
    }
  })
  return errors
}

function matchRuleForValidation(rule: RuleDocument): MatchRuleDocument {
  const apiRule: ApiRuleKey =
    rule.apiRule && Number.isInteger(rule.apiRule.ruleId) && rule.apiRule.ruleId > 0
      ? {
          namespace: rule.apiRule.namespace || undefined,
          ruleId: rule.apiRule.ruleId,
        }
      : { ruleId: 1 }
  return {
    schemaVersion: 'match-rule/v1',
    ruleKey: apiRule,
    contract: rule.contract,
    prefilter: rule.prefilter,
    evaluation: rule.evaluation,
    scoring: rule.scoring,
    seedSelection: rule.seedSelection,
    runtime: rule.runtime,
  }
}

export function validateRuleDocument(
  rule: RuleDocument,
  capabilities?: Capabilities,
): ValidationResponse {
  const matchRule = matchRuleForValidation(rule)
  const valid = matchRuleValidator(matchRule)
  const errors = valid ? [] : (matchRuleValidator.errors ?? []).map(ajvIssue)
  if (rule.schemaVersion !== 'match-rule/v1')
    errors.push(semanticIssue('/schemaVersion', '规则文档版本必须为 match-rule/v1'))
  if (!rule.ruleKey.trim()) errors.push(semanticIssue('/ruleKey', 'RuleKey 不能为空'))
  if (!rule.placementId.trim()) errors.push(semanticIssue('/placementId', 'PlacementID 不能为空'))
  if (!rule.graph || !Array.isArray(rule.graph.nodes) || !Array.isArray(rule.graph.edges))
    errors.push(semanticIssue('/graph', '编辑器图必须包含 nodes 和 edges'))
  errors.push(...validateContractSemantics(rule.contract))
  const graphResult = validateGraph(rule.graph, rule.contract)
  errors.push(...graphResult.errors)
  if (capabilities) {
    const scalarOps = new Set(capabilities.expressionOps ?? [])
    const bitmapOps = new Set(capabilities.bitmapOps ?? [])
    for (const node of rule.graph.nodes) {
      if (!node.data.astPath) continue
      const op = typeof node.data.config.op === 'string' ? node.data.config.op : ''
      if (!op) continue
      const allowed = node.data.outputType === 'bitmap' ? bitmapOps : scalarOps
      if (allowed.size > 0 && !allowed.has(op))
        errors.push({
          path: `/graph/nodes/${node.id}/config/op`,
          message: `当前 Go 服务不支持节点类型「${op}」`,
          keyword: 'capability',
          source: 'graph',
          severity: 'error',
        })
    }
    if (
      capabilities.candidateScorers.length > 0 &&
      !capabilities.candidateScorers.includes(rule.scoring.type)
    )
      errors.push({
        path: '/scoring/type',
        message: `当前 Go 服务不支持评分函数「${rule.scoring.type}」`,
        keyword: 'capability',
        source: 'graph',
        severity: 'error',
      })
    if (
      capabilities.seedSelections.length > 0 &&
      !capabilities.seedSelections.includes(rule.seedSelection.type)
    )
      errors.push({
        path: '/seedSelection/type',
        message: `当前 Go 服务不支持 Seed 挑选算法「${rule.seedSelection.type}」`,
        keyword: 'capability',
        source: 'graph',
        severity: 'error',
      })
  }
  return { valid: errors.length === 0, errors }
}
