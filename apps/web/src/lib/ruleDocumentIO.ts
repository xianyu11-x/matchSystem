import { buildRuleGraph } from './graphBuilder'
import type { ApiRuleKey, MatchRuleDocument, RuleDocument } from '../types'

/** Portable files are the exact one-rule match-rule/v1 document. */
export type PortableRuleDocument = MatchRuleDocument

const isObject = (value: unknown): value is Record<string, unknown> =>
  Boolean(value && typeof value === 'object' && !Array.isArray(value))

function apiRuleKeyForDocument(document: RuleDocument): ApiRuleKey {
  const key = document.apiRule
  if (key && Number.isInteger(key.ruleId) && key.ruleId > 0)
    return { namespace: key.namespace || undefined, ruleId: key.ruleId }
  const parts = document.ruleKey.split(/[/:#]/).filter(Boolean)
  const ruleId = Number(parts.at(-1))
  if (!Number.isInteger(ruleId) || ruleId <= 0)
    throw new Error('当前规则缺少有效的 ApiRuleKey，无法导出 match-rule/v1')
  return {
    namespace: parts.length > 1 ? parts.slice(0, -1).join('/') : undefined,
    ruleId,
  }
}

function sameRuleKey(left: ApiRuleKey, right: ApiRuleKey): boolean {
  return (
    left.ruleId === right.ruleId &&
    (left.namespace || undefined) === (right.namespace || undefined)
  )
}

function parseApiRuleKey(value: unknown): ApiRuleKey {
  if (!isObject(value) || typeof value.ruleId !== 'number' || !Number.isInteger(value.ruleId) || value.ruleId <= 0)
    throw new Error('文件的 ruleKey 必须包含正整数 ruleId')
  if (value.namespace !== undefined && typeof value.namespace !== 'string')
    throw new Error('文件的 ruleKey.namespace 必须是字符串')
  return {
    namespace: value.namespace ? value.namespace : undefined,
    ruleId: value.ruleId,
  }
}

function requireRuleObject(value: unknown, name: string): Record<string, unknown> {
  if (!isObject(value)) throw new Error(`文件必须包含 ${name} object`)
  return value
}

export function portableRuleDocument(document: RuleDocument): PortableRuleDocument {
  return {
    schemaVersion: 'match-rule/v1',
    ruleKey: apiRuleKeyForDocument(document),
    contract: structuredClone(document.contract),
    prefilter: structuredClone(document.prefilter),
    evaluation: structuredClone(document.evaluation),
    scoring: structuredClone(document.scoring),
    seedSelection: structuredClone(document.seedSelection),
    runtime: structuredClone(document.runtime),
  }
}

export function importRuleDocument(value: unknown, current: RuleDocument): RuleDocument {
  if (!isObject(value)) throw new Error('导入文件的根必须是 JSON object')
  if (value.schemaVersion !== 'match-rule/v1') throw new Error('仅支持 match-rule/v1')

  const importedKey = parseApiRuleKey(value.ruleKey)
  const currentKey = apiRuleKeyForDocument(current)
  if (!sameRuleKey(importedKey, currentKey))
    throw new Error(
      `文件属于 ${importedKey.namespace ? `${importedKey.namespace}/` : ''}${importedKey.ruleId}，当前规则为 ${currentKey.namespace ? `${currentKey.namespace}/` : ''}${currentKey.ruleId}`,
    )

  requireRuleObject(value.contract, 'contract')
  requireRuleObject(value.prefilter, 'prefilter')
  requireRuleObject(value.evaluation, 'evaluation')
  requireRuleObject(value.scoring, 'scoring')
  requireRuleObject(value.seedSelection, 'seedSelection')
  requireRuleObject(value.runtime, 'runtime')

  const next: RuleDocument = {
    ...current,
    apiRule: structuredClone(importedKey),
    contract: structuredClone(value.contract) as RuleDocument['contract'],
    prefilter: structuredClone(value.prefilter) as RuleDocument['prefilter'],
    evaluation: structuredClone(value.evaluation) as RuleDocument['evaluation'],
    scoring: structuredClone(value.scoring) as RuleDocument['scoring'],
    seedSelection: structuredClone(value.seedSelection) as RuleDocument['seedSelection'],
    runtime: structuredClone(value.runtime) as RuleDocument['runtime'],
  }
  return { ...next, graph: buildRuleGraph(next) }
}

export function portableRuleFileName(document: RuleDocument): string {
  const safe = (value: string) => value.replace(/[^a-zA-Z0-9._-]+/g, '-')
  const key = apiRuleKeyForDocument(document)
  const text = key.namespace ? `${key.namespace}-${key.ruleId}` : String(key.ruleId)
  return `matchscope-${safe(text)}.json`
}
