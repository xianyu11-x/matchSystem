import { parseSafeInteger } from './composerValidation'
import { inProcessEndpoint } from './physicalNodeEndpoint'
import { uint64Text, parseRuleKey, ruleIdentity, ruleIdentityLabel } from './ruleIdentity'
import type {
  ApiRuleKey,
  JsonObject,
  JsonValue,
  MatchRuleDocument,
  PhysicalNodeCreateRequest,
  PythonProviderConfig,
} from '../types'

export interface DeploymentForm {
  rowId: string
  ruleId: string
  placementId: string
  originalIdentity?: string
  originalRuleId?: string
  originalRule?: ApiRuleKey
  weight: string
  enabled: boolean
  tickFacts: string
  originalTickFacts?: string
  pythonEnabled: boolean
  python: string
  runner: string
  script: string
  timeoutMs: string
  applyLatest: boolean
}

export interface PhysicalNodeForm {
  id: string
  enabled: boolean
  selector: string
  deployments: DeploymentForm[]
  baseline?: string
}

const object = (value: JsonValue | undefined): JsonObject =>
  value && typeof value === 'object' && !Array.isArray(value) ? value : {}
const objects = (value: JsonValue | undefined): JsonObject[] =>
  Array.isArray(value) ? value.map(object) : []
const rulesIn = (scenario?: JsonObject): JsonObject[] => objects(scenario?.rules)

let nextRowId = 0

export function newDeploymentForm(): DeploymentForm {
  return {
    rowId: String(++nextRowId),
    ruleId: '',
    placementId: '',
    weight: '1',
    enabled: true,
    tickFacts: '',
    pythonEnabled: false,
    python: '',
    runner: '',
    script: '',
    timeoutMs: '',
    applyLatest: false,
  }
}

export function newPhysicalNodeForm(): PhysicalNodeForm {
  return { id: '', enabled: true, selector: 'round_robin', deployments: [] }
}

function keyFromValue(value: JsonValue | undefined): ApiRuleKey | undefined {
  return parseRuleKey(value)
}

export function keyFromSelection(value: string): ApiRuleKey | undefined {
  return parseRuleKey({ ruleId: value })
}

export const deploymentIdentity = (key: ApiRuleKey, placementId: string): string =>
  JSON.stringify([ruleIdentity(key), uint64Text(placementId, true) ?? placementId])

const sameNodeId = (left: unknown, right: unknown): boolean => {
  const normalized = uint64Text(left, true)
  return normalized !== undefined && normalized === uint64Text(right, true)
}

function identityOfRow(row: JsonObject): string | undefined {
  const logical = object(row.logicalNode)
  const key = keyFromValue(logical.rule)
  const placementId = uint64Text(logical.placementId, true)
  return key && placementId ? deploymentIdentity(key, placementId) : undefined
}

function nodeBaseline(
  scenario: JsonObject,
  id: string,
  physical: JsonObject | undefined,
): string | undefined {
  if (!physical) return undefined
  return JSON.stringify({
    physical,
    deployments: rulesIn(scenario).filter((row) => sameNodeId(row.physicalNodeId, id)),
  })
}

export function physicalNodeFormFromScenario(scenario: JsonObject, id: string): PhysicalNodeForm {
  const physical = objects(scenario.physicalNodes).find((node) => sameNodeId(node.id, id))
  const baseline = nodeBaseline(scenario, id, physical)
  if (!physical || !baseline) throw new Error('物理节点配置已不存在，请刷新后重试')
  const deployments = rulesIn(scenario)
    .filter((row) => sameNodeId(row.physicalNodeId, id))
    .map((row) => {
      const logical = object(row.logicalNode)
      const key = keyFromValue(logical.rule)
      const placementId = uint64Text(logical.placementId, true)
      if (!key || !placementId) throw new Error('逻辑节点配置无效，无法编辑')
      const provider = object(row.pythonProvider)
      return {
        ...newDeploymentForm(),
        ruleId: ruleIdentity(key),
        originalRuleId: ruleIdentity(key),
        originalRule: key,
        placementId,
        originalIdentity: deploymentIdentity(key, placementId),
        weight: String(row.weight ?? 1),
        enabled: row.enabled === true,
        tickFacts: row.tickFacts === undefined ? '' : JSON.stringify(row.tickFacts, null, 2),
        originalTickFacts:
          row.tickFacts === undefined ? '' : JSON.stringify(row.tickFacts, null, 2),
        pythonEnabled: row.pythonProvider !== undefined,
        python: typeof provider.python === 'string' ? provider.python : '',
        runner: typeof provider.runner === 'string' ? provider.runner : '',
        script: typeof provider.script === 'string' ? provider.script : '',
        timeoutMs: typeof provider.timeoutMs === 'number' ? String(provider.timeoutMs) : '',
      }
    })
  return {
    id,
    enabled: physical.enabled === true,
    selector:
      typeof physical.selector === 'string' && physical.selector
        ? physical.selector
        : 'round_robin',
    deployments,
    baseline,
  }
}

export function physicalNodeRuleOptions(
  rules: MatchRuleDocument[],
): Array<{ value: string; label: string }> {
  return rules.flatMap(({ ruleKey }) => {
    const key = parseRuleKey(ruleKey)
    return key ? [{ value: ruleIdentity(key), label: ruleIdentityLabel(key) }] : []
  })
}

