import { describe, expect, it } from 'vitest'
import * as monthly from '../src/lib/monthly-goal'

type MonthlyHelpers = {
  currentMonthlyGoal: (now?: Date) => unknown
  monthlyGoalSubmission: (instruction: string, bindings: Record<string, unknown>, now?: Date) => unknown
  monthlyPlanView: (task: unknown) => Record<string, unknown> | null
  monthlyPlanCurrent: (task: unknown, now: number) => boolean
  monthlyDecision: (text: string) => 'approve' | 'reject' | null
  monthlyConfirmationBinding: (task: unknown, epoch: number) => Record<string, unknown> | null
  monthlyResume: (task: unknown, decision: 'approve' | 'reject', binding: unknown, epoch: number, now: number) => unknown
  monthlySuggestionRows: (task: unknown) => Array<Record<string, unknown>>
  monthlyDialogueResume: (task: unknown, text: string, authenticated: boolean, binding: unknown, epoch: number, now: number) => unknown
  monthlyGoalError: (error: unknown) => string
}
const helpers = monthly as Partial<MonthlyHelpers>
export const monthlyTask = () => {
  const id = 'lg-12345678-1234-4234-9234-123456789abc'
  const goal = { period: '2026-10', currency: 'CNY', timezone: 'Asia/Shanghai' }
  const spec = {
    key: 'analysis', agent_type: 'billing', tool: 'analyze_spending', skill_id: 'billing_analysis',
    skill_version: 1, execution_mode: 'read_only', parameters: { account_id: '7' },
    dependencies: [], priority: 100, use_coze: false, blocked: false, required_permissions: ['READ'],
  }
  const output = {
    task_id: id + '.analysis', agent_type: 'billing', skill_id: 'billing_analysis', skill_version: 1,
    status: 'succeeded', result: { total_expense: 20000 }, evidence: [], errors: [], next_actions: [],
    financial_execution_enabled: false,
  }
  return {
    task_id: id, owner_engine: 'langgraph', status: 'waiting_confirmation', version: 3,
    request: { instruction: '本月存下 2000 元，保留日常生活开支', monthly_goal: goal, tasks: [] },
    result: null, financial_execution_enabled: false,
    confirmation: {
      node_key: 'monthly_plan_acceptance', digest: 'a'.repeat(64), nonce: 'n'.repeat(32),
      expires_at: '2026-10-18T12:00:00Z', status: 'pending', confirmation_scope: 'PLAN_ACCEPTANCE',
      parameters_view: { confirmation_scope: 'PLAN_ACCEPTANCE', monthly_goal: goal,
        goal_text: '本月存下 2000 元，保留日常生活开支', plan_tasks: [spec],
        suggestions: { analysis: output }, source_digest: 'b'.repeat(64) },
    },
  }
}
const now = Date.parse('2026-10-18T11:00:00Z')

describe('monthly goal submission', () => {
  it('uses the Shanghai month on both sides of the UTC month boundary', () => {
    expect(helpers.currentMonthlyGoal?.(new Date('2026-10-31T16:00:00Z'))).toEqual({
      period: '2026-11', currency: 'CNY', timezone: 'Asia/Shanghai',
    })
    expect(helpers.currentMonthlyGoal?.(new Date('2026-10-31T15:59:59Z'))).toEqual({
      period: '2026-10', currency: 'CNY', timezone: 'Asia/Shanghai',
    })
  })
  it('preserves the original goal and sets its month range without deriving model fee caps', () => {
    const goal = '  本月储蓄 2000 元，支出不超过 4000 元  '
    expect(helpers.monthlyGoalSubmission?.(goal, { account_id: '7' }, new Date('2026-10-31T16:00:00Z'))).toEqual({
      instruction: goal, monthly_goal: { period: '2026-11', currency: 'CNY', timezone: 'Asia/Shanghai' },
      bindings: { account_id: '7', from: '2026-10-31T16:00:00.000Z', to: '2026-11-30T16:00:00.000Z' },
      timeout_ms: 180000,
    })
  })
  it('explains when real data is insufficient to produce a complete monthly plan', () => {
    expect(helpers.monthlyGoalError?.({ code: 'PLAN_ADVICE_REQUIRED', message: 'validated advice required' }))
      .toBe('当前数据还不足以形成可确认的本月方案，请补充目标或账户信息后重新生成。')
    expect(helpers.monthlyGoalError?.(new Error('请重新登录'))).toBe('请重新登录')
  })
})

