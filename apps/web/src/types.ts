import type { Edge, Node } from '@xyflow/react'

export type ValueType = 'bool' | 'int64' | 'strings' | 'uint64s' | 'bitmap'
export type FieldType = Exclude<ValueType, 'bool' | 'bitmap'>
export type FactScope = 'tick' | 'object' | 'match'

export interface AttributeSpec {
  name: string
  type: 'strings' | 'uint64s' | 'int64'
  maxValues?: number
}

export interface FactSpec extends AttributeSpec {
  scope: FactScope
  /** Human-readable meaning exposed by the LogicalNode Fact contract. */
  description?: string
}

export interface IndexSpec {
  type: 'multi_value' | 'int64_range'
  name: string
  keyType?: 'string' | 'uint64'
  maxDocumentValues?: number
  maxQueryValues?: number
}

export interface ContractLimits {
  maxBytes?: number
  maxDepth?: number
  maxChildren?: number
  maxStringBytes?: number
  maxIndexes?: number
  maxAttributes?: number
  maxFacts?: number
  maxValues?: number
  maxDocumentValues?: number
  maxQueryValues?: number
}

export interface LogicalNodeContract {
  schemaVersion: 'logical-node-contract/v3'
  attributes: AttributeSpec[]
  facts: FactSpec[]
  indexes: IndexSpec[]
  limits?: ContractLimits
}

export type JsonPrimitive = string | number | boolean | null
export type JsonValue = JsonPrimitive | JsonObject | JsonValue[]
export interface JsonObject {
  [key: string]: JsonValue
}

export interface ExpressionScalar {
  schemaVersion: 'expression-scalar/v3'
  resultType: ValueType extends never ? never : 'bool' | 'int64' | 'strings' | 'uint64s'
  expr: JsonObject
}

export interface PrefilterDocument {
  schemaVersion: 'prefilter/v3'
  bitmap: {
    resultType: 'bitmap'
    expr: JsonObject
  }
  runtime?: {
    containsProbeThreshold?: number
  }
}

export interface EvaluationDocument {
  schemaVersion: 'evaluation/v3'
  canJoin: ExpressionScalar
  canComplete: ExpressionScalar
}

export type ScoreDirection = 'ascending' | 'descending'

export type CandidateScoringConfig =
  | { type: 'constant'; params: { value: number } }
  | { type: 'created_at'; params: { direction: ScoreDirection; weight?: number } }
  | {
      type: 'int64_field'
      params: {
        field: string
        direction: ScoreDirection
        weight?: number
        missingScore?: number
      }
    }

export type SeedSelectionConfig =
  | { type: 'arrival'; params: Record<string, never> }
  | { type: 'oldest'; params: Record<string, never> }
  | { type: 'int64_priority'; params: { field: string; direction: ScoreDirection } }
  | { type: 'random'; params: { randomSeed: number } }

export interface RuleRuntimeConfig {
  candidateLimitPerSeed: number
  maxPlayers: number
  attemptLimitPerProduceMatch: number
  attemptLimitPerMatchRound: number
}

export interface TypedAttributes {
  strings: Record<string, string[]>
  uint64s: Record<string, number[]>
  int64: Record<string, number>
}

export type FactValue = string[] | number[] | number
export type FactSnapshot = Record<string, FactValue>

export interface OwnerRef {
  physicalNodeId: string
  logicalNodeKey: string
  ruleKey: string
  placementId: string
}

export type TicketStatus = 'waiting' | 'matched' | 'expired' | 'rejected'

export interface Ticket {
  ticketId: string
  createdAt: string
  attributes: TypedAttributes
  facts: FactSnapshot
  routeDecision?: {
    status: 'routed' | 'pending' | 'rejected'
    owner?: OwnerRef
    reason?: string
  }
  status: TicketStatus
  matchId?: string
}

export interface MatchRecord {
  matchId: string
  createdAt: string
  roundId: string
  ruleKey: string
  placementId: string
  ticketIds: string[]
  memberCount: number
  facts?: FactSnapshot
  durationMs?: number
}

export interface TopologyNode {
  id: string
  name: string
  ruleKey: string
  placementId: string
  ticketCount: number
  state: 'healthy' | 'degraded' | 'stopped'
  load: number
}

export interface Topology {
  updatedAt: string
  nodes: TopologyNode[]
  routes: Array<{ from: string; to: string; tickets: number }>
}

export interface RoundRecord {
  roundId: string
  startedAt: string
  completedAt?: string
  seed: number
  matchCount: number
  ticketCount: number
  status: 'running' | 'completed' | 'failed'
}

export interface Scenario {
  scenarioId: string
  name: string
  updatedAt: string
  activeRuleKey: string
  rules: RuleSummary[]
  tickFacts: FactSnapshot
  rawScenario?: JsonObject
  revision?: string
}

export interface RuleSummary {
  ruleKey: string
  apiRule?: ApiRuleKey
  placementId: string
  displayName: string
  enabled: boolean
  contract: LogicalNodeContract
  prefilter: PrefilterDocument
  evaluation: EvaluationDocument
  scoring?: CandidateScoringConfig
  seedSelection?: SeedSelectionConfig
  runtime?: RuleRuntimeConfig
  tickFacts?: FactSnapshot
}

