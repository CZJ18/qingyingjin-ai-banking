export type ModelAdmission = {
  currency: string
  frozen: boolean
  approval_window: {
    next_request_maximum_cost: string
    remaining_approval_budget: string
    remaining_requests: number
    remaining_concurrent_slots: number
  }
  approval_limits: { max_requests: number; max_concurrent: number; simulation: boolean }
}

function money(value: unknown): bigint | null {
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]{0,10})(\.[0-9]{1,8})?$/.test(value)) return null
  const [whole, fraction = ''] = value.split('.')
  return BigInt(whole!) * 100000000n + BigInt(fraction.padEnd(8, '0'))
}
function object(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}
export function modelAdmissionState(value: unknown): { available: boolean; reason: string; snapshot: ModelAdmission | null } {
  const raw = object(value), window = object(raw.approval_window), limits = object(raw.approval_limits)
  const cost = money(window.next_request_maximum_cost), balance = money(window.remaining_approval_budget)
  if (typeof raw.currency !== 'string' || !/^[A-Z]{3}$/.test(raw.currency) || typeof raw.frozen !== 'boolean' ||
      typeof limits.simulation !== 'boolean' || cost === null || balance === null ||
      ![limits.max_requests, limits.max_concurrent].every(v => Number.isSafeInteger(v) && Number(v) > 0) ||
      ![window.remaining_requests, window.remaining_concurrent_slots].every(v => Number.isSafeInteger(v) && Number(v) >= 0) ||
      Number(window.remaining_requests) > Number(limits.max_requests) ||
      Number(window.remaining_concurrent_slots) > Number(limits.max_concurrent))
    return { available: false, reason: '尚未取得完整的模型准入状态，请先检查服务和本轮审批。', snapshot: null }
  const snapshot = value as ModelAdmission
  if (snapshot.frozen) return { available: false, reason: '模型费用账本当前已冻结，请先核查费用记录与冻结原因。', snapshot }
  if (Number(window.remaining_requests) < 1)
    return { available: false, reason: '本轮模型请求次数已用尽，单加预算不会恢复次数。已有分析仍可查看；新周期完成启用后再开始分析。', snapshot }
  if (Number(window.remaining_concurrent_slots) < 1)
    return { available: false, reason: '已有模型请求正在处理或等待结果核实，请先查看任务历史，不要重复提交。', snapshot }
  if (balance < cost)
    return { available: false, reason: '剩余预算不足以覆盖下一次请求的最大预占，暂不能开始新分析。已有分析仍可查看。', snapshot }
  return { available: true, reason: '', snapshot }
}

export function modelFailureMessage(code: string, reason = ''): string {
  if (code === 'MODEL_MONETARY_ADMISSION_REQUIRED' && reason === 'MODEL_ADMISSION_NOT_CONFIGURED')
    return '当前入口尚未配置真实AI准入，暂不能发送新分析。输入可以保留；你仍可查询账单、订阅和卡片，或试算节省目标，这些操作不调用模型。'
  const messages: Record<string, string> = {
    COZE_UNAVAILABLE: '模型连接中断，原请求可能已发出。不要重复提交或恢复任务，请先查询原任务与费用记录。',
    MODEL_CONTEXT_LIMIT_EXCEEDED: '输入内容超出本轮允许的范围，请缩小问题或账单范围后重新检查。',
    MODEL_MONETARY_ADMISSION_REQUIRED: 'AI 分析暂不可用：模型服务或本轮费用准入尚未就绪。请检查服务、新审批和执行计划确认。',
    MODEL_EXECUTION_CONFIRMATION_REQUIRED: '本轮执行计划尚未完成独立确认，暂不能调用模型。',
    MODEL_APPROVAL_REQUIRED: '本轮模型审批或价格策略缺失、已过期或与当前任务范围不匹配，请核对后重新检查。',
    MODEL_APPROVAL_LIMIT_EXCEEDED: '本轮模型请求次数或并发额度已用尽，请先核查已有请求与占额。',
    MODEL_BUDGET_UNAVAILABLE: '模型费用服务暂不可用，请先恢复服务并重新检查准入状态。',
    MODEL_SPEND_QUOTA_EXCEEDED: '模型预算不足，暂不能开始新的分析。',
    MODEL_USER_MONTHLY_QUOTA_EXCEEDED: '本月模型费用额度不足，暂不能开始新的分析。',
    MODEL_OUTCOME_UNKNOWN: '模型请求结果尚未确认。请保留任务和费用记录，不要重复发起，先核查原请求结果。',
    TOOL_OUTCOME_UNKNOWN: '上一次工具或模型调用结果尚未确认。不要重复执行，请先查询原任务与费用记录。',
    MODEL_REQUEST_CONFLICT: '该任务已绑定已有模型请求。请先核查原请求状态，不要重复发起。',
  }
  return messages[code] || 'AI 分析暂未就绪，请重新检查服务、审批范围和当前任务状态。'
}
