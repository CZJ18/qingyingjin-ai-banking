import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import * as Vue from 'vue'
import { createRenderer, defineComponent, h, nextTick, ssrContextKey } from 'vue'
import { readFileSync } from 'node:fs'
import { File } from 'node:buffer'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import { transformSync } from 'esbuild'
import { createPinia } from 'pinia'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { createMemoryHistory, createRouter } from 'vue-router'
import StatementImport from '../src/components/billing/StatementImport.vue'
import Billing from '../src/pages/Billing.vue'
import State from '../src/components/State.vue'
import ExecutionDialog from '../src/components/ExecutionDialog.vue'
import Status from '../src/components/Status.vue'
import JsonDetails from '../src/components/JsonDetails.vue'
import { useSession } from '../src/lib/store'

const network = vi.hoisted(() => ({ request: vi.fn(), prepare: vi.fn(), epoch: 0 }))
vi.mock('../src/api/client', async importOriginal => ({
  ...await importOriginal<object>(), request: network.request,
  sessionVersion: () => network.epoch, api: { prepare: network.prepare },
}))
vi.mock('../src/components/ui/button/Button.vue', () => ({ default: defineComponent({
  setup(_, { attrs, slots }) { return () => h('button', attrs, slots.default?.()) },
}) }))
vi.mock('reka-ui', () => Object.fromEntries([
  'DialogRoot', 'DialogPortal', 'DialogOverlay', 'DialogContent', 'DialogTitle',
  'DialogDescription', 'DialogClose',
].map(name => [name, defineComponent({ setup(_, { slots }) { return () => h('div', slots.default?.()) } })])))
vi.mock('../src/components/billing/ExpenseTrends.vue', () => ({ default: defineComponent({ setup() { return () => h('div') } }) }))
vi.mock('../src/components/billing/CategoryBreakdown.vue', () => ({ default: defineComponent({ setup() { return () => h('div') } }) }))

// The nonpaid Node config compiles SFCs for SSR. Reuse their real setup functions
// with client templates so input and server completion events can be exercised.
for (const [component, path] of [[StatementImport, 'components/billing/StatementImport.vue'], [ExecutionDialog, 'components/ExecutionDialog.vue'], [Status, 'components/Status.vue'], [JsonDetails, 'components/JsonDetails.vue'], [Billing, 'pages/Billing.vue'], [State, 'components/State.vue']] as const) {
  const { descriptor } = parse(readFileSync(new URL('../src/' + path, import.meta.url), 'utf8'))
  const bindings = compileScript(descriptor, { id: path }).bindings
  const compiled = compileTemplate({ source: descriptor.template!.content, filename: path, id: path, compilerOptions: { bindingMetadata: bindings } })
  const code = transformSync(compiled.code, { loader: 'ts' }).code.replace(/import \{([^}]+)\} from "vue"/g, (_, imports: string) => 'const {' + imports.replace(/ as /g, ':') + '} = Vue')
    .replace('export function render', 'return function render')
  ;(component as any).render = new Function('Vue', code)(Vue)
}

