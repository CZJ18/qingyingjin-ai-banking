import { money } from './utils'

// Display persisted structured values; never infer money from model prose.
export function billingFindingText(finding: { finding_id: string; description: string }, report: {total_income:number;total_expense:number}) {
  if (finding.finding_id !== 'totals') return finding.description
  return `期间已记录收入 ${money(report.total_income)}、支出 ${money(report.total_expense)}。仅统计外部收支；历史补录参与分析，不代表账户余额变化。`
}

export function planningExplanation(result: {balance_minor:number;necessary_reserve_minor:number;disposable_minor:number}, option: {feasible:boolean;savings_minor:number;retained_minor:number}) {
  const summary = `账户资金 ${money(result.balance_minor)}，必要开支预留 ${money(result.necessary_reserve_minor)}，可支配资金 ${money(result.disposable_minor)}。`
  return summary + (option.feasible
    ? `本方案建议储蓄 ${money(option.savings_minor)}，安排后保留 ${money(option.retained_minor)}。`
    : '当前约束下本方案暂不可行，请先核对必要开支、预留和目标。') +
    '预计收入和未来少花的钱不计入当前余额。建议需在数据变化后重新核对，资金操作仍需单独授权。'
}

export function planReason(reason: string) {
  const known: Record<string,string> = {
    'initial planning':'首次生成资金方案',
    'user updated cashflow, necessary expenses or reserves':'根据你修改的收入、必要开支或预留重新计算',
    'explicit advisory replan':'根据最新账户数据重新计算建议',
  }
  return known[reason] || (/[\u3400-\u9fff]/.test(reason) ? reason : '服务端根据业务数据更新了方案；可展开查看原始原因。')
}