export interface RuleGraphNodeData {
  [key: string]: unknown
  label: string
  nodeType: CapabilityNodeType
  outputType: ValueType
  inputTypes: ValueType[]
  maxInputs?: number
  /** Number of input handles that are required by the current AST shape. */
  requiredInputs?: number
  /** True for children/items arrays that may accept additional inputs. */
  variadic?: boolean
  config: Record<string, JsonValue>
  astPath?: string
  valid?: boolean
  error?: string
}

export type RuleGraphNode = Node<RuleGraphNodeData, 'rule'>
export type RuleGraphEdge = Edge<{ valueType: ValueType; valid?: boolean }, string>

export interface RuleGraphDocument {
  nodes: RuleGraphNode[]
  edges: RuleGraphEdge[]
}

export interface RuleDocument {
  /** The editor keeps graph/placement metadata beside the portable rule fields. */
  schemaVersion: 'match-rule/v1'
  ruleKey: string
  placementId: string
  contract: LogicalNodeContract
  prefilter: PrefilterDocument
  evaluation: EvaluationDocument
  scoring: CandidateScoringConfig
  seedSelection: SeedSelectionConfig
  runtime: RuleRuntimeConfig
  graph: RuleGraphDocument
  apiRule?: ApiRuleKey
  tickFacts?: FactSnapshot
}

/** OpenAPI wire identity.  Rule IDs and Ticket IDs remain numeric at the API boundary. */
export interface ApiRuleKey {
  namespace?: string
  ruleId: number
}

/** Complete portable rule configuration sent as `RuleSpec.rule` on the API. */
export interface MatchRuleDocument {
  schemaVersion: 'match-rule/v1'
  ruleKey: ApiRuleKey
  contract: LogicalNodeContract
  prefilter: PrefilterDocument
  evaluation: EvaluationDocument
  scoring: CandidateScoringConfig
  seedSelection: SeedSelectionConfig
  runtime: RuleRuntimeConfig
}

export type CapabilityNodeType =
  | 'source.attribute'
  | 'source.fact'
  | 'literal.string'
  | 'literal.int64'
  | 'literal.uint64'
  | 'literal.bool'
  | 'compare.int64'
  | 'compare.strings'
  | 'compare.uint64'
  | 'expression.generic'
  | 'logic.and'
  | 'logic.or'
  | 'logic.not'
  | 'prefilter.lookup'
  | 'prefilter.exclude'
  | 'prefilter.combine'
  | 'prefilter.generic'
  | 'evaluation.join'
  | 'evaluation.complete'

export interface CapabilityNode {
  type: CapabilityNodeType
  op?: string
  label: string
  description: string
  category: 'source' | 'literal' | 'expression' | 'prefilter' | 'evaluation'
  outputType: ValueType
  inputTypes: ValueType[]
  maxInputs: number
  allowedScopes?: FactScope[]
  fields?: string[]
}

export interface Capabilities {
  schemaVersions: string[]
  sources: string[]
  nodeTypes: CapabilityNode[]
  limits: ContractLimits
  candidateScorers: string[]
  seedSelections: string[]
  expressionOps?: string[]
  bitmapOps?: string[]
  indexTypes?: string[]
  factTypes?: string[]
  scalarOperators?: Array<{
    name: string
    resultType: string
    inputs?: string[]
    fields: string[]
  }>
  bitmapOperators?: Array<{
    name: string
    resultType: string
    inputs?: string[]
    fields: string[]
  }>
}

export interface TicketsPage {
  items: Ticket[]
  nextCursor?: string
  prevCursor?: string
  total?: number
}

export interface MatchesPage {
  items: MatchRecord[]
  nextCursor?: string
  total?: number
}

export interface ValidationIssue {
  path: string
  message: string
  keyword?: string
  source?: 'schema' | 'graph' | 'backend'
  severity?: 'error' | 'warning'
}

export interface ValidationResponse {
  valid: boolean
  errors: ValidationIssue[]
  warnings?: ValidationIssue[]
}

export interface TicketInput {
  ticketId?: string
  rule?: ApiRuleKey
  placementId?: string
  affinityKey?: string
  attributes: TypedAttributes
  facts: FactSnapshot
}

export interface BatchGeneratorSpec {
  count: number
  seed: number
  startTicketId?: number
  ruleKey: string
  rule?: ApiRuleKey
  placementId?: string
  /** Optional deterministic choices constrained by the selected Contract. */
  stringChoices?: Record<string, string[]>
  uint64Choices?: Record<string, number[]>
  int64Ranges?: Record<string, { min: number; max: number }>
}

export interface BatchGeneratorResponse {
  accepted: number
  rejected: number
  generatorId: string
  seed: number
}

export interface RunRoundInput {
  now?: number
  matchLimit?: number
}

export interface RunRoundResponse {
  round: RoundRecord
  matches: MatchRecord[]
}
