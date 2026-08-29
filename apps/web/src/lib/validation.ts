import Ajv2020, { type ErrorObject, type ValidateFunction } from 'ajv/dist/2020'
import { validateGraph } from './graph'
import type { Capabilities, RuleDocument, ValidationIssue, ValidationResponse } from '../types'

const contractSchema = {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  $id: 'https://matchsystem.local/schema/logical-node-contract/v3',
  type: 'object',
  additionalProperties: false,
  required: ['schemaVersion', 'attributes', 'facts', 'indexes'],
  properties: {
    schemaVersion: { const: 'logical-node-contract/v3' },
    attributes: { type: 'array', maxItems: 256, items: { $ref: '#/$defs/attribute' } },
    facts: { type: 'array', maxItems: 256, items: { $ref: '#/$defs/fact' } },
    indexes: { type: 'array', maxItems: 128, items: { $ref: '#/$defs/index' } },
    limits: { $ref: '#/$defs/limits' },
  },
  $defs: {
    name: { type: 'string', minLength: 1, maxLength: 1024 },
    valueType: { enum: ['strings', 'uint64s', 'int64'] },
    attribute: {
      oneOf: [
        {
          type: 'object',
          additionalProperties: false,
          required: ['name', 'type', 'maxValues'],
          properties: {
            name: { $ref: '#/$defs/name' },
            type: { enum: ['strings', 'uint64s'] },
            maxValues: { type: 'integer', minimum: 1, maximum: 10000 },
          },
        },
        {
          type: 'object',
          additionalProperties: false,
          required: ['name', 'type'],
          properties: { name: { $ref: '#/$defs/name' }, type: { const: 'int64' } },
        },
      ],
    },
    fact: {
      oneOf: [
        {
          type: 'object',
          additionalProperties: false,
          required: ['name', 'type', 'scope', 'maxValues'],
          properties: {
            name: { $ref: '#/$defs/name' },
            type: { enum: ['strings', 'uint64s'] },
            scope: { enum: ['tick', 'object', 'match'] },
            maxValues: { type: 'integer', minimum: 1, maximum: 10000 },
          },
        },
        {
          type: 'object',
          additionalProperties: false,
          required: ['name', 'type', 'scope'],
          properties: {
            name: { $ref: '#/$defs/name' },
            type: { const: 'int64' },
            scope: { enum: ['tick', 'object', 'match'] },
          },
        },
      ],
    },
    index: {
      oneOf: [
        {
          type: 'object',
          additionalProperties: false,
          required: ['type', 'name', 'keyType', 'maxDocumentValues', 'maxQueryValues'],
          properties: {
            type: { const: 'multi_value' },
            name: { $ref: '#/$defs/name' },
            keyType: { enum: ['string', 'uint64'] },
            maxDocumentValues: { type: 'integer', minimum: 1, maximum: 256 },
            maxQueryValues: { type: 'integer', minimum: 1, maximum: 256 },
          },
        },
        {
          type: 'object',
          additionalProperties: false,
          required: ['type', 'name'],
          properties: { type: { const: 'int64_range' }, name: { $ref: '#/$defs/name' } },
        },
      ],
    },
    limits: {
      type: 'object',
      additionalProperties: false,
      properties: {
        maxBytes: { type: 'integer', minimum: 0 },
        maxDepth: { type: 'integer', minimum: 0 },
        maxChildren: { type: 'integer', minimum: 0 },
        maxStringBytes: { type: 'integer', minimum: 0 },
        maxIndexes: { type: 'integer', minimum: 0 },
        maxAttributes: { type: 'integer', minimum: 0 },
        maxFacts: { type: 'integer', minimum: 0 },
        maxValues: { type: 'integer', minimum: 0 },
        maxDocumentValues: { type: 'integer', minimum: 0 },
        maxQueryValues: { type: 'integer', minimum: 0 },
      },
    },
  },
} as const

const expressionSchema = {
  type: 'object',
  required: ['schemaVersion', 'resultType', 'expr'],
  additionalProperties: true,
  properties: {
    schemaVersion: { const: 'expression-scalar/v3' },
    resultType: { enum: ['bool', 'int64', 'strings', 'uint64s'] },
    expr: { type: 'object' },
  },
}

const documentSchema = {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  $id: 'https://matchsystem.local/schema/rule-document/v1',
  type: 'object',
  additionalProperties: false,
  required: [
    'schemaVersion',
    'ruleKey',
    'placementId',
    'contract',
    'prefilter',
    'evaluation',
    'graph',
  ],
  properties: {
    schemaVersion: { const: 'rule-document/v1' },
    ruleKey: { type: 'string', minLength: 1 },
    placementId: { type: 'string', minLength: 1 },
    contract: { $ref: 'https://matchsystem.local/schema/logical-node-contract/v3' },
    prefilter: {
      type: 'object',
      required: ['schemaVersion', 'bitmap'],
      properties: {
        schemaVersion: { const: 'prefilter/v3' },
        bitmap: {
          type: 'object',
          required: ['resultType', 'expr'],
          properties: { resultType: { const: 'bitmap' }, expr: { type: 'object' } },
        },
        runtime: { type: 'object' },
      },
    },
    evaluation: {
      type: 'object',
      required: ['schemaVersion', 'canJoin', 'canComplete'],
      properties: {
        schemaVersion: { const: 'evaluation/v3' },
        canJoin: expressionSchema,
        canComplete: expressionSchema,
      },
    },
    graph: {
      type: 'object',
      required: ['nodes', 'edges'],
      properties: {
        nodes: { type: 'array', items: { type: 'object' } },
        edges: { type: 'array', items: { type: 'object' } },
      },
    },
  },
} as const

const ajv = new Ajv2020({ allErrors: true, strict: false })
ajv.addSchema(contractSchema)
const validateDocument: ValidateFunction<RuleDocument> = ajv.compile(documentSchema)

const ajvIssue = (error: ErrorObject): ValidationIssue => ({
  path: error.instancePath || '/',
  message: error.message ?? '结构不合法',
  keyword: error.keyword,
  source: 'schema',
  severity: 'error',
})

export function validateRuleDocument(
  rule: RuleDocument,
  _capabilities?: Capabilities,
): ValidationResponse {
  const valid = validateDocument(rule)
  const errors = valid ? [] : (validateDocument.errors ?? []).map(ajvIssue)
  const graphResult = validateGraph(rule.graph, rule.contract)
  errors.push(...graphResult.errors)
  return { valid: errors.length === 0, errors }
}
