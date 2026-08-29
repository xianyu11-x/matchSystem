import { useQuery } from '@tanstack/react-query'
import { api, isDemoMode } from './api'

export function useHealth() {
  return useQuery({
    queryKey: ['health'],
    queryFn: api.getHealth,
    refetchInterval: 15_000,
    retry: 0,
    meta: { isDemoMode },
  })
}