// An in-memory renderer exercises component events without a browser or network.
class Element {
  children: Element[] = []
  parent: Element | null = null
  props: Record<string, any> = {}
  listeners: Record<string, (event: any) => void> = {}
  value = ''; text = ''; checked = false; selectedIndex = -1
  constructor(public tag: string) {}
  get options() { return this.children }
  getRootNode() { return this }
  addEventListener(name: string, fn: (event: any) => void) { this.listeners[name] = fn }
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
const text = (node: Element): string => node.text + node.children.map(text).join('')
const nodes = (root: Element): Element[] => [root, ...root.children.flatMap(nodes)]
const flush = async () => { for (let i = 0; i < 15; i++) { await Promise.resolve(); await nextTick() } }
const csv = 'id,occurred_at,amount,direction,currency,status\nsynthetic-1,2026-10-20,15.00,expense,CNY,posted'
const utf8Csv = '交易编号,交易时间,金额,收支方向,币种,入账状态,商户名称\nsynthetic-utf8,2026-10-20 12:00:00,15.00,支出,人民币,已入账,合成商户'
const csvFile = (content: string | Uint8Array) => new File([content], 'synthetic.csv', { type: 'text/csv' })
function delayedFile(content: string | Uint8Array) {
  const file = csvFile(content), readText = file.text.bind(file), readBytes = file.arrayBuffer.bind(file)
  let finish!: () => void
  const gate = new Promise<void>(resolve => { finish = resolve })
  Object.defineProperties(file, {
    text: { value: async () => { await gate; return readText() } },
    arrayBuffer: { value: async () => { await gate; return readBytes() } },
  })
  return { file, finish }
}
const selectFile = (root: Element, file: File): Promise<void> =>
  nodes(root).find(node => node.tag === 'input' && node.props.type === 'file')!.props.onChange({ target: { files: [file] } })
const source = { kind: 'unverified_csv_upload', timezone: 'Asia/Shanghai', columns: {}, file_sha256: 'a'.repeat(64), row_sha256: 'b'.repeat(64), row_number: 2, raw_record: { id: 'synthetic-1' } }
const record = { external_transaction_id: 'synthetic-1', direction: 'expense', amount_minor: 1500, currency: 'CNY', merchant_name: '合成商户', category: 'other', occurred_at: 1792425600000, effect_mode: 'record_only', source }
let parameters: Record<string, any>, resultParameters: Record<string, any> | undefined
let previewRecords: Record<string, any>[]
let finalStatus: string, lostResponse: boolean, reconcile: Promise<any> | undefined
const view = (status: string, params = parameters) => ({ operation: {
  id: 'synthetic-operation', kind: 'import_transaction', parameters: params, status,
  expires_at: Date.now() + 60000, snapshot: {}, amount_minor: 1500,
  intent_digest: 'synthetic-intent', confirmation_nonce: 'synthetic-nonce', version: 1,
}, risk_events: [] })
const cleanups: (() => void)[] = []
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] }); vi.setSystemTime(new Date('2026-10-27T12:00:00+08:00'))
  network.epoch = 0; network.request.mockReset(); network.prepare.mockReset()
  resultParameters = undefined; finalStatus = 'succeeded'; lostResponse = false; reconcile = undefined
  previewRecords = [structuredClone(record), { ...structuredClone(record), external_transaction_id: 'synthetic-2', source: { ...source, row_sha256: 'c'.repeat(64), row_number: 3 } }]
  vi.stubGlobal('Document', class {}); vi.stubGlobal('ShadowRoot', class {})
  vi.stubGlobal('fetch', vi.fn(() => { throw new Error('nonpaid synthetic test: network forbidden') }))
  network.prepare.mockImplementation(async (input: any) => { parameters = input.parameters; return view('pending_authorization') })
  network.request.mockImplementation(async (path: string, options?: any) => {
    if (path.endsWith('/import/preview')) return { file_sha256: source.file_sha256, records: previewRecords, skipped: previewRecords.length ? 0 : 2, warnings: [], financial_execution_enabled: false }
    if (path.endsWith('/billing/generate_billing_report')) {
      const expense = options.body.from === '2026-08-31T16:00:00.000Z' ? 1500 : 0
      return { total_income: 0, total_expense: expense, accounts: [], transactions: [], category_breakdown: expense ? [{ category: 'other', expense_minor: expense }] : [], budget_status: [], anomalies: [], findings: [], daily: [], weekly: [], monthly: [], evidence_transaction_ids: [] }
    }
    if (path.startsWith('/api/v1/transactions?')) return { items: [], total: 0, limit: 10, offset: 0 }
    if (path.endsWith('/readiness')) return { ready: true }
    if (path.endsWith('/authorize')) return view('authorized')
    if (path.endsWith('/execute')) { if (lostResponse) throw new Error('synthetic response lost'); return view(finalStatus, resultParameters || parameters) }
    if (path.endsWith('/synthetic-operation')) return reconcile || view(finalStatus, resultParameters || parameters)
    throw new Error('unexpected synthetic request: ' + path)
  })
})
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.unstubAllGlobals(); vi.useRealTimers() })

