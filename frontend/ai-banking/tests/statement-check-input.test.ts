import { expect, it } from 'vitest'
import { statementCheckInput } from '../src/lib/expense-goal-actions'

it('encodes explicit source totals as exact integer cents, including zero income', () => {
 expect(statementCheckInput('31','0','1234.56',true)).toEqual({full_month_confirmed:true,transaction_count:31,income_minor:0,expense_minor:123456})
})
it('requires every independently entered total and full-month declaration', () => {
 for(const [count,income,expense,confirmed] of [['','0','1',true],['1','','1',true],['1','0','',true],['1','0','1',false]] as const)
  expect(()=>statementCheckInput(count,income,expense,confirmed)).toThrow()
})
it('rejects fractional or oversized row counts and inexact amounts', () => {
 for(const count of ['-1','1.5','1e2','10001','Infinity'])expect(()=>statementCheckInput(count,'0','1',true)).toThrow()
 for(const amount of ['-1','1.001','1e3','10000000000.01'])expect(()=>statementCheckInput('1','0',amount,true)).toThrow()
})
