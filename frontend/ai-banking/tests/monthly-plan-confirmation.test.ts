import { describe, expect, it } from 'vitest'
import { createSSRApp, h } from 'vue'
import { renderToString } from '@vue/server-renderer'
import MonthlyPlanConfirmation from '../src/components/agents/MonthlyPlanConfirmation.vue'

const monthlyTask = () => {
  const id = 'lg-12345678-1234-4234-9234-123456789abc'
  const goal = { period: '2026-10', currency: 'CNY', timezone: 'Asia/Shanghai' }
  const spec = { key: 'plan', agent_type: 'savings', tool: 'get_financial_plan', skill_id: 'savings_planning',
    skill_version: 1, execution_mode: 'read_only', parameters: { plan_id: '7', amount_minor: 200000,
      source_account_id: '7', target_account_id: '8', constraints: ['保留生活开支'] }, dependencies: [],
    priority: 100, use_coze: false, blocked: false, required_permissions: ['READ'] }
  return { task_id: id, owner_engine: 'langgraph', version: 3, status: 'waiting_confirmation', financial_execution_enabled: false,
    request: { instruction: '本月存下 2000 元 <script>alert(1)</script>', monthly_goal: goal }, confirmation: {
      node_key: 'monthly_plan_acceptance', digest: 'a'.repeat(64), nonce: 'n'.repeat(32), status: 'pending',
      confirmation_scope: 'PLAN_ACCEPTANCE', expires_at: '2099-10-18T12:00:00Z', parameters_view: {
        confirmation_scope: 'PLAN_ACCEPTANCE', monthly_goal: goal, goal_text: '本月存下 2000 元 <script>alert(1)</script>',
        plan_tasks: [spec], suggestions: { plan: { task_id: id + '.plan', agent_type: 'savings', skill_id: 'savings_planning',
          skill_version: 1, status: 'succeeded', result: { note: '实际服务端建议', constraints: ['保留生活开支'] },
          financial_execution_enabled: false } }, source_digest: 'b'.repeat(64),
      },
    },
  }
}
async function render(task: unknown) {
  const app = createSSRApp(MonthlyPlanConfirmation, { task, busy: false, epoch: 7 })
  app.component('RouterLink', { setup(_, { slots }) { return () => h('a', slots.default?.()) } })
  return renderToString(app)
}
describe('monthly final plan presentation', () => {
  it('shows month, original goal, every suggestion and complete parameters as escaped text', async () => {
    const html = await render(monthlyTask())
    for (const value of ['2026-10', '本月存下 2000 元', '实际服务端建议', '200000', '保留生活开支', '确认方案', '拒绝方案'])
      expect(html).toContain(value)
    expect(html).toContain('&lt;script&gt;alert(1)&lt;/script&gt;')
    expect(html).not.toContain('<script>')
    expect(html).not.toContain('n'.repeat(32))
    expect(html).not.toContain('a'.repeat(64))
    expect(html).not.toContain('b'.repeat(64))
  })
  it('shows the expiry message and disables both decisions after the challenge expires', async () => {
    const task = monthlyTask()
    task.confirmation.expires_at = '2020-01-01T00:00:00Z'
    const html = await render(task)
    expect(html).toContain('该方案确认已失效')
    expect(html.match(/<button[^>]*disabled/g)).toHaveLength(2)
  })
})
