import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import * as Vue from 'vue'
import { createRenderer, defineComponent, h, nextTick, ref, ssrContextKey } from 'vue'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { readFileSync } from 'node:fs'
import { parse, compileScript, compileTemplate } from '@vue/compiler-sfc'
import { transformSync } from 'esbuild'
import Agents from '../src/pages/Agents.vue'
import TaskResult from '../src/components/assistant/TaskResult.vue'
import ScenarioOverview from '../src/components/assistant/ScenarioOverview.vue'
import { useSession } from '../src/lib/store'

const io = vi.hoisted(() => ({ task: null as any, prepare: vi.fn(), inspect: vi.fn(), openGoal: vi.fn() }))
vi.mock('../src/lib/job', async () => {
  const { ref } = await import('vue')
  const query = () => ({ data: ref(undefined), isLoading: ref(false), error: ref(null), refetch: vi.fn() })
  return { useJob: () => ({ job: query(), trace: query(), connection: ref('idle') }) }
})
vi.mock('../src/lib/agent-job', async () => {
  const { ref } = await import('vue')
  return { useAgentJob: () => ({ task: { data: ref(io.task), isLoading: ref(false), error: ref(null), refetch: vi.fn() }, events: { data: ref([]), error: ref(null), isLoading: ref(false), refetch: vi.fn() }, connection: ref('done') }) }
})
vi.mock('../src/lib/task-history', async () => {
  const { ref } = await import('vue')
  return { useTaskHistory: () => ({ items: ref([]), total: ref(0), isLoading: ref(false), isFetching: ref(false), error: ref(null), hasNextPage: ref(false), isFetchingNextPage: ref(false), refetch: vi.fn() }) }
})
vi.mock('../src/components/ExecutionDialog.vue', () => ({ default: defineComponent({ setup(_, { expose }) { expose({ prepare: io.prepare, inspect: io.inspect }); return () => h('div') } }) }))
vi.mock('../src/components/assistant/ExpenseGoalAdvice.vue', () => ({ default: defineComponent({ setup(_, { expose }) { expose({ openForm: io.openGoal }); return () => h('div') } }) }))
vi.mock('../src/components/ui/button/Button.vue', () => ({ default: defineComponent({ setup(_, { attrs, slots }) { return () => h('button', attrs, slots.default?.()) } }) }))
vi.mock('../src/components/Status.vue', () => ({ default: defineComponent({ setup() { return () => h('span') } }) }))
vi.mock('../src/components/State.vue', () => ({ default: defineComponent({ setup(_, { slots }) { return () => h('div', slots.default?.()) } }) }))
vi.mock('../src/components/agents/DependencyFlow.vue', () => ({ default: defineComponent({ setup() { return () => h('div') } }) }))
vi.mock('../src/components/Chart.vue', () => ({ default: defineComponent({ setup() { return () => h('div') } }) }))
vi.mock('../src/components/JsonDetails.vue', () => ({ default: defineComponent({ setup() { return () => h('div') } }) }))

