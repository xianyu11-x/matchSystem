import type { JsonObject, RoutingAlgorithm, RoutingConfig } from '../types'
import { parseSafeInteger } from './composerValidation'

export interface RoutingForm {
  algorithm: string
  ticketLimit: string
}

export function routingFormFromScenario(scenario?: JsonObject): RoutingForm {
  const routing = scenario?.routing
  const config = routing && typeof routing === 'object' && !Array.isArray(routing) ? routing : {}
  const algorithm =
    typeof config.algorithm === 'string' && config.algorithm
      ? config.algorithm
      : 'weighted_rendezvous'
  return {
    algorithm,
    ticketLimit:
      algorithm === 'weighted_priority' && config.ticketLimit !== undefined
        ? String(config.ticketLimit)
        : '',
  }
}

export function routingFormWithAlgorithm(form: RoutingForm, algorithm: string): RoutingForm {
  if (algorithm !== 'weighted_priority') return { algorithm, ticketLimit: '' }
  const limit = Number(form.ticketLimit)
  return {
    algorithm,
    ticketLimit:
      form.ticketLimit.trim() && Number.isSafeInteger(limit) && limit > 0 ? form.ticketLimit : '50',
  }
}

export function routingConfigFromForm(form: RoutingForm): RoutingConfig {
  if (
    !['weighted_rendezvous', 'queue_weighted_random', 'weighted_priority'].includes(form.algorithm)
  )
    throw new Error('请选择有效的 Router 路由算法')
  const priority = form.algorithm === 'weighted_priority'
  if (priority && !form.ticketLimit.trim()) throw new Error('权重优先策略需要填写正整数阈值')
  return {
    algorithm: form.algorithm as RoutingAlgorithm,
    ...(form.ticketLimit.trim()
      ? { ticketLimit: parseSafeInteger(form.ticketLimit, '队列阈值', priority ? 1 : 0) }
      : {}),
  }
}
