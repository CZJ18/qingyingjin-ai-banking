import { expect,it } from 'vitest'
import { billingFindingText, planningExplanation, planReason } from '../src/lib/business-copy'

it('formats recorded summary from authoritative minor amounts, not the original prose',()=>{
 const text=billingFindingText({finding_id:'totals',description:'期间收入100000分，支出24000分'}, {total_income:100000,total_expense:24000})
 expect(text).toContain('¥1,000.00');expect(text).toContain('¥240.00')
 expect(text).toContain('不代表账户余额变化');expect(text).not.toContain('100000分')
})
it('preserves non-summary findings instead of replacing transaction-specific evidence',()=>{
 expect(billingFindingText({finding_id:'anomaly-1',description:'这两笔消费需核实'}, {total_income:0,total_expense:24000})).toBe('这两笔消费需核实')
})
it('explains actual plan amounts without treating future avoided spend as cash',()=>{
 const text=planningExplanation({balance_minor:576000,necessary_reserve_minor:206000,disposable_minor:90000},{feasible:true,savings_minor:7500,retained_minor:568500})
 expect(text).toContain('¥5,760.00');expect(text).toContain('¥75.00');expect(text).toContain('单独授权')
 expect(text).toContain('预计收入和未来少花的钱不计入当前余额')
})
it('does not describe an infeasible option as an achievable savings plan',()=>{
 expect(planningExplanation({balance_minor:0,necessary_reserve_minor:100,disposable_minor:0},{feasible:false,savings_minor:0,retained_minor:0})).toContain('暂不可行')
 expect(planReason('initial planning')).toBe('首次生成资金方案')
 expect(planReason('unknown system reason')).not.toContain('unknown system reason')
})
