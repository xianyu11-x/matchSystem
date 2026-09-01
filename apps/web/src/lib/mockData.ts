import type {
  CapabilityNode,
  Capabilities,
  EvaluationDocument,
  FactSnapshot,
  LogicalNodeContract,
  MatchRecord,
  PrefilterDocument,
  RuleDocument,
  RuleGraphDocument,
  RuleGraphEdge,
  RuleGraphNode,
  RuleSummary,
  Scenario,
  Ticket,
  TicketsPage,
  Topology,
} from '../types'

const contract: LogicalNodeContract = {
  schemaVersion: 'logical-node-contract/v3',
  attributes: [
    { name: 'region', type: 'strings', maxValues: 4 },
    { name: 'playerLevel', type: 'int64' },
    { name: 'modes', type: 'strings', maxValues: 4 },
  ],
  facts: [
    {
      name: 'preferredRoles',
      type: 'strings',
      scope: 'object',
      maxValues: 3,
      description: '玩家偏好的队伍位置，用于匹配角色组成。',
    },
    {
      name: 'latencyMs',
      type: 'int64',
      scope: 'object',
      description: '玩家到当前匹配区域的网络延迟（毫秒）。',
    },
    {
      name: 'queueDepth',
      type: 'int64',
      scope: 'tick',
      description: '当前 Tick 时刻等待匹配的 Ticket 数量。',
    },
    {
      name: 'partySize',
      type: 'int64',
      scope: 'match',
      description: '本次 Match 中的成员数量。',
    },
  ],
  indexes: [
    {
      type: 'multi_value',
      name: 'region',
      keyType: 'string',
      maxDocumentValues: 4,
      maxQueryValues: 2,
    },
    { type: 'int64_range', name: 'playerLevel' },
  ],
  limits: {
    maxDepth: 32,
    maxChildren: 16,
    maxAttributes: 256,
    maxFacts: 256,
    maxValues: 10000,
  },
}

const prefilter: PrefilterDocument = {
  schemaVersion: 'prefilter/v3',
  bitmap: {
    resultType: 'bitmap',
    expr: {
      op: 'lookup_string',
      index: 'region',
      values: {
        schemaVersion: 'expression-scalar/v3',
        resultType: 'strings',
        expr: { op: 'strings_literal', values: ['ap-southeast', 'eu-west'] },
      },
    },
  },
  runtime: { containsProbeThreshold: 128 },
}

const evaluation: EvaluationDocument = {
  schemaVersion: 'evaluation/v3',
  canJoin: {
    schemaVersion: 'expression-scalar/v3',
    resultType: 'bool',
    expr: { op: 'bool_literal', value: true },
  },
  canComplete: {
    schemaVersion: 'expression-scalar/v3',
    resultType: 'bool',
    expr: { op: 'bool_literal', value: true },
  },
}

export const demoRuleSummary: RuleSummary = {
  ruleKey: 'ranked-5v5',
  apiRule: { namespace: 'demo', ruleId: 1 },
  placementId: 'sea-1',
  displayName: '排位 · 5v5',
  enabled: true,
  contract,
  prefilter,
  evaluation,
  scoring: { type: 'created_at', params: { direction: 'descending' } },
  seedSelection: { type: 'arrival', params: {} },
  runtime: {
    candidateLimitPerSeed: 128,
    maxPlayers: 8,
    attemptLimitPerProduceMatch: 10,
    attemptLimitPerMatchRound: 20,
  },
  tickFacts: { queueDepth: 742 },
  providerDescriptors: {
    tick: {
      id: 'demo.tick-facts',
      version: 'v1',
      facts: [
        {
          name: 'queueDepth',
          type: 'int64',
          scope: 'tick',
          description: '当前 Tick 等待匹配的 Ticket 数量。',
        },
      ],
    },
    object: {
      id: 'demo.object-facts',
      version: 'v1',
      facts: [
        { name: 'preferredRoles', type: 'strings', scope: 'object', maxValues: 3 },
        { name: 'latencyMs', type: 'int64', scope: 'object' },
      ],
    },
    match: {
      id: 'demo.match-facts',
      version: 'v1',
      facts: [{ name: 'partySize', type: 'int64', scope: 'match' }],
    },
  },
}

