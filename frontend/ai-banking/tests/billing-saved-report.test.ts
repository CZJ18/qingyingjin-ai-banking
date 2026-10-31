import { beforeEach, expect, it, vi } from 'vitest'
import { computed, createSSRApp, h, ref } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { request } from '../src/api/client'

const state = vi.hoisted(() => ({
  account: '27', user: '42', query: {} as Record<string, unknown>, source: null as any,
  options: [] as any[],
}))
vi.mock('@/lib/store', () => ({ useSession: () => ({ connected: true, sessionEpoch: 1,
  user: { id: state.user }, accountId: state.account, saveBillingReport: vi.fn() }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: state.query }), useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/components/billing/StatementImport.vue', () => ({ default: { render: () => null } }))
vi.mock('@/api/client', async importOriginal => ({
  ...await importOriginal<typeof import('../src/api/client')>(),
  request: vi.fn(async () => ({})),
}))
vi.mock('@tanstack/vue-query', () => ({ useQuery: (options: any) => {
  state.options.push(options)
  const linked = computed(() => options.queryKey.value[0] === 'linked-billing-report' ? state.source : undefined)
  return { data: linked, isLoading: ref(false), isFetching: ref(false), error: ref(null), refetch: vi.fn() }
} }))
import Billing from '../src/pages/Billing.vue'

const id = 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee'
beforeEach(() => {
  vi.mocked(request).mockClear()
  state.account = '27'; state.user = '42'; state.query = { report_id: id }; state.options = []
  state.source = { report_id: id, user_id: '42', currency: 'CNY', total_income: 500000,
    total_expense: 390000, transaction_count: 61,
    analysis_period: { from: '2026-08-31T16:00:00Z', to: '2026-09-30T16:00:00Z', timezone: 'Asia/Shanghai' },
    accounts: [{ id: '27', display_name: '合成账单账户' }], category_breakdown: [{ category: 'shopping', expense_minor: 390000, transaction_ids: ['1'] }],
    transactions: [], evidence_transaction_ids: ['1'], budget_status: [], daily: [], weekly: [], monthly: [], anomalies: [], findings: [],
  }
})
it('keeps an empty saved category empty instead of querying current transactions', async () => {
  state.query = { report_id: id, category: 'subscriptions' }
  await render()
  const query = state.options.find(o => o.queryKey.value[0] === 'billing-transactions')
  const result = await query.queryFn({signal: new AbortController().signal})
  expect(result).toEqual({items: [], total: 0, limit: 10, offset: 0})
  expect(request).not.toHaveBeenCalled()
})
it('enables ordinary live reports only when the user has no saved-report link', async () => {
  state.query = {}; await render()
  expect(state.options.find(o => o.queryKey.value[0] === 'billing-analysis').enabled.value).toBe(true)
})
const render = async () => {
  const app = createSSRApp(Billing)
  app.component('RouterLink', { setup(_, { slots }) { return () => h('a', slots.default?.()) } })
  return renderToString(app)
}
it('opens an owned saved report without a category and never enables live report generation', async () => {
  const html = await render()
  expect(html).toContain('保存时的账单快照')
  expect(html).toContain('3,900.00'); expect(html).toContain('5,000.00')
  expect(html).not.toContain('请选择试行记录中的具体类别')
  expect(state.options.find(o => o.queryKey.value[0] === 'billing-analysis').enabled.value).toBe(false)
})
it('does not substitute the current month when a report link is malformed', async () => {
  state.query = { report_id: 'bad-id' }; state.source = null
  const html = await render()
  expect(html).toContain('报告链接无效')
  expect(state.options.find(o => o.queryKey.value[0] === 'billing-analysis').enabled.value).toBe(false)
  expect(state.options.find(o => o.queryKey.value[0] === 'billing-transactions').enabled.value).toBe(false)
})
it.each(['account', 'user'] as const)('hides a cached snapshot after the %s binding changes', async key => {
  state[key] = '99'
  const html = await render()
  expect(html).not.toContain('3,900.00')
  expect(html).toContain('不属于当前')
  expect(html).not.toContain('未找到交易记录')
  expect(html).not.toContain('当前账单筛选')
  expect(state.options.find(o => o.queryKey.value[0] === 'billing-transactions').enabled.value).toBe(false)
})
