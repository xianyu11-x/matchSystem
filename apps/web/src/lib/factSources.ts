import type {
  FactSpec,
  LogicalNodeFactsResponse,
  ProviderDescriptorSet,
  RuntimeFactValues,
} from '../types'

export interface RuleFactSources {
  contractFacts?: FactSpec[]
  runtimeFacts?: RuntimeFactValues
}

export interface ResolvedRuleFactSources {
  contractFacts: FactSpec[]
  providerDescriptors: ProviderDescriptorSet
  runtimeFacts: RuntimeFactValues
}

/**
 * Resolve all three Fact layers from one selected-rule source.
 *
 * `local` is deliberately checked by property presence rather than by
 * truthiness: an empty Contract list or runtime snapshot is an explicit value
 * and must not be replaced with stale query metadata. Provider descriptors
 * always come from the selected running LogicalNode metadata and are read-only.
 * When the selected rule document is unavailable (for example during a rule
 * switch), callers pass `undefined` and the metadata query may provide a
 * temporary fallback.
 */
export function resolveRuleFactSources(
  local: RuleFactSources | undefined,
  metadata?: Partial<LogicalNodeFactsResponse>,
): ResolvedRuleFactSources {
  return {
    contractFacts:
      local?.contractFacts !== undefined
        ? local.contractFacts
        : metadata?.contractFacts ?? metadata?.facts ?? [],
    providerDescriptors:
      metadata?.providerDescriptors ?? {},
    runtimeFacts:
      local?.runtimeFacts !== undefined
        ? local.runtimeFacts
        : metadata?.runtimeFacts ?? { tick: {} },
  }
}
