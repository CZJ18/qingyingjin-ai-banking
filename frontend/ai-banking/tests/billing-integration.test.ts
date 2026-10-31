import { afterEach, expect, it, vi } from 'vitest'
import * as Vue from 'vue'
import { createRenderer, createSSRApp, defineComponent, h, nextTick, ssrContextKey } from 'vue'
import { readFileSync } from 'node:fs'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { transformSync } from 'esbuild'
import { createPinia } from 'pinia'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { renderToString } from '@vue/server-renderer'
import { createRouter, createMemoryHistory } from 'vue-router'
import Billing from '../src/pages/Billing.vue'
import State from '../src/components/State.vue'
import JsonDetails from '../src/components/JsonDetails.vue'
import { useSession } from '../src/lib/store'

const network = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../src/api/client', async importOriginal => ({
  ...await importOriginal<object>(), request: network.request,
}))
vi.mock('../src/components/billing/StatementImport.vue', () => ({ default: defineComponent({
  emits: ['view-month'],
  setup(_, { emit }) { return () => h('button', { onClick: () => emit('view-month', '2026-10') }, '查看2026-10月账单') },
}) }))
vi.mock('../src/components/ui/button/Button.vue', () => ({ default: defineComponent({
  setup(_, { attrs, slots }) { return () => h('button', attrs, slots.default?.()) },
}) }))
vi.mock('reka-ui', () => Object.fromEntries([
  'DialogRoot', 'DialogPortal', 'DialogOverlay', 'DialogContent', 'DialogTitle',
  'DialogDescription', 'DialogClose',
].map(name => [name, defineComponent({ setup(_, { slots }) { return () => h('div', slots.default?.()) } })])))
vi.mock('../src/components/billing/ExpenseTrends.vue', () => ({ default: defineComponent({ setup() { return () => h('div') } }) }))
vi.mock('../src/components/billing/CategoryBreakdown.vue', () => ({ default: defineComponent({ setup() { return () => h('div') } }) }))

const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); vi.unstubAllGlobals(); vi.useRealTimers() })

async function renderBilling(expense: number) {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-10-19T12:00:00+08:00'))
  const pinia = createPinia(), session = useSession(pinia)
  session.connected = true
  session.accountId = '7'
  const from = Date.parse('2026-10-01T00:00:00+08:00'), to = Date.parse('2026-11-01T00:00:00+08:00')
  // These SSR assertions use only the seeded synthetic snapshot, without a
  // background refresh racing it through an unrelated request mock.
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  client.setQueryData(['billing-analysis', from, to, '7'], {
    total_income: 0, total_expense: expense, accounts: [], transactions: [],
    category_breakdown: [{ category: 'dining', expense_minor: expense }],
    budget_status: [], anomalies: [], findings: [], daily: [], weekly: [], monthly: [],
    evidence_transaction_ids: [],
  })
  client.setQueryData(['billing-transactions', session.sessionEpoch, 'live', from, to, '7', '', '', 0, []], {
    items: [
      { id: '1', bank_account_id: '7', direction: 'income', kind: 'opening', amount_minor: 1000000, occurred_at: from + 1 },
      { id: '2', bank_account_id: '7', direction: 'expense', kind: 'transfer_out', amount_minor: 10000, occurred_at: from + 2 },
      { id: '3', bank_account_id: '7', direction: 'expense', kind: 'external', amount_minor: 3900, occurred_at: from + 3 },
    ], total: 3, limit: 10, offset: 0,
  })
  const app = createSSRApp(Billing)
  const router=createRouter({history:createMemoryHistory(),routes:[{path:'/billing',component:Billing}]})
  await router.push('/billing');await router.isReady()
  app.use(router)
  app.use(pinia).use(VueQueryPlugin, { queryClient: client })
  try { return await renderToString(app) } finally { client.clear() }
}

it('compares consumption totals only with external transactions, excluding opening and transfers', async () => {
  const html = await renderBilling(3900)
  expect(html).toContain('共 3 笔')
  expect(html).not.toContain('当前分页收入超过周期汇总')
  expect(html).not.toContain('当前分页支出超过周期汇总')
})

it('still detects external spending that exceeds the consumption report', async () => {
  expect(await renderBilling(3800)).toContain('当前分页支出超过周期汇总')
})

it('labels user-uploaded bills as unverified alongside the simulated account context', async () => {
  const html = await renderBilling(3900)
  expect(html).toContain('模拟银行环境 · 非真实资金')
  expect(html).toContain('账单分析可使用本人上传记录，未经银行核验；模拟账户余额不代表真实银行余额。')
})

