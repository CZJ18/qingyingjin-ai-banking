import { afterEach, expect, it, vi } from 'vitest'
import { createSSRApp, h } from 'vue'
import { createPinia } from 'pinia'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { renderToString } from '@vue/server-renderer'
import Billing from '../src/pages/Billing.vue'
import { useSession } from '../src/lib/store'

afterEach(() => vi.useRealTimers())

async function renderBilling(expense: number) {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-10-19T12:00:00+08:00'))
  const pinia = createPinia(), session = useSession(pinia)
  session.accountId = '7'
  const from = Date.parse('2026-10-01T00:00:00+08:00'), to = Date.parse('2026-11-01T00:00:00+08:00')
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  client.setQueryData(['billing-analysis', from, to, '7'], {
    total_income: 0, total_expense: expense, accounts: [], transactions: [],
    category_breakdown: [{ category: 'dining', expense_minor: expense }],
    budget_status: [], anomalies: [], findings: [], daily: [], weekly: [], monthly: [],
    evidence_transaction_ids: [],
  })
  client.setQueryData(['billing-transactions', from, to, '7', '', '', 0, []], {
    items: [
      { id: '1', bank_account_id: '7', direction: 'income', kind: 'opening', amount_minor: 1000000, occurred_at: from + 1 },
      { id: '2', bank_account_id: '7', direction: 'expense', kind: 'transfer_out', amount_minor: 10000, occurred_at: from + 2 },
      { id: '3', bank_account_id: '7', direction: 'expense', kind: 'external', amount_minor: 3900, occurred_at: from + 3 },
    ], total: 3, limit: 10, offset: 0,
  })
  const app = createSSRApp(Billing)
  app.use(pinia).use(VueQueryPlugin, { queryClient: client })
  app.component('RouterLink', { setup(_, { slots }) { return () => h('a', slots.default?.()) } })
  try { return await renderToString(app) } finally { client.clear() }
}

it('compares consumption totals only with external transactions, excluding opening and transfers', async () => {
  const html = await renderBilling(3900)
  expect(html).not.toContain('当前分页收入超过周期汇总')
  expect(html).not.toContain('当前分页支出超过周期汇总')
})

it('still detects external spending that exceeds the consumption report', async () => {
  expect(await renderBilling(3800)).toContain('当前分页支出超过周期汇总')
})