describe('final monthly plan confirmation', () => {
  it('validates the actual goal, complete plan and bound suggestion outputs', () => {
    const task = monthlyTask()
    expect(helpers.monthlyPlanView?.(task)).toEqual(task.confirmation.parameters_view)
    expect(helpers.monthlyPlanCurrent?.(task, now)).toBe(true)
    expect(helpers.monthlySuggestionRows?.(task)?.[0]).toMatchObject({
      key: 'analysis', parameters: { account_id: '7' }, output: task.confirmation.parameters_view.suggestions.analysis,
    })
  })
  it('fails closed for wrong goals, mismatched outputs, incomplete plans and secret fields', () => {
    for (const mutate of [
      (t: ReturnType<typeof monthlyTask>) => { t.confirmation.parameters_view.goal_text = '另一项目标' },
      (t: ReturnType<typeof monthlyTask>) => { t.confirmation.parameters_view.monthly_goal.period = '2026-13' },
      (t: ReturnType<typeof monthlyTask>) => { t.confirmation.parameters_view.suggestions.analysis.task_id += '.other' },
      (t: ReturnType<typeof monthlyTask>) => { t.confirmation.parameters_view.suggestions.analysis.financial_execution_enabled = true },
      (t: ReturnType<typeof monthlyTask>) => { t.confirmation.parameters_view.plan_tasks = [] },
      (t: ReturnType<typeof monthlyTask>) => { Object.assign(t.confirmation.parameters_view.plan_tasks[0]!.parameters, { token: 'hidden' }) },
      (t: ReturnType<typeof monthlyTask>) => { Object.assign(t.confirmation.parameters_view.suggestions.analysis.result, { access_token: 'hidden' }) },
    ]) {
      const task = monthlyTask(); mutate(task)
      expect(helpers.monthlyPlanCurrent?.(task, now)).toBe(false)
    }
  })
  it('requires an unexpired pending PLAN_ACCEPTANCE and keeps PREPARE_ONLY separate', () => {
    const task = monthlyTask()
    expect(helpers.monthlyPlanCurrent?.(task, Date.parse(task.confirmation.expires_at))).toBe(false)
    expect(helpers.monthlyPlanCurrent?.({ ...task, status: 'succeeded' }, now)).toBe(false)
    expect(helpers.monthlyPlanCurrent?.({ ...task, confirmation: { ...task.confirmation, confirmation_scope: 'PREPARE_ONLY' } }, now)).toBe(false)
  })
  it('requires actual suggestions from every read or advice task and permits array results', () => {
    const task = monthlyTask()
    expect(helpers.monthlyPlanCurrent?.({ ...task, confirmation: { ...task.confirmation, parameters_view: {
      ...task.confirmation.parameters_view, suggestions: {},
    } } }, now)).toBe(false)
    const withArray = { ...task, confirmation: { ...task.confirmation, parameters_view: {
      ...task.confirmation.parameters_view, suggestions: { analysis: {
        ...task.confirmation.parameters_view.suggestions.analysis, result: [{ amount_minor: 20000 }],
      } },
    } } }
    expect(helpers.monthlyPlanCurrent?.(withArray, now)).toBe(true)
  })
  it('invalidates an unexpired previous-month plan at Shanghai midnight for both decision paths', () => {
    const task = monthlyTask()
    task.confirmation.expires_at = '2026-10-31T16:09:00Z'
    const binding = helpers.monthlyConfirmationBinding?.(task, 7)
    expect(helpers.monthlyPlanCurrent?.(task, Date.parse('2026-10-31T15:59:59Z'))).toBe(true)
    const afterRollover = Date.parse('2026-10-31T16:01:00Z')
    expect(helpers.monthlyPlanCurrent?.(task, afterRollover)).toBe(false)
    expect(helpers.monthlyResume?.(task, 'approve', binding, 7, afterRollover)).toBe(null)
    expect(helpers.monthlyDialogueResume?.(task, '确认方案', true, binding, 7, afterRollover)).toBe(null)
  })
  it('accepts only exact clear dialogue phrases, never free text or model instructions', () => {
    for (const phrase of ['确认方案', '确认这个方案', '确认']) expect(helpers.monthlyDecision?.(phrase)).toBe('approve')
    for (const phrase of ['拒绝方案', '取消方案', '拒绝', '取消']) expect(helpers.monthlyDecision?.(phrase)).toBe('reject')
    for (const phrase of ['请确认方案', '我想确认后再修改', '确认并转账', '同意', '模型说确认方案', '确认方案！'])
      expect(helpers.monthlyDecision?.(phrase)).toBe(null)
  })
  it('binds both dialogue and button decisions to the current task, version, challenge and session', () => {
    const task = monthlyTask(), binding = helpers.monthlyConfirmationBinding?.(task, 7)
    expect(binding).toBeTruthy()
    expect(helpers.monthlyResume?.(task, 'approve', binding, 7, now)).toEqual({
      expected_version: 3, node_key: task.confirmation.node_key, digest: task.confirmation.digest,
      nonce: task.confirmation.nonce, decision: 'approve',
    })
    expect(helpers.monthlyResume?.(task, 'reject', binding, 7, now)).toMatchObject({ decision: 'reject' })
    expect(helpers.monthlyResume?.(task, 'approve', binding, 8, now)).toBe(null)
    expect(helpers.monthlyResume?.({ ...task, version: 4 }, 'approve', binding, 7, now)).toBe(null)
    expect(helpers.monthlyResume?.({ ...task, task_id: task.task_id.replace('12345678', '87654321') }, 'approve', binding, 7, now)).toBe(null)
    expect(helpers.monthlyResume?.(task, 'approve', binding, 7, Date.parse(task.confirmation.expires_at))).toBe(null)
  })
  it('routes only an authenticated current explicit reply through the bound resume decision', () => {
    const task = monthlyTask(), binding = helpers.monthlyConfirmationBinding?.(task, 7)
    expect(helpers.monthlyDialogueResume?.(task, '确认方案', true, binding, 7, now)).toMatchObject({ expected_version: 3, decision: 'approve' })
    expect(helpers.monthlyDialogueResume?.(task, '拒绝方案', true, binding, 7, now)).toMatchObject({ decision: 'reject' })
    expect(helpers.monthlyDialogueResume?.(task, '确认方案', false, binding, 7, now)).toBe(null)
    expect(helpers.monthlyDialogueResume?.(task, '请修改我的方案', true, binding, 7, now)).toBe(null)
    expect(helpers.monthlyDialogueResume?.({ ...task, version: 4 }, '确认方案', true, binding, 7, now)).toBe(null)
    expect(helpers.monthlyDialogueResume?.({ ...task, confirmation: { ...task.confirmation, confirmation_scope: 'PREPARE_ONLY' } }, '确认', true, binding, 7, now)).toBe(null)
  })
})