it('shows the full imported month after clearing same-month dates and existing transaction filters', async () => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date('2026-10-27T12:00:00+08:00'))
  vi.stubGlobal('Document', class {})
  vi.stubGlobal('ShadowRoot', class {})
  vi.stubGlobal('fetch', vi.fn(() => { throw new Error('nonpaid synthetic test: network forbidden') }))
  network.request.mockReset()
  network.request.mockImplementation(async (path: string) => {
    if (path.endsWith('/billing/generate_billing_report')) return {
      total_income: 0, total_expense: 0, accounts: [], transactions: [],
      category_breakdown: [], budget_status: [], anomalies: [], findings: [],
      daily: [], weekly: [], monthly: [], evidence_transaction_ids: [],
    }
    if (path.startsWith('/api/v1/transactions?')) return { items: [], total: 0, limit: 10, offset: 0 }
    if (path === '/api/v1/transactions/old-related') return { id: 'old-related', amount_minor: 1 }
    throw new Error('unexpected synthetic request: ' + path)
  })

  // Compile the actual client templates because the nonpaid Node config uses SSR SFCs.
  for (const [component, path] of [[Billing, 'pages/Billing.vue'], [State, 'components/State.vue'], [JsonDetails, 'components/JsonDetails.vue']] as const) {
    const { descriptor } = parse(readFileSync(new URL('../src/' + path, import.meta.url), 'utf8'))
    const bindings = compileScript(descriptor, { id: path }).bindings
    const compiled = compileTemplate({ source: descriptor.template!.content, filename: path, id: path, compilerOptions: { bindingMetadata: bindings } })
    const code = transformSync(compiled.code, { loader: 'ts' }).code.replace(/import \{([^}]+)\} from "vue"/g, (_, imports: string) => 'const {' + imports.replace(/ as /g, ':') + '} = Vue')
      .replace('export function render', 'return function render')
    ;(component as any).render = new Function('Vue', code)(Vue)
  }
  class Element {
    children: Element[] = []; parent: Element | null = null
    props: Record<string, any> = {}; value = ''; text = ''
    constructor(public tag: string) {}
    get options() { return this.children }
    getRootNode() { return this }
    addEventListener() {}
    removeEventListener() {}
  }
  const renderer = createRenderer<Element, Element>({
    createElement: tag => new Element(tag), createText: text => Object.assign(new Element('#text'), { text }),
    createComment: () => new Element('#comment'), setText: (node, text) => { node.text = text },
    setElementText: (node, text) => { node.text = text; node.children = [] },
    parentNode: node => node.parent,
    nextSibling: node => node.parent?.children[node.parent.children.indexOf(node) + 1] || null,
    insert(node, parent, anchor) {
      if (node.parent) node.parent.children.splice(node.parent.children.indexOf(node), 1)
      node.parent = parent
      const index = anchor ? parent.children.indexOf(anchor) : -1
      if (index < 0) parent.children.push(node); else parent.children.splice(index, 0, node)
    },
    remove(node) { node.parent?.children.splice(node.parent.children.indexOf(node), 1); node.parent = null },
    patchProp(node, key, _old, value) { node.props[key] = value; if (key === 'value') node.value = value },
  })
  const all = (node: Element): Element[] => [node, ...node.children.flatMap(all)]
  const text = (node: Element): string => node.text + node.children.map(text).join('')
  const flush = async () => { for (let i = 0; i < 15; i++) { await Promise.resolve(); await nextTick() } }
  const pinia = createPinia(), session = useSession(pinia), root = new Element('root')
  session.connected = true; session.accountId = '7'
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/billing', component: Billing }] })
  await router.push('/billing'); await router.isReady()
  const app = renderer.createApp(Billing)
  app.provide(ssrContextKey, { modules: new Set() })
  app.use(router).use(pinia).use(VueQueryPlugin, { queryClient: client }); app.mount(root)
  cleanups.push(() => { app.unmount(); client.clear() })
  await flush()
  const state = app._instance!.setupState as Record<string, any>
  state.start = '2026-10-10'; state.end = '2026-10-11'
  state.category = 'shopping'; state.search = '旧商户'; state.appliedSearch = '旧商户'
  await flush()
  state.relatedIds = ['old-related']; await flush()
  state.page = 2; await flush()
  network.request.mockClear()

  const monthButton = all(root).find(node => node.tag === 'button' && text(node) === '查看2026-10月账单')
  expect(monthButton).toBeDefined()
  monthButton!.props.onClick(); await flush()

  expect([state.billMode, state.month, state.start, state.end]).toEqual(['month', '2026-10', '2026-10-01', '2026-10-31'])
  expect([state.category, state.search, state.appliedSearch, state.relatedIds, state.page]).toEqual(['', '', '', [], 0])
  const requestPath = network.request.mock.calls.map(([path]) => path).find(path => path.startsWith('/api/v1/transactions?'))
  expect(requestPath).toBeDefined()
  const params = new URL(requestPath, 'https://synthetic.invalid').searchParams
  expect(Object.fromEntries(params)).toEqual({
    bank_account_id: '7', from: String(Date.parse('2026-10-01T00:00:00+08:00')),
    to: String(Date.parse('2026-11-01T00:00:00+08:00')), limit: '10', offset: '0',
  })
})
