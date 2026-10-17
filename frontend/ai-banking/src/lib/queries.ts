import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { api } from '@/api/client'
import { useSession } from './store'
import { renewalApi } from '@/api/renewals'
export function useRenewals() {
  const s = useSession()
  return useQuery({
    queryKey: computed(() => ['renewal-notices', s.sessionEpoch]),
    queryFn: ({ signal }) => renewalApi.list(signal),
    enabled: computed(() => s.connected),
    refetchInterval: 30000,
  })
}
export function useAccounts() {
  const s = useSession()
  return useQuery({
    queryKey: computed(() => ['accounts', s.sessionEpoch]),
    queryFn: ({ signal }) => api.accounts(signal),
    enabled: computed(() => s.connected),
  })
}
export function useSavings() {
  const s = useSession()
  return useQuery({
    queryKey: computed(() => ['savings', s.sessionEpoch]),
    queryFn: ({ signal }) => api.savings(signal),
    enabled: computed(() => s.connected),
  })
}
export function useOperations() {
  const s = useSession()
  return useQuery({
    queryKey: computed(() => ['operations', s.sessionEpoch]),
    queryFn: ({ signal }) => api.operations(signal),
    enabled: computed(() => s.connected),
    refetchInterval: 10000,
  })
}
export function period(month: string) {
  const [year, m] = month.split('-').map(Number)
  return {
    from: new Date(Date.UTC(year!, m! - 1, 1, -8)).toISOString(),
    to: new Date(Date.UTC(year!, m!, 1, -8)).toISOString(),
    timezone: 'Asia/Shanghai' as const,
  }
}
export function currentMonth() {
  return new Intl.DateTimeFormat('sv-SE', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
  }).format(new Date())
}