async function mountImport(billing = false) {
  const pinia = createPinia(), session = useSession(pinia), root = new Element('root')
  session.connected = true; session.accountId = '7'
  const query = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const viewedMonth = vi.fn()
  const app = billing ? renderer.createApp(Billing) : renderer.createApp(StatementImport, { initialOpen: true, onViewMonth: viewedMonth })
  if (billing) {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/billing', component: Billing }] })
    await router.push('/billing?import=1'); await router.isReady(); app.use(router)
  }
  app.provide(ssrContextKey, { modules: new Set() })
  app.use(pinia).use(VueQueryPlugin, { queryClient: query }); app.mount(root)
  cleanups.push(() => { app.unmount(); query.clear() })
  const click = async (label: string) => {
    const button = nodes(root).find(node => node.tag === 'button' && text(node) === label)
    expect(button, `button ${label}`).toBeDefined()
    button!.props.onClick(); await flush()
  }
  const textarea = nodes(root).find(node => node.tag === 'textarea')!
  textarea.value = csv; textarea.listeners.input!({ target: textarea }); await flush()
  await click('预览历史账单')
  return { root, session, click, viewedMonth }
}
async function execute(click: (label: string) => Promise<void>, root: Element) {
  await click('核对并授权补录')
  const checkbox = nodes(root).find(node => node.tag === 'input' && node.props.type === 'checkbox' && text(node.parent!).includes('我已核对'))!
  checkbox.checked = true; checkbox.listeners.change!({ target: checkbox }); await flush()
  await click('授权并执行')
}

it('marks only a server-confirmed imported row complete and prevents another preparation', async () => {
  const { root, click } = await mountImport()
  await execute(click, root)
  const done = nodes(root).find(node => node.tag === 'button' && text(node) === '已补录')
  expect(done).toBeDefined(); expect(done!.props.disabled).toBe(true)
  done!.props.onClick(); await flush()
  expect(network.prepare).toHaveBeenCalledTimes(1)
  expect(nodes(root).filter(node => node.tag === 'button' && text(node) === '核对并授权补录')).toHaveLength(1)
  expect(parameters).toMatchObject({ bank_account_id: '7', effect_mode: 'record_only', source })
})

it('updates a completed row after recovering a lost execution response from the server', async () => {
  lostResponse = true
  const { root, click } = await mountImport()
  await execute(click, root)
  expect(nodes(root).find(node => node.tag === 'button' && text(node) === '已补录')?.props.disabled).toBe(true)
})

it.each(['failed', 'rejected', 'authorized'])('keeps %s outcomes available for explicit review', async status => {
  finalStatus = status
  const { root, click } = await mountImport()
  await execute(click, root)
  expect(nodes(root).filter(node => node.tag === 'button' && text(node) === '核对并授权补录')).toHaveLength(2)
  expect(text(root)).not.toContain('已补录')
  expect(text(root)).not.toContain('月账单')
})

it.each(['account', 'external-id', 'file', 'row', 'effect'])('does not mark a row complete for a different %s', async mismatch => {
  const { root, click } = await mountImport()
  resultParameters = { ...record, bank_account_id: '7', source: { ...source } }
  if (mismatch === 'account') resultParameters.bank_account_id = '8'
  if (mismatch === 'external-id') resultParameters.external_transaction_id = 'synthetic-other'
  if (mismatch === 'file') resultParameters.source.file_sha256 = 'd'.repeat(64)
  if (mismatch === 'row') resultParameters.source.row_sha256 = 'd'.repeat(64)
  if (mismatch === 'effect') resultParameters.effect_mode = 'apply_simulated_effect'
  await execute(click, root)
  expect(text(root)).not.toContain('已补录')
  expect(text(root)).not.toContain('月账单')
})