const node = (
  id: string,
  label: string,
  nodeType: RuleGraphNode['data']['nodeType'],
  outputType: RuleGraphNode['data']['outputType'],
  inputTypes: RuleGraphNode['data']['inputTypes'],
  position: { x: number; y: number },
  config: RuleGraphNode['data']['config'],
): RuleGraphNode => ({
  id,
  type: 'rule',
  position,
  data: { label, nodeType, outputType, inputTypes, config },
})

export const demoGraph: RuleGraphDocument = {
  nodes: [
    node(
      'source-region',
      'Attribute · region',
      'source.attribute',
      'strings',
      [],
      { x: 30, y: 100 },
      { source: 'candidate_attributes', name: 'region' },
    ),
    node(
      'literal-regions',
      '允许地区',
      'literal.string',
      'strings',
      [],
      { x: 30, y: 280 },
      { values: ['ap-southeast', 'eu-west'] },
    ),
    node(
      'compare-region',
      '包含任一地区',
      'compare.strings',
      'bool',
      ['strings', 'strings'],
      { x: 330, y: 165 },
      { op: 'strings_contains_any' },
    ),
    node(
      'lookup-region',
      'Prefilter · region index',
      'prefilter.lookup',
      'bitmap',
      ['strings'],
      { x: 620, y: 90 },
      { index: 'region', valueType: 'strings' },
    ),
    node(
      'evaluation-join',
      'Evaluation · canJoin',
      'evaluation.join',
      'bool',
      ['bool'],
      { x: 620, y: 265 },
      { field: 'canJoin' },
    ),
  ],
  edges: [
    {
      id: 'e-region-compare',
      source: 'source-region',
      target: 'compare-region',
      targetHandle: 'input-0',
      data: { valueType: 'strings' },
    },
    {
      id: 'e-literal-compare',
      source: 'literal-regions',
      target: 'compare-region',
      targetHandle: 'input-1',
      data: { valueType: 'strings' },
    },
    {
      id: 'e-region-lookup',
      source: 'literal-regions',
      target: 'lookup-region',
      targetHandle: 'input-0',
      data: { valueType: 'strings' },
    },
    {
      id: 'e-compare-evaluation',
      source: 'compare-region',
      target: 'evaluation-join',
      targetHandle: 'input-0',
      data: { valueType: 'bool' },
    },
  ] as RuleGraphEdge[],
}

export const demoRule: RuleDocument = {
  schemaVersion: 'match-rule/v1',
  ruleKey: demoRuleSummary.ruleKey,
  placementId: demoRuleSummary.placementId,
  apiRule: demoRuleSummary.apiRule,
  contract,
  prefilter,
  evaluation,
  scoring: demoRuleSummary.scoring!,
  seedSelection: demoRuleSummary.seedSelection!,
  runtime: demoRuleSummary.runtime!,
  tickFacts: { queueDepth: 742 },
  providerDescriptors: demoRuleSummary.providerDescriptors,
  graph: demoGraph,
}

export const demoScenario: Scenario = {
  scenarioId: 'scenario-demo',
  name: 'Season 12 · 东南亚演练场',
  updatedAt: '2026-08-29T09:12:00.000Z',
  activeRuleKey: demoRuleSummary.ruleKey,
  rules: [demoRuleSummary],
  tickFacts: { queueDepth: 742 },
}