for (const [component, path] of [[Agents, 'pages/Agents.vue'], [TaskResult, 'components/assistant/TaskResult.vue'], [ScenarioOverview, 'components/assistant/ScenarioOverview.vue']] as const) {
  const { descriptor } = parse(readFileSync(new URL('../src/' + path, import.meta.url), 'utf8'))
  const bindings = compileScript(descriptor, { id: path }).bindings
  const compiled = compileTemplate({ source: descriptor.template!.content, filename: path, id: path, compilerOptions: { bindingMetadata: bindings } })
  const code = transformSync(compiled.code, { loader: 'ts' }).code.replace(/import \{([^}]+)\} from "vue"/g, (_, names: string) => 'const {' + names.replace(/ as /g, ':') + '} = Vue').replace('export function render', 'return function render')
  ;(component as any).render = new Function('Vue', code)(Vue)
}
class Element { children: Element[] = []; parent: Element | null = null; props: Record<string, any> = {}; text = ''; value = ''; listeners: Record<string, (event: any) => void> = {}; constructor(public tag: string) {} addEventListener(name: string, fn: (event: any) => void) { this.listeners[name] = fn } removeEventListener() {} }
const renderer = createRenderer<Element, Element>({
  createElement: tag => new Element(tag), createText: text => Object.assign(new Element('#text'), { text }), createComment: () => new Element('#comment'),
  setText: (node, text) => { node.text = text }, setElementText: (node, text) => { node.text = text; node.children = [] },
  parentNode: node => node.parent, nextSibling: node => node.parent?.children[node.parent.children.indexOf(node) + 1] || null,
  insert(node, parent, anchor) { node.parent = parent; const index = anchor ? parent.children.indexOf(anchor) : -1; if (index < 0) parent.children.push(node); else parent.children.splice(index, 0, node) },
  remove(node) { node.parent?.children.splice(node.parent.children.indexOf(node), 1); node.parent = null }, patchProp(node, key, _old, value) { node.props[key] = value },
})
const nodes = (node: Element): Element[] => [node, ...node.children.flatMap(nodes)]
const text = (node: Element): string => node.text + node.children.map(text).join('')
const cleanup: (() => void)[] = []
const proposal = { kind: 'card_manage', parameters: { action: 'lock', card_id: 'synthetic-card' } }
function task(result: Record<string, any>, tool = 'require_gateway', skill = 'operation_preparation') {
  return { task_id: 'lg-11111111-1111-4111-8111-111111111111', status: 'succeeded', created_at: '2026-10-28T00:00:00Z', version: 1, request: { instruction: '合成隔离测试', tasks: [{ key: 'step', agent_type: 'supervisor', tool, skill_id: skill, execution_mode: 'intent', parameters: {}, dependencies: [] }] }, result: { outputs: [{ task_id: 'lg-11111111-1111-4111-8111-111111111111.step', agent_type: 'supervisor', skill_id: skill, status: 'succeeded', financial_execution_enabled: false, result }] } }
}
beforeEach(() => {
  io.prepare.mockReset(); io.inspect.mockReset(); io.openGoal.mockReset()
  vi.stubGlobal('fetch', vi.fn(() => { throw new Error('external and local network forbidden in this isolated UI test') }))
})
afterEach(() => { cleanup.splice(0).forEach(fn => fn()); vi.unstubAllGlobals() })
async function mount() {
  const root = new Element('root'), pinia = createPinia(), session = useSession(pinia)
  session.connected = true; session.selectedAgentTask = io.task.task_id
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/agents', component: Agents }, ...['/security','/billing','/scenarios','/subscriptions'].map(path=>({path,component:defineComponent({render:()=>h('div')})}))] })
  await router.push('/agents'); await router.isReady()
  const app = renderer.createApp(Agents)
  app.provide(ssrContextKey, { modules: new Set() }); app.use(pinia).use(router); app.mount(root)
  cleanup.push(() => app.unmount()); await nextTick()
  return root
}
it('clicking the actual proposal button in task details reaches the existing preparation dialog once', async () => {
  io.task = task({ operation_proposal: proposal, requires_user_preparation: true, server_validated: false, authorization_granted: false })
  const root = await mount(), button = nodes(root).find(n => n.tag === 'button' && text(n).includes('核对并创建操作意向'))
  expect(button).toBeDefined(); button!.props.onClick(); await nextTick()
  await vi.waitFor(() => expect(io.prepare).toHaveBeenCalledExactlyOnceWith('card_manage', proposal.parameters, undefined, expect.stringMatching(/^agent-op-[a-f0-9]{64}$/)))
  expect(globalThis.fetch).not.toHaveBeenCalled()
})
it('clicking the actual billing result goal button opens the existing local target form', async () => {
  io.task = task({ category_breakdown: [], transaction_count: 0, total_income: 0, total_expense: 0 }, 'generate_billing_report', 'billing_analysis')
  const root = await mount(), button = nodes(root).find(n => n.tag === 'button' && text(n).includes('设置下月少花目标'))
  expect(button).toBeDefined(); button!.props.onClick(); await nextTick()
  expect(io.openGoal).toHaveBeenCalledOnce()
  expect(io.prepare).not.toHaveBeenCalled(); expect(globalThis.fetch).not.toHaveBeenCalled()
})

it('repeated preparation of one persisted proposal passes the same stable transport key', async () => {
  io.task = task({ operation_proposal: proposal, requires_user_preparation: true, server_validated: false, authorization_granted: false })
  const root = await mount(), button = nodes(root).find(n => n.tag === 'button' && text(n).includes('核对并创建操作意向'))!
  button.props.onClick(); await nextTick()
  await vi.waitFor(() => expect(io.prepare).toHaveBeenCalledTimes(1))
  button.props.onClick(); await nextTick()
  await vi.waitFor(() => expect(io.prepare).toHaveBeenCalledTimes(2))
  const first = io.prepare.mock.calls[0]![3], second = io.prepare.mock.calls[1]![3]
  expect(first).toMatch(/^agent-op-[a-f0-9]{64}$/)
  expect(second).toBe(first)
  expect(globalThis.fetch).not.toHaveBeenCalled()
})

it('shows persisted natural-language scenario resources without inventing the missing request plan', async () => {
  io.task = task({ scenario: 'transfer', account_id: '23', user_id: '42', is_simulated: true, truncated: false,
    items: [{ id: '23', kind: 'source_account', status: 'active', currency: 'CNY', amount_minor: 1234 }],
    limitations: ['仅限隔离模拟账户'] }, '', 'task_planning')
  io.task.request = { instruction: '只读查看转账范围', tasks: [], bindings: { account_id: '23' } }
  io.task.result.outputs[0].skill_version = 1
  const root = await mount()
  expect(text(root)).toContain('智能转账 · 模拟业务')
  expect(text(root)).toContain('所选资金账户')
  expect(text(root)).toContain('12.34')
  expect(io.task.request.tasks).toEqual([])
  expect(io.prepare).not.toHaveBeenCalled()
  expect(globalThis.fetch).not.toHaveBeenCalled()
})

it('does not relabel an explicitly different execution mode as a read-only scenario', async () => {
  io.task = task({ scenario: 'cards', account_id: '23', user_id: '42', is_simulated: true, truncated: false,
    items: [], limitations: ['仅限隔离模拟账户'] }, '', 'task_planning')
  io.task.result.outputs[0].skill_version = 1
  const root = await mount()
  expect(text(root)).not.toContain('卡片管理 · 模拟业务')
  expect(io.prepare).not.toHaveBeenCalled()
})