it('does not apply a late completion after the session changes', async () => {
  lostResponse = true
  let finish!: (result: any) => void
  reconcile = new Promise(resolve => { finish = resolve })
  const { root, click, session } = await mountImport()
  await execute(click, root)
  session.sessionEpoch++; network.epoch++
  finish(view('succeeded')); await flush()
  expect(text(root)).not.toContain('已补录')
  expect(nodes(root).find(node => node.tag === 'textarea')?.value).toBe('')
})

it('describes uploaded records as unverified read-only analysis in the authorization dialog', async () => {
  const { root, click } = await mountImport()
  await click('核对并授权补录')
  expect(text(root)).toContain('补录上传账单（只读分析）')
  expect(text(root)).toContain('用户上传记录未经银行核验')
  expect(text(root)).toContain('不改变账户余额')
  expect(text(root)).not.toContain('导入模拟交易')
})

it('explains the supported export format and a separate zero-balance analysis account', async () => {
  const { root } = await mountImport()
  const content = text(root)
  expect(content).toContain('本人从银行或支付平台导出的 CSV 账单')
  expect(content).toContain('上传与预览本身不调用模型')
  expect(content).toContain('专用的零余额模拟账户')
  expect(content).toContain('避免演示流水混入')
  expect(content).toContain('收入 / 支出')
  expect(content).toContain('已入账 / 待入账 / 已冲正 / 失败')
  expect(content).toContain('CNY / 人民币')
  expect(content).toContain('RFC3339 或 YYYY-MM-DD HH:mm:ss')
  expect(content).toContain('不接受千分位或货币符号')
})

it.each([
  ['2026-08-31T16:30:00Z', '2026-09'],
  ['2026-09-30T16:30:00Z', '2026-10'],
])('offers the Beijing month for successful normalized time %s without guessing the raw CSV', async (occurredAt, month) => {
  previewRecords[0]!.occurred_at = Date.parse(occurredAt)
  const { root, click, viewedMonth } = await mountImport()
  expect(text(root)).not.toContain('月账单')
  await execute(click, root)
  await click(`查看${month}月账单`)
  expect(viewedMonth).toHaveBeenCalledExactlyOnceWith(month)
  expect(network.prepare).toHaveBeenCalledTimes(1)
})

it('keeps a completed month available after re-preview returns no new rows', async () => {
  previewRecords[0]!.occurred_at = Date.parse('2026-09-15T12:00:00+08:00')
  const { root, click, viewedMonth } = await mountImport()
  await execute(click, root)
  previewRecords = []
  await click('预览历史账单')
  expect(text(root)).toContain('可导入 0 笔')
  await click('查看2026-09月账单')
  expect(viewedMonth).toHaveBeenCalledExactlyOnceWith('2026-09')
})

it.each(['account', 'session', 'csv', 'file'])('clears completed month navigation when the %s changes', async change => {
  previewRecords[0]!.occurred_at = Date.parse('2026-09-15T12:00:00+08:00')
  const { root, click, session } = await mountImport()
  await execute(click, root)
  expect(text(root)).toContain('查看2026-09月账单')
  if (change === 'account') session.accountId = '8'
  if (change === 'session') { session.sessionEpoch++; network.epoch++ }
  if (change === 'csv') {
    const textarea = nodes(root).find(node => node.tag === 'textarea')!
    textarea.value = csv + '\nsynthetic-other'; textarea.listeners.input!({ target: textarea })
  }
  if (change === 'file') {
    await selectFile(root, csvFile(csv))
  }
  await flush()
  expect(text(root)).not.toContain('查看2026-09月账单')
})

