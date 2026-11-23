import type { ApiRuleKey } from '../types'

const maxUint64 = (1n << 64n) - 1n

/** 从原始 JSON 数值恢复超出安全整数范围的规则身份。 */
export function parseRuleIdentityJson(text: string): unknown {
  const parse = JSON.parse as unknown as (
    input: string,
    reviver: (key: string, value: unknown, context?: { source?: string }) => unknown,
  ) => unknown
  return parse(text, (key, value, context) => {
    if (key === 'ruleId' && typeof value === 'number' && !Number.isSafeInteger(value)) {
      if (!context?.source || !/^\d+$/.test(context.source))
        throw new Error('规则身份中的大整数无法精确解析，请使用十进制字符串。')
      return context.source
    }
    return value
  })
}

/** 旧版场景编辑器会重新序列化 JSON，拒绝可能被舍入的大整数。 */
export function parseEditableJson(text: string): unknown {
  const parsed = parseRuleIdentityJson(text)
  const hasUnsafeInteger = (value: unknown): boolean => {
    if (typeof value === 'number') return Number.isInteger(value) && !Number.isSafeInteger(value)
    if (value && typeof value === 'object') return Object.values(value).some(hasUnsafeInteger)
    return false
  }
  if (hasUnsafeInteger(parsed))
    throw new Error('导入的 JSON 包含无法精确表示的整数，请使用十进制字符串。')
  return parsed
}

/** 只接受可精确表示的 JSON 数值或规范十进制字符串。 */
export function uint64Text(value: unknown, positive = false): string | undefined {
  if (typeof value === 'number') {
    if (!Number.isSafeInteger(value) || value < 0 || Object.is(value, -0)) return undefined
    value = String(value)
  }
  if (typeof value !== 'string' || value.trim() !== value || !/^(?:0|[1-9]\d*)$/.test(value))
    return undefined
  try {
    const id = BigInt(value)
    return id <= maxUint64 && (!positive || id > 0n) ? id.toString() : undefined
  } catch {
    return undefined
  }
}

export function positiveRuleId(value: unknown): string | undefined {
  return uint64Text(value, true)
}

export function parseRuleKey(value: unknown): ApiRuleKey | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined
  const source = value as Record<string, unknown>
  const id = positiveRuleId(source.ruleId)
  if (!id) return undefined
  if (source.namespace !== undefined && typeof source.namespace !== 'string') return undefined
  return {
    ...(source.namespace !== undefined ? { namespace: source.namespace as string } : {}),
    ruleId: id,
  }
}

export function ruleIdentity(key: ApiRuleKey): string {
  return positiveRuleId(key.ruleId) ?? String(key.ruleId ?? '')
}

export function ruleIdentityLabel(key: ApiRuleKey): string {
  return `${key.namespace ? `${key.namespace}/` : ''}${key.ruleId}`
}
