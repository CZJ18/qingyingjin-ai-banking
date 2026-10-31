// These are user-chosen target rhythms and optional trial actions, not a
// forecast, a verified reduction, a new budget or financial authorization.
import { minor } from './utils'

export function statementCheckInput(count: string, income: string, expense: string, confirmed: boolean) {
  if (!confirmed) throw new Error('请先按导出账单核对该月全部记录，并确认核对范围')
  if (!/^\d{1,5}$/.test(count.trim()) || Number(count.trim()) > 10000)
    throw new Error('请填写账单汇总中的有效交易笔数（0—10000）')
  return { full_month_confirmed: true as const, transaction_count: Number(count.trim()),
    income_minor: minor(income.trim()), expense_minor: minor(expense.trim()) }
}

export function goalPace(period: string, targetMinor: number) {
  if (!/^[1-9]\d{3}-(0[1-9]|1[0-2])$/.test(period) ||
      !Number.isSafeInteger(targetMinor) || targetMinor <= 0 || targetMinor > 1e12) return null
  const [year, month] = period.split('-').map(Number)
  const days = new Date(Date.UTC(year!, month!, 0)).getUTCDate()
  const denominator = BigInt(days), target = BigInt(targetMinor)
  const round = (value: bigint) => Number((value + denominator / 2n) / denominator)
  return { days, dailyMinor: round(target), weeklyMinor: round(target * 7n) }
}

const actions = {
  shopping: {
    title: '给非必要购物留出冷静期',
    action: '非必要购买先放置 24 小时，再核对是否仍需要；不靠分期、借款或延后付款凑节省目标。',
    alternative: '先检查现有物品能否继续使用；确有需要再比较维修、替换或购买，保留必要购买，不因购物分类就断定每笔都能省掉。',
    check: '记下主动放弃的购买，再看本周总支出是否真的下降，不把延期购买直接算作已节省。',
  },
  subscriptions: {
    title: '先核对将要续费的订阅',
    action: '若有订阅，逐项核对下次扣费日期、续费价格和实际需要；只把本人确认不再需要的项目列入待决定清单。',
    alternative: '若已有同类权益，先核对是否覆盖实际需要，再考虑按需付费；没有合适替代就保留，停扣必须另行确认。',
    check: '是否取消由你逐项决定；未确认停扣前，不把未来费用记成已节省。',
  },
  sports: {
    title: '先试不增加开支的运动安排',
    action: '保留正常运动；下次付费前，比较免费场地、已有会员与按次方案，优先试不增加开支的替代方式。',
    alternative: '优先使用已有会员或可用的免费场地；核对安全与适用性后再决定，不为了凑目标停止必要的健康安排。',
    check: '本周记录实际运动开支；先核实已有会员的使用和到期情况，不直接假定可以取消或节省多少。',
  },
  other: {
    title: '找出一项可以主动减少的可选开支',
    action: '先连续记录 7 天的可选小额消费，选一项愿意减少的消费频次；保留必要生活开支。',
    alternative: '先把未分类记录核对清楚；对确实可选的消费尝试降低一次频次或使用已有物品，不把必要支出一并删掉。',
    check: '记录实际消费次数和金额，周末检查是否转移到了其他类别，避免只是换个地方花钱。',
  },
} as const

// A zero ceiling is a mathematical boundary, not evidence that stopping all
// purchases in a category is realistic. Keep the original financial numbers.
export function expenseOptionIsExtreme(categories: readonly { original_minor: number; proposed_minor: number }[]) {
  return categories.some(row => Number.isSafeInteger(row.original_minor) && row.original_minor > 0 && row.proposed_minor === 0)
}

export function selectedExpenseActions(categories: readonly string[]) {
  return [...new Set(categories)].flatMap(category =>
    Object.hasOwn(actions, category)
      ? [{ category, ...actions[category as keyof typeof actions] }]
      : [])
}