it('does not restore a previous component instance completed months after refresh', async () => {
  previewRecords[0]!.occurred_at = Date.parse('2026-09-15T12:00:00+08:00')
  const previous = await mountImport()
  await execute(previous.click, previous.root)
  expect(text(previous.root)).toContain('查看2026-09月账单')
  const refreshed = await mountImport()
  expect(text(refreshed.root)).not.toContain('查看2026-09月账单')
})

it('invalidates the previous import immediately while a newly selected file is still reading', async () => {
  previewRecords[0]!.occurred_at = Date.parse('2026-09-15T12:00:00+08:00')
  const { root, click } = await mountImport()
  await execute(click, root)
  expect(text(root)).toContain('查看2026-09月账单')
  let finishExecution!: (result: any) => void
  lostResponse = true
  reconcile = new Promise(resolve => { finishExecution = resolve })
  await execute(click, root)
  const delayed = delayedFile(csv)
  const reading = selectFile(root, delayed.file)
  await flush()
  expect(text(root)).not.toContain('月账单')
  finishExecution(view('succeeded')); await flush()
  expect(text(root)).not.toContain('月账单')
  delayed.finish(); await reading; await flush()
  expect(text(root)).not.toContain('月账单')
})

it.each([false, true])('reads real UTF-8 Chinese CSV bytes with BOM=%s without changing their text', async bom => {
  const { root, click } = await mountImport()
  network.request.mockClear()
  const bytes = new TextEncoder().encode((bom ? '\uFEFF' : '') + utf8Csv)
  await selectFile(root, csvFile(bytes)); await flush()
  expect(nodes(root).find(node => node.tag === 'textarea')?.value).toBe(utf8Csv)
  expect(network.request).not.toHaveBeenCalled()
  await click('预览历史账单')
  expect(network.request).toHaveBeenCalledExactlyOnceWith('/api/v1/transactions/import/preview', expect.objectContaining({
    body: expect.objectContaining({ account_id: '7', csv: utf8Csv }),
  }))
})

it.each([
  ['invalid leading byte', [0xFF]],
  ['lone continuation byte', [0x80]],
  ['truncated Chinese sequence', [0xE4, 0xB8]],
  ['overlong sequence', [0xC0, 0xAF]],
  ['encoded surrogate', [0xED, 0xA0, 0x80]],
])('rejects real CSV bytes containing %s before preview or authorization', async (_name, invalidBytes) => {
  const { root, click } = await mountImport()
  network.request.mockClear()
  const bytes = new Uint8Array([...new TextEncoder().encode(utf8Csv), ...invalidBytes])
  await selectFile(root, csvFile(bytes)); await flush()
  expect(text(root)).toContain('请在本机另存为 UTF-8 后重试')
  expect(nodes(root).find(node => node.tag === 'textarea')?.value).toBe('')
  expect(text(root)).not.toContain('可导入 2 笔')
  expect(nodes(root).find(node => node.tag === 'button' && text(node) === '预览历史账单')?.props.disabled).toBe(true)
  await click('预览历史账单')
  expect(network.request).not.toHaveBeenCalled()
  expect(network.prepare).not.toHaveBeenCalled()
})

it.each([
  ['account', false], ['session', false], ['account', true], ['session', true],
] as const)('ignores a late file read after %s changes, invalid UTF-8=%s', async (change, invalid) => {
  const { root, session } = await mountImport()
  network.request.mockClear()
  const delayed = delayedFile(invalid ? new Uint8Array([0xFF]) : utf8Csv)
  const reading = selectFile(root, delayed.file)
  await flush()
  if (change === 'account') session.accountId = '8'
  else { session.sessionEpoch++; network.epoch++ }
  await flush()
  const retained = nodes(root).find(node => node.tag === 'textarea')?.value
  delayed.finish(); await reading; await flush()
  expect(nodes(root).find(node => node.tag === 'textarea')?.value).toBe(retained)
  expect(text(root)).not.toContain('请在本机另存为 UTF-8 后重试')
  expect(network.request).not.toHaveBeenCalled()
  expect(network.prepare).not.toHaveBeenCalled()
})