export const demoCapabilities: Capabilities = {
  schemaVersions: [
    'match-rule/v1',
    'logical-node-contract/v3',
    'expression-scalar/v3',
    'prefilter/v3',
    'evaluation/v3',
  ],
  candidateScorers: ['constant', 'created_at', 'int64_field'],
  seedSelections: ['arrival', 'oldest', 'int64_priority', 'random'],
  sources: [
    'seed_attributes',
    'seed_facts',
    'tick_facts',
    'candidate_attributes',
    'candidate_facts',
    'match_facts',
  ],
  limits: { maxDepth: 32, maxChildren: 16, maxAttributes: 256, maxFacts: 256 },
  nodeTypes: [
    {
      type: 'source.attribute',
      label: 'Attribute 引用',
      description: '读取候选 Ticket 的 typed Attribute',
      category: 'source',
      outputType: 'strings',
      inputTypes: [],
      maxInputs: 0,
    },
    {
      type: 'source.fact',
      label: 'Fact 引用',
      description: '读取允许 scope 内的 Fact snapshot',
      category: 'source',
      outputType: 'strings',
      inputTypes: [],
      maxInputs: 0,
      allowedScopes: ['tick', 'object', 'match'],
    },
    {
      type: 'literal.string',
      label: 'String 常量',
      description: '字符串集合字面量',
      category: 'literal',
      outputType: 'strings',
      inputTypes: [],
      maxInputs: 0,
    },
    {
      type: 'literal.int64',
      label: 'Int64 常量',
      description: '有符号整数常量',
      category: 'literal',
      outputType: 'int64',
      inputTypes: [],
      maxInputs: 0,
    },
    {
      type: 'literal.uint64',
      label: 'Uint64 常量',
      description: '无符号整数集合常量',
      category: 'literal',
      outputType: 'uint64s',
      inputTypes: [],
      maxInputs: 0,
    },
    {
      type: 'literal.bool',
      label: 'Bool 常量',
      description: '布尔值字面量',
      category: 'literal',
      outputType: 'bool',
      inputTypes: [],
      maxInputs: 0,
    },
    {
      type: 'compare.int64',
      label: 'Int64 比较',
      description: '比较两个 int64 表达式',
      category: 'expression',
      outputType: 'bool',
      inputTypes: ['int64', 'int64'],
      maxInputs: 2,
    },
    {
      type: 'compare.strings',
      label: 'Strings 比较',
      description: '集合相等、包含或交集',
      category: 'expression',
      outputType: 'bool',
      inputTypes: ['strings', 'strings'],
      maxInputs: 2,
    },
    {
      type: 'compare.uint64',
      label: 'Uint64s 比较',
      description: '集合相等、包含或交集（无符号整数）',
      category: 'expression',
      outputType: 'bool',
      inputTypes: ['uint64s', 'uint64s'],
      maxInputs: 2,
    },
    {
      type: 'logic.and',
      label: 'AND',
      description: '合并多个布尔表达式',
      category: 'expression',
      outputType: 'bool',
      inputTypes: ['bool'],
      maxInputs: 16,
      variadic: true,
      variadicInputType: 'bool',
    },
    {
      type: 'logic.or',
      label: 'OR',
      description: '任一布尔表达式成立',
      category: 'expression',
      outputType: 'bool',
      inputTypes: ['bool'],
      maxInputs: 16,
      variadic: true,
      variadicInputType: 'bool',
    },
    {
      type: 'logic.not',
      label: 'NOT',
      description: '反转布尔表达式',
      category: 'expression',
      outputType: 'bool',
      inputTypes: ['bool'],
      maxInputs: 1,
    },
    {
      type: 'prefilter.lookup',
      label: 'Bitmap Lookup',
      description: '用索引生成候选 Bitmap',
      category: 'prefilter',
      outputType: 'bitmap',
      inputTypes: ['strings'],
      maxInputs: 1,
    },
    {
      type: 'prefilter.exclude',
      label: 'Bitmap Exclude',
      description: '从候选 Bitmap 排除子集',
      category: 'prefilter',
      outputType: 'bitmap',
      inputTypes: ['bitmap'],
      maxInputs: 1,
    },
    {
      type: 'prefilter.combine',
      label: 'Bitmap Combine',
      description: '合并多个候选 Bitmap 分支',
      category: 'prefilter',
      outputType: 'bitmap',
      inputTypes: ['bitmap'],
      maxInputs: 16,
      variadic: true,
      variadicInputType: 'bitmap',
    },
    {
      type: 'evaluation.join',
      label: 'canJoin',
      description: 'Evaluation 层加入判定',
      category: 'evaluation',
      outputType: 'bool',
      inputTypes: ['bool'],
      maxInputs: 1,
    },
    {
      type: 'evaluation.complete',
      label: 'canComplete',
      description: 'Evaluation 层完成判定',
      category: 'evaluation',
      outputType: 'bool',
      inputTypes: ['bool'],
      maxInputs: 1,
    },
  ] satisfies CapabilityNode[],
}

