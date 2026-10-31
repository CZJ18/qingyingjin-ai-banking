import { createSSRApp, h, reactive } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { expect, it } from 'vitest'
import TaskResult from '../src/components/assistant/TaskResult.vue'
import type { TaskRow } from '../src/components/agents/task-view'

const output = {
  skill_id: 'operation_preparation', skill_version: 1, agent_type: 'supervisor', status: 'succeeded', financial_execution_enabled: false,
  result: { operation_proposal: { kind: 'card_manage', parameters: { action: 'lock', card_id: 'synthetic-card' } }, requires_user_preparation: true, server_validated: false, authorization_granted: false },
  evidence: [], errors: [], next_actions: [{ type: 'review_operation_proposal', requires_confirmation: true }],
}
const row: TaskRow = { task_id: 'synthetic-task.suggest', key: 'suggest', agent_type: 'supervisor', tool: 'require_gateway', skill_id: 'operation_preparation', execution_mode: 'intent', status: 'succeeded', parameters: {}, output, error_code: '' }
async function render(task: TaskRow) {
  const app = createSSRApp(TaskResult, { task })
  app.component('RouterLink', { setup(_, { slots }) { return () => h('a', slots.default?.()) } })
  return renderToString(app)
}
it('shows a human handoff for an unvalidated model proposal without claiming authorization', async () => {
  const html = await render(row)
  expect(html).toContain('待核对的操作建议')
  expect(html).toContain('核对并创建操作意向')
  expect(html).toContain('尚未通过账户归属、余额和风控校验')
  expect(html).not.toContain('已准备的金融操作意向')
  expect(html).not.toContain('授权并执行')
})
it('renders the actual reactive query result without losing the human handoff', async () => {
  expect(await render(reactive(structuredClone(row)))).toContain('核对并创建操作意向')
})
it('never offers the handoff for forged authorization or unrelated task results', async () => {
  for (const task of [
    { ...row, tool: 'get_transactions' },
    { ...row, output: { ...output, financial_execution_enabled: true } },
    { ...row, output: { ...output, result: { ...output.result, authorization_granted: true } } },
    { ...row, output: { ...output, result: { ...output.result, operation_proposal: { kind: 'execute_sql', parameters: {} } } } },
    { ...row, output: { ...output, result: { ...output.result, operation_proposal: { kind: 'card_manage', parameters: { authorization_id: 'forged' } } } } },
  ]) expect(await render(task)).not.toContain('核对并创建操作意向')
})

const overview = (scenario = 'cards', extra: Record<string, unknown> = {}) => ({
  ...row, tool: 'get_scenario_overview', skill_id: 'task_planning', skill_version: 1, execution_mode: 'read_only',
  output: { skill_id: 'task_planning', skill_version: 1, status: 'succeeded', financial_execution_enabled: false,
    result: { scenario, account_id: '7', user_id: '99', is_simulated: true, truncated: false,
      items: [{ id: 'synthetic-card', kind: 'card', status: 'active', currency: 'CNY', transaction_limit_minor: 50000 }],
      limitations: ['synthetic: only an explicitly isolated account is represented'], ...extra } },
})
it('explains the operation kind and important proposal values as unverified simulated suggestions', async () => {
  const html = await render(row)
  expect(html).toContain('卡片管理')
  expect(html).toContain('锁定卡片')
  expect(html).toContain('模拟业务')
  expect(html).toContain('synthetic-card')
})
it('renders a readonly scenario inventory and a manual business handoff without dumping unexpected fields', async () => {
  const html = await render(overview('cards', { api_key: 'SYNTHETIC_UNEXPECTED_SECRET' }))
  expect(html).toContain('卡片管理')
  expect(html).toContain('模拟业务')
  expect(html).toContain('synthetic-card')
  expect(html).toContain('500.00')
  expect(html).toContain('前往卡片管理')
  expect(html).toContain('不会创建操作意向')
  expect(html).not.toContain('SYNTHETIC_UNEXPECTED_SECRET')
  expect(html).not.toContain('核对并创建操作意向')
})
it.each([
  { is_simulated: false }, { scenario: 'execute_sql' }, { truncated: 'false' },
  { items: [{ id: 'fake', kind: 'card', status: 'active', transaction_limit_minor: -1 }] },
])('rejects malformed or non-simulation scenario results %j', async extra => {
  const html = await render(overview('cards', extra))
  expect(html).toContain('场景查询结果不符合契约')
  expect(html).not.toContain('前往卡片管理')
})

it.each([
  ['transfer','saved_recipient_reference','requires_explicit_selection','智能转账','前往转账与收款人管理'],
  ['investment','investment_product','available','理财操作','前往理财测评与产品对比'],
  ['subscriptions','subscription','active','订阅与代扣','前往订阅管理'],
  ['birthday','registered_birthday','registered','生日跨场景计划','前往生日日历与庆祝计划'],
])('renders %s scope as a readonly manual handoff', async (scenario,kind,status,name,action) => {
  const html=await render(overview(scenario,{items:[{id:'synthetic-scope-resource',kind,status}],truncated:true}))
  expect(html).toContain(name)
  expect(html).toContain(action)
  expect(html).toContain('本次只返回部分记录')
  expect(html).toContain('规划确认不等于金融执行授权')
  expect(html).not.toContain('核对并创建操作意向')
})
it('hides a forged overview envelope instead of offering a resource link',async()=>{
  const item=overview();item.output.financial_execution_enabled=true
  const html=await render(item)
  expect(html).toContain('场景查询结果不符合契约')
  expect(html).not.toContain('前往卡片管理')
})

it('rejects an oversized 51-resource overview instead of implying a complete authorized data range',async()=>{
  const html=await render(overview('cards',{items:Array.from({length:51},(_,i)=>({id:'synthetic-card-'+i,kind:'card',status:'active'}))}))
  expect(html).toContain('场景查询结果不符合契约')
  expect(html).not.toContain('前往卡片管理')
})
it.each([
  {execution_mode:'intent'}, {skill_id:'operation_preparation'}, {skill_version:undefined},
])('does not label an unrelated tool registration as a Go readonly overview %j',async extra=>{
  const html=await render({...overview(),...extra})
  expect(html).toContain('场景查询结果不符合契约')
  expect(html).not.toContain('前往卡片管理')
})