it.each([false, true])('ignores an earlier file read after selecting another file, invalid UTF-8=%s', async invalid => {
  const { root } = await mountImport()
  network.request.mockClear()
  const delayed = delayedFile(invalid ? new Uint8Array([0xFF]) : csv)
  const reading = selectFile(root, delayed.file)
  await flush()
  await selectFile(root, csvFile(utf8Csv)); await flush()
  delayed.finish(); await reading; await flush()
  expect(nodes(root).find(node => node.tag === 'textarea')?.value).toBe(utf8Csv)
  expect(text(root)).not.toContain('请在本机另存为 UTF-8 后重试')
  expect(network.request).not.toHaveBeenCalled()
})

it('accepts a real UTF-8 file at exactly 256 KiB', async () => {
  const { root } = await mountImport()
  const content = utf8Csv + ' '.repeat(262144 - new TextEncoder().encode(utf8Csv).length)
  const file = csvFile(content)
  expect(file.size).toBe(262144)
  await selectFile(root, file); await flush()
  expect(nodes(root).find(node => node.tag === 'textarea')?.value).toBe(content)
  expect(text(root)).not.toContain('CSV 不能超过 256 KiB')
})

it('rejects a real CSV over 256 KiB before reading any bytes', async () => {
  const { root } = await mountImport()
  network.request.mockClear()
  const file = csvFile(new Uint8Array(262145))
  const readBytes = vi.spyOn(file, 'arrayBuffer'), readText = vi.spyOn(file, 'text')
  await selectFile(root, file); await flush()
  expect(text(root)).toContain('CSV 不能超过 256 KiB')
  expect(nodes(root).find(node => node.tag === 'textarea')?.value).toBe('')
  expect(readBytes).not.toHaveBeenCalled()
  expect(readText).not.toHaveBeenCalled()
  expect(network.request).not.toHaveBeenCalled()
})

it('switches the billing page from annual view to the imported month only when the user clicks', async () => {
  previewRecords[0]!.occurred_at = Date.parse('2026-09-15T12:00:00+08:00')
  const { root, click, session } = await mountImport(true)
  await execute(click, root)
  expect(nodes(root).find(node => node.props['aria-label'] === '账单月份')?.value).toBe('2026-10')
  const mode = nodes(root).find(node => node.props['aria-label'] === '报表周期')!
  mode.props['onUpdate:modelValue']('year'); await flush()
  expect(nodes(root).find(node => node.props['aria-label'] === '账单年份')).toBeDefined()
  await click('查看2026-09月账单')
  expect(nodes(root).find(node => node.props['aria-label'] === '账单年份')).toBeUndefined()
  expect(nodes(root).find(node => node.props['aria-label'] === '账单月份')?.value).toBe('2026-09')
  expect(nodes(root).find(node => node.props['aria-label'] === '开始日期')?.value).toBe('2026-09-01')
  expect(nodes(root).find(node => node.props['aria-label'] === '结束日期')?.value).toBe('2026-09-30')
  expect(session.accountId).toBe('7')
  expect(network.prepare).toHaveBeenCalledTimes(1)
  expect(network.request.mock.calls.filter(([path]) => path.endsWith('/execute'))).toHaveLength(1)
  expect(network.request).toHaveBeenCalledWith('/api/v1/billing/generate_billing_report', expect.objectContaining({ body: expect.objectContaining({ from: '2026-08-31T16:00:00.000Z', to: '2026-09-30T16:00:00.000Z', account_id: '7' }) }))
})

