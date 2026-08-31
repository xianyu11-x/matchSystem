import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './api'
import type {
  ApiRuleKey,
  BatchGeneratorSpec,
  RuleDocument,
  RunRoundInput,
  TicketInput,
} from '../types'

export const queryKeys = {
  capabilities: ['capabilities'] as const,
  scenario: ['scenario'] as const,
  topology: ['topology'] as const,
  rule: (ruleKey: string, placementId: string) => ['rule', ruleKey, placementId] as const,
  match: (matchId: string) => ['match', matchId] as const,
  logicalNodeFacts: (rule: ApiRuleKey | undefined, placementId: string | undefined) =>
    [
      'logical-node-facts',
      rule?.namespace ?? '',
      rule?.ruleId ?? 0,
      placementId ?? '',
    ] as const,
  tickets: (params: Record<string, unknown>) => ['tickets', params] as const,
  matches: ['matches'] as const,
}

export function useCapabilities() {
  return useQuery({ queryKey: queryKeys.capabilities, queryFn: api.getCapabilities })
}

export function useScenario() {
  return useQuery({ queryKey: queryKeys.scenario, queryFn: api.getScenario, staleTime: 15_000 })
}

export function useTopology() {
  return useQuery({
    queryKey: queryKeys.topology,
    queryFn: api.getTopology,
    refetchInterval: 10_000,
  })
}

export function useRule(ruleKey: string | undefined, placementId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.rule(ruleKey ?? '', placementId ?? ''),
    queryFn: () => api.getRule(ruleKey ?? '', placementId ?? ''),
    enabled: Boolean(ruleKey && placementId),
  })
}

/**
 * Reads the Fact contract advertised by the selected LogicalNode's provider.
 * The query is disabled until both the API rule identity and placement are
 * known, so changing the Rule selector naturally scopes the cache and request.
 */
export function useLogicalNodeFacts(
  rule: ApiRuleKey | undefined,
  placementId: string | undefined,
  enabled = true,
) {
  return useQuery({
    queryKey: queryKeys.logicalNodeFacts(rule, placementId),
    queryFn: () => api.getLogicalNodeFacts(rule!, placementId!),
    enabled: enabled && Boolean(rule && Number.isInteger(rule.ruleId) && placementId),
    staleTime: 60_000,
  })
}

export function useTickets(params: {
  cursor?: string
  limit?: number
  search?: string
  status?: string
}) {
  return useQuery({
    queryKey: queryKeys.tickets(params),
    queryFn: () => api.getTickets(params),
    placeholderData: (previous) => previous,
  })
}

export function useMatches() {
  return useQuery({
    queryKey: queryKeys.matches,
    queryFn: () => api.getMatches({ limit: 50 }),
    refetchInterval: 10_000,
  })
}

export function useMatch(matchId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.match(matchId ?? ''),
    queryFn: () => api.getMatch(matchId!),
    enabled: Boolean(matchId),
    staleTime: 60_000,
  })
}

async function refreshScenarioBoundQueries(queryClient: ReturnType<typeof useQueryClient>) {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: queryKeys.scenario }),
    queryClient.invalidateQueries({ queryKey: ['rule'] }),
    queryClient.invalidateQueries({ queryKey: queryKeys.topology }),
    queryClient.invalidateQueries({ queryKey: ['tickets'] }),
    queryClient.invalidateQueries({ queryKey: queryKeys.matches }),
    queryClient.invalidateQueries({ queryKey: ['match'] }),
    queryClient.invalidateQueries({ queryKey: ['logical-node-facts'] }),
  ])
  queryClient.removeQueries({ queryKey: ['match'] })
  queryClient.removeQueries({ queryKey: ['logical-node-facts'] })
}

export function useStartRound() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: RunRoundInput) => api.startRound(input),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.topology }),
        queryClient.invalidateQueries({ queryKey: queryKeys.matches }),
        queryClient.invalidateQueries({ queryKey: ['tickets'] }),
      ])
    },
  })
}

export function useCreateTicket() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: TicketInput) => api.createTicket(input),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['tickets'] }),
  })
}

export function useCreateBatch() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: BatchGeneratorSpec) => api.createBatch(input),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['tickets'] }),
  })
}

export function useDeleteTicket() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (ticketId: string) => api.deleteTicket(ticketId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['tickets'] }),
  })
}

export function useValidateRule() {
  return useMutation({ mutationFn: (rule: RuleDocument) => api.validateRule(rule) })
}

export function useReplaceScenario() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({
      scenario,
      rule,
    }: {
      scenario: import('../types').Scenario
      rule: RuleDocument
    }) => api.replaceScenario(scenario, rule),
    onSuccess: () => refreshScenarioBoundQueries(queryClient),
  })
}

export function useImportScenario() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (scenario: import('../types').JsonObject) => api.replaceScenarioPayload(scenario),
    onSuccess: () => refreshScenarioBoundQueries(queryClient),
  })
}
