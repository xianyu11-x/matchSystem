import { expect, it } from 'vitest'
import { demoRuleSummary } from './mockData'
import {
  preferredTicketTarget,
  resolveTicketTarget,
  ticketTargetFields,
  ticketTargets,
} from './ticketTargets'
import type { RuleSummary } from '../types'

const deployedRule = (overrides: Partial<RuleSummary>): RuleSummary => ({
  ...demoRuleSummary,
  ...overrides,
})

it('按规范 RuleID 合并别名，并保留大整数节点和部署身份', () => {
  const ruleId = '9007199254740993'
  const targets = ticketTargets([
    deployedRule({
      ruleKey: ruleId,
      apiRule: { namespace: '旧/别名', ruleId },
      physicalNodeId: '18446744073709551615',
      placementId: '18446744073709551614',
    }),
    deployedRule({
      ruleKey: ruleId,
      apiRule: { namespace: '新:别名', ruleId },
      physicalNodeId: '2',
      placementId: '3',
    }),
    deployedRule({
      ruleKey: '9007199254740992',
      apiRule: { namespace: '新:别名', ruleId: '9007199254740992' },
      physicalNodeId: '3',
      placementId: '4',
    }),
  ])

  expect(targets.rule.map((target) => target.ruleId)).toEqual([ruleId, '9007199254740992'])
  expect(targets.node).toHaveLength(3)
  expect(targets.node[0].label).toContain('物理节点 18446744073709551615')
  expect(targets.node[0].label).toContain('部署 18446744073709551614')
  expect(ticketTargetFields(targets.rule[0])).toEqual({
    rule: { namespace: '旧/别名', ruleId },
  })
  expect(ticketTargetFields(targets.node[0])).toEqual({
    rule: { namespace: '旧/别名', ruleId },
    placementId: '18446744073709551614',
  })
})

it('停用目标不可投递；规则模式选择同规则的启用部署作为 Contract 代表', () => {
  const targets = ticketTargets([
    deployedRule({ physicalNodeId: '1', placementId: '1', physicalNodeEnabled: false }),
    deployedRule({ physicalNodeId: '2', placementId: '2', enabled: false }),
    deployedRule({ physicalNodeId: '3', placementId: '3', enabled: true }),
  ])
  expect(targets.rule).toHaveLength(1)
  expect(targets.rule[0].summary.physicalNodeId).toBe('3')
  expect(targets.node.map((node) => node.enabled)).toEqual([false, false, true])
  expect(targets.node[0].label).toContain('物理节点已停用')
  expect(targets.node[1].label).toContain('部署已停用')
  expect(ticketTargetFields(targets.node[0])).toBeUndefined()
  expect(preferredTicketTarget(targets.node, targets.rule[0])).toBe(targets.node[2])
})

it('所选节点删除、迁移或停用后不会自动改投其他节点', () => {
  const initial = ticketTargets([
    deployedRule({ physicalNodeId: '1', placementId: '7' }),
    deployedRule({ physicalNodeId: '2', placementId: '8' }),
  ])
  const selectedKey = initial.node[0].key
  expect(resolveTicketTarget(initial.node, undefined)).toBe(initial.node[0])

  const removed = ticketTargets([deployedRule({ physicalNodeId: '2', placementId: '8' })])
  expect(resolveTicketTarget(removed.node, selectedKey)).toBeUndefined()
  expect(ticketTargetFields(resolveTicketTarget(removed.node, selectedKey))).toBeUndefined()

  const moved = ticketTargets([deployedRule({ physicalNodeId: '3', placementId: '7' })])
  expect(resolveTicketTarget(moved.node, selectedKey)).toBeUndefined()

  const disabled = ticketTargets([
    deployedRule({ physicalNodeId: '1', placementId: '7', enabled: false }),
    deployedRule({ physicalNodeId: '2', placementId: '8' }),
  ])
  const selected = resolveTicketTarget(disabled.node, selectedKey)
  expect(selected?.key).toBe(selectedKey)
  expect(ticketTargetFields(selected)).toBeUndefined()
})

it('不提供无效或无法精确表示的节点部署作为直接投递目标', () => {
  const targets = ticketTargets([
    deployedRule({ physicalNodeId: '1', placementId: '9007199254740992', placementIdValid: false }),
    deployedRule({ physicalNodeId: '0', placementId: '7' }),
  ])
  expect(targets.node).toHaveLength(0)
  expect(targets.rule).toHaveLength(1)
})