// A direct mount keeps the execution dialog alive when the enclosing page session changes.
async function mountSessionDialog() {
  const root=new Element('root'),pinia=createPinia(),session=useSession(pinia)
  session.connected=true;session.accountId='7';session.user={id:'99'} as any
  const query=new QueryClient({defaultOptions:{queries:{retry:false}}})
  const app=renderer.createApp(ExecutionDialog)
  app.provide(ssrContextKey,{modules:new Set()});app.use(pinia).use(VueQueryPlugin,{queryClient:query})
  const dialog=app.mount(root) as unknown as {prepare:(kind:string,parameters:Record<string,unknown>,expiresInSeconds?:number,idempotencyKey?:string)=>Promise<void>;inspect:(id:string)=>Promise<void>}
  cleanups.push(()=>{app.unmount();query.clear()})
  const prepared={...structuredClone(record),bank_account_id:'7'}
  const click=async(label:string)=>{const button=nodes(root).find(node=>node.tag==='button'&&text(node)===label);expect(button).toBeDefined();await button!.props.onClick();await flush()}
  const acknowledge=async()=>{const checkbox=nodes(root).find(node=>node.tag==='input'&&node.props.type==='checkbox')!;expect(checkbox).toBeDefined();checkbox.checked=true;checkbox.listeners.change!({target:checkbox});await flush()}
  return {root,session,dialog,prepared,click,acknowledge}
}
it.each(['disconnect','epoch','user'] as const)('execution dialog session clears private view and stale authorization on %s',async change=>{
  const {root,session,dialog,prepared,acknowledge}=await mountSessionDialog()
  await dialog.prepare('import_transaction',prepared);await flush();await acknowledge()
  expect(text(root)).toContain('synthetic-1')
  const staleExecute=nodes(root).find(node=>node.tag==='button'&&text(node)==='授权并执行')!
  expect(staleExecute.props.disabled).toBe(false)
  if(change==='disconnect')session.connected=false
  if(change==='epoch'){session.sessionEpoch++;network.epoch++}
  if(change==='user')session.user={id:'100'} as any
  await flush()
  expect(text(root)).not.toContain('synthetic-1')
  expect(text(root)).not.toContain('¥15.00')
  expect(nodes(root).some(node=>node.tag==='button'&&text(node)==='授权并执行')).toBe(false)
  expect(nodes(root).some(node=>node.props.open===true)).toBe(false)
  const before=network.request.mock.calls.length
  await staleExecute.props.onClick();await flush()
  expect(network.request.mock.calls.length).toBe(before)
})
it('execution dialog session retains a preparation retry key only within the same session',async()=>{
  const {session,dialog,prepared}=await mountSessionDialog()
  network.prepare.mockRejectedValueOnce(new Error('synthetic response unknown')).mockRejectedValueOnce(new Error('synthetic response unknown'))
  await dialog.prepare('import_transaction',prepared);await dialog.prepare('import_transaction',prepared)
  expect(network.prepare.mock.calls[0]![1]).toBe(network.prepare.mock.calls[1]![1])
  session.sessionEpoch++;network.epoch++;await flush()
  await dialog.prepare('import_transaction',prepared)
  expect(network.prepare.mock.calls[2]![1]).not.toBe(network.prepare.mock.calls[1]![1])
})
it('execution dialog session requires fresh acknowledgement and execution key after reauthentication',async()=>{
  const {root,session,dialog,prepared,click,acknowledge}=await mountSessionDialog()
  finalStatus='failed'
  await dialog.prepare('import_transaction',prepared);await flush();await acknowledge();await click('授权并执行')
  const first=network.request.mock.calls.find(([path])=>path.endsWith('/execute'))![1].key
  session.sessionEpoch++;network.epoch++;await flush()
  await dialog.prepare('import_transaction',prepared);await flush()
  expect(nodes(root).find(node=>node.tag==='button'&&text(node)==='授权并执行')?.props.disabled).toBe(true)
  await acknowledge();await click('授权并执行')
  const calls=network.request.mock.calls.filter(([path])=>path.endsWith('/execute'))
  expect(calls).toHaveLength(2);expect(calls[1]![1].key).not.toBe(first)
})
it('execution dialog session ignores old asynchronous preparation and does not unlock a new session request',async()=>{
  const {root,session,dialog,prepared}=await mountSessionDialog()
  let oldDone!:(v:any)=>void,newDone!:(v:any)=>void
  network.prepare.mockImplementationOnce(()=>new Promise(resolve=>{oldDone=resolve})).mockImplementationOnce(()=>new Promise(resolve=>{newDone=resolve}))
  const old=dialog.prepare('import_transaction',{...prepared,external_transaction_id:'synthetic-old-session'});await flush()
  session.sessionEpoch++;network.epoch++;await flush()
  const current=dialog.prepare('import_transaction',{...prepared,external_transaction_id:'synthetic-new-session'});await flush()
  expect(network.prepare).toHaveBeenCalledTimes(2)
  oldDone(view('pending_authorization',{...prepared,external_transaction_id:'synthetic-old-session'}));await old;await flush()
  expect(nodes(root).some(node=>node.props['aria-busy']==='true'||node.props['aria-busy']===true)).toBe(true)
  expect(text(root)).not.toContain('synthetic-old-session')
  newDone(view('pending_authorization',{...prepared,external_transaction_id:'synthetic-new-session'}));await current;await flush()
  expect(text(root)).toContain('synthetic-new-session')
  expect(text(root)).not.toContain('synthetic-old-session')
})

