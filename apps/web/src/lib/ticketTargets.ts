import type { ApiRuleKey, RuleSummary } from '../types'
import { parseRuleKey, ruleIdentity, ruleIdentityLabel, uint64Text } from './ruleIdentity'

export type TicketTargetMode = 'rule' | 'node'

export interface TicketTarget {
  mode: TicketTargetMode
  key: string
  ruleId: string
  apiRule: ApiRuleKey
  summary: RuleSummary
  label: string
  enabled: boolean
  placementId?: string
}

export function ticketTargets(rules: RuleSummary[]): Record<TicketTargetMode, TicketTarget[]> {
  const byRule = new Map<string, TicketTarget>()
  const nodes: TicketTarget[] = []
  for (const summary of rules) {
    const apiRule = parseRuleKey(summary.apiRule ?? { ruleId: summary.ruleKey })
    if (!apiRule) continue
    const ruleId = ruleIdentity(apiRule)
    const enabled = summary.enabled && summary.physicalNodeEnabled !== false
    const ruleLabel = ruleIdentityLabel(apiRule)
    const ruleTarget: TicketTarget = {
      mode: 'rule',
      key: ruleId,
      ruleId,
      apiRule,
      summary,
      label: `规则 ${ruleLabel}${enabled ? '' : ' · 无可用部署'}`,
      enabled,
    }
    const existing = byRule.get(ruleId)
    if (!existing || (!existing.enabled && enabled)) byRule.set(ruleId, ruleTarget)

    const physicalNodeId = uint64Text(summary.physicalNodeId, true)
    const placementId =
      summary.placementIdValid === false ? undefined : uint64Text(summary.placementId, true)
    if (!physicalNodeId || !placementId) continue
    const disabledReason = !summary.enabled
      ? ' · 部署已停用'
      : summary.physicalNodeEnabled === false
        ? ' · 物理节点已停用'
        : ''
    nodes.push({
      mode: 'node',
      key: JSON.stringify([physicalNodeId, ruleId, placementId]),
      ruleId,
      apiRule,
      summary,
      placementId,
      label: `物理节点 ${physicalNodeId} · 部署 ${placementId} · 规则 ${ruleLabel}${disabledReason}`,
      enabled,
    })
  }
  return { rule: [...byRule.values()], node: nodes }
}

export function resolveTicketTarget(
  targets: TicketTarget[],
  selectedKey: string | undefined,
): TicketTarget | undefined {
  return selectedKey === undefined
    ? targets.find((target) => target.enabled)
    : targets.find((target) => target.key === selectedKey)
}

export function preferredTicketTarget(
  targets: TicketTarget[],
  current?: TicketTarget,
): TicketTarget | undefined {
  return (
    targets.find((target) => target.enabled && target.ruleId === current?.ruleId) ??
    targets.find((target) => target.enabled)
  )
}

export function ticketTargetFields(
  target: TicketTarget | undefined,
): { rule: ApiRuleKey; placementId?: string } | undefined {
  if (!target?.enabled) return undefined
  if (target.mode === 'node') {
    if (!target.placementId) return undefined
    return { rule: target.apiRule, placementId: target.placementId }
  }
  return { rule: target.apiRule }
}
