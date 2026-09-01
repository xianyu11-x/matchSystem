import type {
  FactSpec,
  LogicalNodeFactsResponse,
  ProviderDescriptorSet,
  RuntimeFactValues,
} from '../types'

export interface RuleFactSources {
  contractFacts?: FactSpec[]
  providerDescriptors?: ProviderDescriptorSet
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
 * truthiness: an empty Provider Descriptor object or an empty runtime snapshot
 * is an explicit value and must not be replaced with stale query metadata.
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
      local?.providerDescriptors !== undefined
        ? local.providerDescriptors
        : metadata?.providerDescriptors ?? {},
    runtimeFacts:
      local?.runtimeFacts !== undefined
        ? local.runtimeFacts
        : metadata?.runtimeFacts ?? { tick: {} },
  }
}
