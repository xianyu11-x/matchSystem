import type { AttributeGenerator, BatchGeneratorSpec } from '../types'

export const splitList = (value: string) =>
  value
    .split(/[,，\n]/)
    .map((part) => part.trim())
    .filter(Boolean)

export function parseSafeInteger(
  value: string,
  label: string,
  min = Number.MIN_SAFE_INTEGER,
  max = Number.MAX_SAFE_INTEGER,
): number {
  const text = value.trim()
  const parsed = Number(text)
  if (!/^-?\d+$/.test(text) || !Number.isSafeInteger(parsed) || parsed < min || parsed > max)
    throw new Error(`${label}：请输入 ${min} 至 ${max} 之间的整数`)
  return parsed
}

export function parseInputValue(
  raw: string,
  type: string,
  label: string,
): number | string[] | number[] {
  if (type === 'strings') return splitList(raw)
  if (type === 'uint64s') return splitList(raw).map((value) => parseSafeInteger(value, label, 0))
  return parseSafeInteger(raw, label)
}

export function validateBatch(spec: BatchGeneratorSpec, continuous = false): void {
  if (!continuous) parseSafeInteger(String(spec.count), '生成数量', 1, 1000000)
  parseSafeInteger(String(spec.seed), '随机种子')
  if (!continuous && spec.createdAtStep !== undefined) {
    parseSafeInteger(String(spec.createdAtStep), '创建时间步长')
    const last = (spec.createdAtStart || Date.now()) + (spec.createdAtStep || 1) * (spec.count - 1)
    if (!Number.isSafeInteger(last) || last < 0)
      throw new Error('最后一条对象的创建时间超出非负安全整数范围')
  }
  if (spec.startTicketId !== undefined) {
    parseSafeInteger(String(spec.startTicketId), '起始 Ticket ID', 1)
    if (!continuous && spec.startTicketId + spec.count - 1 > Number.MAX_SAFE_INTEGER)
      throw new Error('最后一条 Ticket ID 超出安全整数范围，请减小起始 ID 或数量')
  }
  const generators = spec.attributeGenerators ?? {}
  const done = new Set<string>()
  const visiting = new Set<string>()
  function check(name: string) {
    if (done.has(name)) return
    if (visiting.has(name)) throw new Error(`${name}：共享属性存在循环引用`)
    const g: AttributeGenerator = generators[name]
    visiting.add(name)
    if (g.source === 'shared') {
      if (!g.ref || !generators[g.ref] || generators[g.ref].type !== g.type)
        throw new Error(`${name}：请选择已启用的同类型来源属性`)
      check(g.ref)
    } else if (!g.source || g.source === 'sample') {
      const integer = (value: string | undefined, signed: boolean): bigint => {
        if (!value || !/^-?\d+$/.test(value.trim())) throw new Error(`${name}：请输入十进制整数`)
        const n = BigInt(value)
        if (n < (signed ? -(1n << 63n) : 0n) || n > (signed ? (1n << 63n) - 1n : (1n << 64n) - 1n))
          throw new Error(`${name}：数值超出 ${signed ? 'int64' : 'uint64'} 范围`)
        return n
      }
      let size = 0n
      if (g.type === 'int64') {
        if (integer(g.min, true) > integer(g.max, true))
          throw new Error(`${name}：最小值不能大于最大值`)
      } else if (g.type === 'strings') {
        const values = (g.values ?? []).map((v) => v.trim())
        if (!values.length || values.some((v) => !v)) throw new Error(`${name}：候选值不能为空`)
        size = BigInt(new Set(values).size)
      } else {
        const intervals = splitList(g.set ?? '')
          .map((part) => {
            const match = /^(\d+)(?:\s*-\s*(\d+))?$/.exec(part)
            if (!match) throw new Error(`${name}：集合请填写单值或闭区间，例如 1-100，200-400`)
            const lo = integer(match[1], false),
              hi = integer(match[2] ?? match[1], false)
            if (lo > hi) throw new Error(`${name}：区间起点不能大于终点`)
            return [lo, hi]
          })
          .sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0))
        if (!intervals.length) throw new Error(`${name}：请填写候选集合`)
        let end = -1n
        for (const [lo, hi] of intervals) {
          const start = lo > end + 1n ? lo : end + 1n
          if (hi >= start) size += hi - start + 1n
          if (hi > end) end = hi
        }
      }
      if (g.type !== 'int64') {
        const count = parseSafeInteger(String(g.count ?? 1), `${name} 抽取数量`, 0, 4096)
        if (!g.replacement && BigInt(count) > size)
          throw new Error(`${name}：不重复抽取数量超过候选集合大小`)
      }
    }
    visiting.delete(name)
    done.add(name)
  }
  Object.keys(generators).forEach(check)
}