const regions = ['ap-southeast', 'eu-west', 'us-east']
const roles = ['tank', 'damage', 'support']

export const demoTickets: Ticket[] = Array.from({ length: 42 }, (_, index) => {
  const id = `ticket-${String(index + 1).padStart(4, '0')}`
  const matched = index < 12
  const region = regions[index % regions.length]
  const status = matched ? 'matched' : 'waiting'
  const facts: FactSnapshot = {
    preferredRoles: [roles[index % roles.length]],
    latencyMs: 18 + ((index * 7) % 90),
  }
  return {
    ticketId: id,
    createdAt: new Date(Date.UTC(2026, 7, 29, 8, 30, index)).toISOString(),
    attributes: {
      strings: { region: [region], modes: ['ranked'] },
      uint64s: {},
      int64: { playerLevel: 30 + (index % 40) },
    },
    facts,
    routeDecision: {
      status: 'routed',
      owner: {
        physicalNodeId: index % 2 === 0 ? 'physical-sea-1' : 'physical-sea-2',
        logicalNodeKey: `ranked-5v5/${index % 2 === 0 ? 'sea-1' : 'sea-2'}`,
        ruleKey: 'ranked-5v5',
        placementId: index % 2 === 0 ? 'sea-1' : 'sea-2',
      },
    },
    status,
    matchId: matched ? `match-${String(Math.floor(index / 4) + 1).padStart(3, '0')}` : undefined,
  }
})

export const demoMatches: MatchRecord[] = Array.from({ length: 3 }, (_, index) => ({
  matchId: `match-${String(index + 1).padStart(3, '0')}`,
  createdAt: new Date(Date.UTC(2026, 7, 29, 8, 35, index * 2)).toISOString(),
  roundId: 'round-0007',
  ruleKey: 'ranked-5v5',
  placementId: index % 2 === 0 ? 'sea-1' : 'sea-2',
  ticketIds: demoTickets.slice(index * 4, index * 4 + 4).map((ticket) => ticket.ticketId),
  memberCount: 4,
  members: structuredClone(demoTickets.slice(index * 4, index * 4 + 4)),
  facts: { teamSize: 4, averageLatencyMs: 38 + index * 3 },
  durationMs: 32 + index * 8,
}))

export const demoTopology: Topology = {
  updatedAt: '2026-08-29T09:14:22.000Z',
  nodes: [
    {
      id: 'physical-sea-1',
      name: 'SEA / primary',
      ruleKey: 'ranked-5v5',
      placementId: 'sea-1',
      ticketCount: 16,
      state: 'healthy',
      load: 0.62,
    },
    {
      id: 'physical-sea-2',
      name: 'SEA / overflow',
      ruleKey: 'ranked-5v5',
      placementId: 'sea-2',
      ticketCount: 14,
      state: 'healthy',
      load: 0.48,
    },
    {
      id: 'physical-eu-1',
      name: 'EU / primary',
      ruleKey: 'ranked-5v5',
      placementId: 'eu-1',
      ticketCount: 7,
      state: 'degraded',
      load: 0.81,
    },
  ],
  routes: [
    { from: 'router', to: 'physical-sea-1', tickets: 16 },
    { from: 'router', to: 'physical-sea-2', tickets: 14 },
    { from: 'router', to: 'physical-eu-1', tickets: 7 },
  ],
}

export const demoTicketsPage = (search = '', status?: string): TicketsPage => {
  const normalizedSearch = search.trim().toLowerCase()
  const filtered = demoTickets.filter((ticket) => {
    const matchesSearch =
      !normalizedSearch ||
      ticket.ticketId.toLowerCase().includes(normalizedSearch) ||
      Object.values(ticket.attributes.strings)
        .flat()
        .some((value) => value.includes(normalizedSearch))
    const matchesStatus = !status || status === 'all' || ticket.status === status
    return matchesSearch && matchesStatus
  })
  return { items: filtered, total: filtered.length }
}
