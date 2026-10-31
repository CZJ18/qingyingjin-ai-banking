import { describe, expect, it, vi } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { createMemoryHistory, createRouter } from 'vue-router'
import { goalPace, selectedExpenseActions } from '../src/lib/expense-goal-actions'
import ExpenseGoalNextSteps from '../src/components/assistant/ExpenseGoalNextSteps.vue'
import type { Schema } from '../src/api/client'

const preview: Schema<'ExpensePreview'> = {
  source: 'deterministic_lifestyle_rules', billing_report_id: 'synthetic-owned-report',
  baseline_period: { from: '2026-09-30T16:00:00Z', to: '2026-10-31T16:00:00Z', timezone: 'Asia/Shanghai' },
  target_period: '2026-11', target_minor: 50000, historical_expense_minor: 24000,
  evidence_transaction_ids: ['synthetic-2','synthetic-3','synthetic-4'],
  result: { schema_version: '2.0', rule_version: 'lifestyle-2', status: 'needs_information', options: [],
    missing_information: ['complete_historical_month_billing_report'], conflicts: [],
    reduction_gap_minor: 0, savings_gap_minor: 0, requires_confirmation: true, financial_execution_enabled: false },
}

async function renderNextSteps(props: InstanceType<typeof ExpenseGoalNextSteps>['$props']) {
  const router=createRouter({history:createMemoryHistory(),routes:[{path:'/billing',component:{render:()=>null}}]})
  await router.push('/billing')
  await router.isReady()
  return renderToString(createSSRApp(ExpenseGoalNextSteps,props).use(router))
}

describe('actionable goal advice with incomplete evidence', () => {
  it('splits the chosen goal by the actual calendar month using integer cents', () => {
    expect(goalPace('2026-11', 50000)).toEqual({ days:30, dailyMinor:1667, weeklyMinor:11667 })
    expect(goalPace('2028-02', 50000)).toEqual({ days:29, dailyMinor:1724, weeklyMinor:12069 })
    expect(goalPace('2026-02', 50000)?.days).toBe(28)
    expect(goalPace('2026-11', 1)).toEqual({ days:30, dailyMinor:0, weeklyMinor:0 })
  })
  it('does not produce a pace from an invalid period, fractional cents or unsafe amount', () => {
    for (const period of ['2026-13','2026-00','2026-2','not-a-month']) expect(goalPace(period,50000)).toBeNull()
    for (const amount of [0,-1,1.1,NaN,Infinity,Number.MAX_SAFE_INTEGER]) expect(goalPace('2026-11',amount)).toBeNull()
    expect(goalPace('2026-11',1e12)?.weeklyMinor).toBe(233333333333)
  })
  it('only proposes actions for explicitly selected optional categories', () => {
    expect(selectedExpenseActions([])).toEqual([])
    expect(selectedExpenseActions(['dining','housing','medical','transport','unknown'])).toEqual([])
    const actions=selectedExpenseActions(['sports','sports','subscriptions'])
    expect(actions.map(action=>action.category)).toEqual(['sports','subscriptions'])
    expect(actions[0]?.action).toContain('保留正常运动')
    expect(actions[1]?.action).toContain('本人确认')
  })
  it('renders a useful trial without treating the target pace as verified savings', async () => {
    const html=await renderNextSteps({preview,adjustableCategories:['sports']})
    for (const text of ['先试行 7 天','16.67','116.67','仅是目标拆解','不是已核实能省下的金额',
      '保留正常运动','第 7 天复盘','完整月份','历史账单覆盖范围尚未核实','去补充账单并核对覆盖范围','不会修改预算、取消订阅或执行资金操作']) expect(html).toContain(text)
    expect(html).not.toContain('先核对将要续费的订阅')
    expect(preview.result.options).toEqual([])
    expect(preview.result.status).toBe('needs_information')
  })
  it('asks for a category choice rather than silently choosing what the user should cut', async () => {
    const html=await renderNextSteps({preview,adjustableCategories:[]})
    expect(html).toContain('先在上方选择')
    expect(html).not.toContain('保留正常运动')
  })
  it('anchors the selected trial action to the same report without treating recorded spending as savings', async () => {
    const report={report_id:preview.billing_report_id,category_breakdown:[
      {category:'sports',expense_minor:6000,transaction_ids:['synthetic-4']},
      {category:'dining',expense_minor:18000,transaction_ids:['synthetic-3']},
    ]} as Schema<'BillingReport'>
    const html=await renderNextSteps({preview,adjustableCategories:['sports','shopping'],report})
    expect(html).toContain('60.00')
    expect(html).toContain('1 笔')
    expect(html).toContain('记录金额不是可节省额度')
    expect(html).toContain('没有这类记录，金额仍待核实')
    expect(html).not.toContain('180.00')
    expect(html).not.toContain('已节省 ¥60.00')
  })
  it('never attaches another report to the goal observation or invents a zero baseline', async () => {
    const report={report_id:'unrelated-report',category_breakdown:[{category:'sports',expense_minor:99999,transaction_ids:['unrelated']}]} as Schema<'BillingReport'>
    const html=await renderNextSteps({preview,adjustableCategories:['sports'],report})
    expect(html).not.toContain('999.99')
    expect(html).toContain('账单依据尚未核对')
    expect(html).not.toContain('已记录运动 ¥0.00')
  })
  it('keeps the missing-statement link on the selected baseline month instead of today’s previous month', async () => {
    vi.useFakeTimers();vi.setSystemTime(new Date('2026-11-15T00:00:00Z'))
    const historical={...preview,baseline_period:{from:'2026-08-31T16:00:00Z',to:'2026-09-30T16:00:00Z',timezone:'Asia/Shanghai'}}
    try{
      const html=await renderNextSteps({preview:historical,adjustableCategories:['shopping']})
      expect(html).toContain('month=2026-09')
    }finally{vi.useRealTimers()}
  })
  it('does not manufacture daily zero targets or show the fallback for a valid calculated plan', async () => {
    const tiny=await renderNextSteps({preview:{...preview,target_minor:1},adjustableCategories:[]})
    expect(tiny).toContain('按月记录目标')
    expect(tiny).not.toContain('每天约')
    const valid=await renderNextSteps({preview:{...preview,result:{...preview.result,status:'ready'}},adjustableCategories:['sports']})
    expect(valid).not.toContain('先试行 7 天')
  })
})
