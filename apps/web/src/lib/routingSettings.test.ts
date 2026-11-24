import { describe, expect, it } from 'vitest'
import {
  routingConfigFromForm,
  routingFormFromScenario,
  routingFormWithAlgorithm,
} from './routingSettings'

describe('Router 路由配置表单', () => {
  it('旧场景使用默认策略，切换权重优先时初始化阈值', () => {
    const original = routingFormFromScenario({})
    expect(original).toEqual({ algorithm: 'weighted_rendezvous', ticketLimit: '' })
    const priority = routingFormWithAlgorithm(original, 'weighted_priority')
    expect(priority).toEqual({ algorithm: 'weighted_priority', ticketLimit: '50' })
    expect(routingConfigFromForm(priority)).toEqual({
      algorithm: 'weighted_priority',
      ticketLimit: 50,
    })
    const random = routingFormWithAlgorithm(priority, 'queue_weighted_random')
    expect(routingConfigFromForm(random)).toEqual({ algorithm: 'queue_weighted_random' })
    expect(routingFormWithAlgorithm(random, 'weighted_priority').ticketLimit).toBe('50')
  })

  it('非优先策略加载时忽略历史阈值，保存时不发送', () => {
    const form = routingFormFromScenario({
      routing: { algorithm: 'queue_weighted_random', ticketLimit: 7 },
    })
    expect(form.ticketLimit).toBe('')
    expect(routingConfigFromForm(form)).toEqual({ algorithm: 'queue_weighted_random' })
    expect(routingConfigFromForm({ algorithm: 'weighted_rendezvous', ticketLimit: '0' })).toEqual({
      algorithm: 'weighted_rendezvous',
      ticketLimit: 0,
    })
  })

  it.each(['', '0', '-1', '1.5', '9007199254740992', '1e2'])(
    '权重优先拒绝无效阈值 %s',
    (ticketLimit) => {
      expect(() => routingConfigFromForm({ algorithm: 'weighted_priority', ticketLimit })).toThrow()
    },
  )

  it('接受安全整数上限并拒绝未知算法', () => {
    expect(
      routingConfigFromForm({
        algorithm: 'weighted_priority',
        ticketLimit: String(Number.MAX_SAFE_INTEGER),
      }).ticketLimit,
    ).toBe(Number.MAX_SAFE_INTEGER)
    expect(() => routingConfigFromForm({ algorithm: 'unknown', ticketLimit: '' })).toThrow('有效')
  })
})
