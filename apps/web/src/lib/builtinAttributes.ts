import type { AttributeSpec, BuiltinAttribute, Capabilities, LogicalNodeContract } from '../types'

/** 合并只读目录和用户字段，仅供编辑与校验，不修改规则文档。 */
export function effectiveAttributes(
  contract: LogicalNodeContract,
  builtins: readonly BuiltinAttribute[] = [],
): AttributeSpec[] {
  const declared = new Set(contract.attributes.map((item) => item.name))
  return [
    ...contract.attributes,
    ...builtins
      .filter((item) => !declared.has(item.name))
      .map(({ name, type }) => ({ name, type })),
  ]
}

export function isReadonlyBuiltin(name: string, builtins: readonly BuiltinAttribute[]): boolean {
  return builtins.some((item) => item.name === name && item.readOnly)
}

export function omitReadonlyBuiltins<T>(
  values: Record<string, T>,
  builtins: readonly BuiltinAttribute[],
): Record<string, T> {
  return Object.fromEntries(
    Object.entries(values).filter(([name]) => !isReadonlyBuiltin(name, builtins)),
  )
}

/** 能力尚未到达时等待同一查询；不能把加载中当作旧服务。 */
export async function capabilitiesForAction(query: {
  data?: Capabilities
  refetch: () => Promise<{ data?: Capabilities }>
}): Promise<Capabilities> {
  if (query.data) return query.data
  const result = await query.refetch()
  if (!result.data) throw new Error('读取服务能力失败，请重试后再导入或保存规则。')
  return result.data
}
