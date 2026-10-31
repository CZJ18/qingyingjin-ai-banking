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
  if (!/^[1-9]\d{3}-(0[1-9]|1[0-2])$/.test(month))
    throw new RangeError('请选择有效的分析月份（YYYY-MM）')
  const [year, m] = month.split('-').map(Number)
  return {
    from: new Date(Date.UTC(year!, m! - 1, 1, -8)).toISOString(),
    to: new Date(Date.UTC(year!, m!, 1, -8)).toISOString(),
    timezone: 'Asia/Shanghai' as const,
  }
}
// A historical task's persisted interval is independent of the new composer.
export function analysisPeriodLabel(bindings?: Record<string, unknown>) {
  const from = typeof bindings?.from === 'string' ? bindings.from : ''
  const to = typeof bindings?.to === 'string' ? bindings.to : ''
  const start = Date.parse(from), end = Date.parse(to)
  if (!Number.isFinite(start) || !Number.isFinite(end) || start >= end)
    return '未提供分析区间'
  const month = new Intl.DateTimeFormat('sv-SE', {
    timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit',
  }).format(new Date(start))
  if (/^[1-9]\d{3}-(0[1-9]|1[0-2])$/.test(month)) {
    const bounds = period(month)
    if (start === Date.parse(bounds.from) && end === Date.parse(bounds.to)) return month
  }
  const format = new Intl.DateTimeFormat('sv-SE', {
    timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  })
  return `${format.format(new Date(start))} 至 ${format.format(new Date(end))}（结束时间不含）`
}
export function currentMonth() {
  return new Intl.DateTimeFormat('sv-SE', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
  }).format(new Date())
}
// FinancialPlan.Settings.period is an existing UTC-month contract in Go.
// Billing remains explicitly Asia/Shanghai; do not send its local month here.
export function planningMonth() {
  return new Date().toISOString().slice(0, 7)
}