export function hasOriginalIdentity(row: DeploymentForm): boolean {
  const key = keyFromSelection(row.ruleId)
  return !!key && row.originalIdentity === deploymentIdentity(key, row.placementId)
}

function validateTickFactNumbers(text: string, label: string): void {
  let inString = false
  let escaped = false
  for (let index = 0; index < text.length; index++) {
    const char = text[index]
    if (inString) {
      if (escaped) escaped = false
      else if (char === '\\') escaped = true
      else if (char === '"') inString = false
      continue
    }
    if (char === '"') {
      inString = true
      continue
    }
    if (char !== '-' && (char < '0' || char > '9')) continue
    const token = text.slice(index).match(/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/)?.[0]
    if (!token) continue
    if (!/^-?(?:0|[1-9]\d*)$/.test(token) || !Number.isSafeInteger(Number(token)))
      throw new Error(`${label}：Tick Facts 数值必须是 JavaScript 安全范围内的十进制整数`)
    index += token.length - 1
  }
}

export function physicalNodeRequest(
  form: PhysicalNodeForm,
  scenario: JsonObject,
  library: MatchRuleDocument[],
): PhysicalNodeCreateRequest {
  if (!uint64Text(form.id, true)) throw new Error('物理节点标识必须为无前导零的正 uint64 十进制数')
  const physical = objects(scenario.physicalNodes).find((node) => sameNodeId(node.id, form.id))
  if (form.baseline !== undefined && nodeBaseline(scenario, form.id, physical) !== form.baseline)
    throw new Error('节点配置已变化，请重新打开编辑后重试')
  if (form.baseline === undefined && physical) throw new Error('物理节点标识已存在')

  const available = new Map(
    library.flatMap(({ ruleKey }) => {
      const key = parseRuleKey(ruleKey)
      return key ? [[ruleIdentity(key), key] as const] : []
    }),
  )
  const existing = new Set(
    rulesIn(scenario)
      .filter((row) => !sameNodeId(row.physicalNodeId, form.id))
      .map(identityOfRow)
      .filter((identity): identity is string => identity !== undefined),
  )
  const selected = new Set<string>()
  const deployments = form.deployments.map((row, index) => {
    const label = `部署 ${index + 1}`
    const selectedKey = keyFromSelection(row.ruleId)
    if (!selectedKey) throw new Error(`${label}：请选择规则`)
    const keyId = ruleIdentity(selectedKey)
    if (selected.has(keyId)) throw new Error(`${label}：同一节点不能重复部署同一规则`)
    selected.add(keyId)
    if (!uint64Text(row.placementId, true))
      throw new Error(`${label}：PlacementID 必须为无前导零的正 uint64 十进制数`)
    const identity = deploymentIdentity(selectedKey, row.placementId)
    if (existing.has(identity)) throw new Error(`${label}：此规则的 PlacementID 已存在`)
    const unchanged = hasOriginalIdentity(row)
    if ((!unchanged || row.applyLatest) && !available.has(keyId))
      throw new Error(`${label}：规则已不在规则库中，请重新选择`)
    const original = row.originalRule && parseRuleKey(row.originalRule)
    const key =
      unchanged && !row.applyLatest
        ? original && ruleIdentity(original) === keyId
          ? original
          : (available.get(keyId) ?? selectedKey)
        : available.get(keyId)!

    const keepTickFacts =
      unchanged && row.originalTickFacts !== undefined && row.tickFacts === row.originalTickFacts
    let tickFacts: JsonObject | undefined
    if (!keepTickFacts && row.tickFacts.trim()) {
      let parsed: unknown
      try {
        parsed = JSON.parse(row.tickFacts)
      } catch {
        throw new Error(`${label}：Tick Facts 必须是有效 JSON 对象`)
      }
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed))
        throw new Error(`${label}：Tick Facts 必须是 JSON 对象`)
      validateTickFactNumbers(row.tickFacts, label)
      tickFacts = parsed as JsonObject
    }
    let pythonProvider: PythonProviderConfig | undefined
    if (row.pythonEnabled) {
      if (!row.script) throw new Error(`${label}：Provider 脚本不能为空`)
      pythonProvider = {
        script: row.script,
        ...(row.python ? { python: row.python } : {}),
        ...(row.runner ? { runner: row.runner } : {}),
        ...(row.timeoutMs
          ? { timeoutMs: parseSafeInteger(row.timeoutMs, `${label}的 RPC 超时`, 0) }
          : {}),
      }
    }
    return {
      logicalNode: { rule: key, placementId: row.placementId },
      weight: parseSafeInteger(row.weight, `${label}的路由权重`, 1, 4294967295),
      enabled: row.enabled,
      ...(tickFacts ? { tickFacts } : {}),
      ...(keepTickFacts ? { keepTickFacts: true } : {}),
      ...(pythonProvider ? { pythonProvider } : {}),
      ...(unchanged && row.applyLatest ? { applyLatest: true } : {}),
    }
  })
  return {
    physicalNode: {
      id: form.id,
      endpoint:
        form.baseline === undefined ? inProcessEndpoint(form.id) : String(physical?.endpoint ?? ''),
      enabled: form.enabled,
      selector: form.selector,
    },
    deployments,
  }
}