it('execution proposal key remains stable across two successful preparations of the same proposal',async()=>{
  const {dialog,prepared}=await mountSessionDialog(),key='agent-op-'+'a'.repeat(64)
  await dialog.prepare('import_transaction',prepared,undefined,key)
  await dialog.prepare('import_transaction',prepared,undefined,key)
  expect(network.prepare).toHaveBeenCalledTimes(2)
  expect(network.prepare.mock.calls.map(call=>call[1])).toEqual([key,key])
  expect(network.request.mock.calls.filter(([path])=>path.endsWith('/authorize')||path.endsWith('/execute'))).toHaveLength(0)
})
it.each(['', 'agent-op-'+'a'.repeat(63), 'agent-op-'+'A'.repeat(64), 'agent-op-'+'a'.repeat(65), 'arbitrary-user-key'])('execution proposal key rejects invalid source key %s before readiness or preparation',async key=>{
  const {root,dialog,prepared}=await mountSessionDialog()
  await dialog.prepare('import_transaction',prepared,undefined,key);await flush()
  expect(network.request).not.toHaveBeenCalled();expect(network.prepare).not.toHaveBeenCalled()
  expect(text(root)).toContain('操作建议幂等标识无效')
})
it('execution proposal key preserves distinct source proposals without silently choosing a random key',async()=>{
  const {dialog,prepared}=await mountSessionDialog(),first='agent-op-'+'a'.repeat(64),second='agent-op-'+'b'.repeat(64)
  await dialog.prepare('import_transaction',prepared,undefined,first)
  await dialog.prepare('import_transaction',prepared,undefined,second)
  expect(network.prepare.mock.calls.map(call=>call[1])).toEqual([first,second])
})
it('execution proposal key retries an unknown preparation response using the original explicit key',async()=>{
  const {dialog,prepared}=await mountSessionDialog(),key='agent-op-'+'c'.repeat(64)
  network.prepare.mockRejectedValueOnce(new Error('synthetic preparation response unknown'))
  await dialog.prepare('import_transaction',prepared,undefined,key)
  await dialog.prepare('import_transaction',prepared,undefined,key)
  expect(network.prepare.mock.calls.map(call=>call[1])).toEqual([key,key])
})
it('execution proposal key keeps successful manual preparations independent when no source key is supplied',async()=>{
  const {dialog,prepared}=await mountSessionDialog()
  await dialog.prepare('import_transaction',prepared)
  await dialog.prepare('import_transaction',prepared)
  const keys=network.prepare.mock.calls.map(call=>call[1])
  expect(keys).toHaveLength(2);expect(keys[0]).not.toBe(keys[1])
})
