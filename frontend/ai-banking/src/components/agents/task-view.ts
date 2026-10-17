/** Presentation of observed task data. Missing execution facts stay missing. */
import { minor } from '../../lib/utils'
export type ResultRecord = Record<string, unknown>
export function record(value: unknown): ResultRecord {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as ResultRecord) : {}
}
const text = (value: unknown) => (typeof value === 'string' ? value : '')
export type TaskRow = {
  task_id: string
  key: string
  agent_type: string
  tool: string
  skill_id: string
  skill_version?: number
  execution_mode: string
  status: string
  parameters: ResultRecord
  output: ResultRecord
  error_code: string
  dependencies?: string[]
  dependencies_known?: boolean
  attempts?: number
  duration_ms?: number
}
export type TaskEvent = {
  id: string
  event: string
  status: string
  task_id: string
  created_at: number
  trace_id?: string
  error_code?: string
}
export type TaskTrace = {
  invocations?: Array<{
    id: string
    tool: string
    status: string
    duration_ms?: number
    error_code?: string
  }>
  events?: TaskEvent[]
}
export function agentRows(raw: unknown, rawEvents: unknown): TaskRow[] {
  const task = record(raw),
    request = record(task.request)
  const specs = Array.isArray(request.tasks) ? request.tasks.map(record) : []
  const outputs = Array.isArray(record(task.result).outputs)
    ? (record(task.result).outputs as unknown[])
    : []
  const taskId = text(task.task_id)
  const rows = new Map<string, TaskRow>()
  for (const spec of specs) {
    const key = text(spec.key)
    rows.set(key, {
      task_id: `${taskId}.${key}`,
      key,
      agent_type: text(spec.agent_type),
      tool: text(spec.tool),
      skill_id: text(spec.skill_id),
      skill_version: typeof spec.skill_version === 'number' ? spec.skill_version : undefined,
      execution_mode: text(spec.execution_mode),
      status: spec.blocked ? 'blocked' : 'pending',
      parameters: record(spec.parameters),
      output: {},
      error_code: '',
      dependencies: Array.isArray(spec.dependencies)
        ? spec.dependencies.filter((v): v is string => typeof v === 'string')
        : [],
      dependencies_known: true,
    })
  }
  for (const rawOutput of outputs) {
    const output = record(rawOutput),
      id = text(output.task_id)
    if (
      !taskId ||
      !id.startsWith(`${taskId}.`) ||
      !/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(id.slice(taskId.length + 1))
    )
      continue
    const key = id.slice(taskId.length + 1),
      previous = rows.get(key)
    rows.set(key, {
      task_id: id,
      key,
      agent_type: text(output.agent_type),
      tool: previous?.tool || text(record(output.result).tool),
      skill_id: text(output.skill_id),
      skill_version: typeof output.skill_version === 'number' ? output.skill_version : undefined,
      execution_mode: previous?.execution_mode || '',
      status: text(output.status) || 'pending',
      parameters: previous?.parameters || {},
      output,
      error_code: '',
      dependencies: previous?.dependencies,
      dependencies_known: previous?.dependencies_known || false,
      duration_ms: typeof output.duration_ms === 'number' ? output.duration_ms : undefined,
    })
  }
  if (Array.isArray(rawEvents)) {
    for (const rawEvent of rawEvents) {
      const event = record(rawEvent),
        key = text(event.node_key),
        existing = rows.get(key)
      // Events describe the root task state, not individual node completion.
      if (existing && text(event.error_code)) existing.error_code = text(event.error_code)
    }
  }
  const challenge = record(task.confirmation),
    key = text(challenge.node_key)
  if (key && task.status === 'waiting_confirmation') {
    const existing = rows.get(key),
      spec = record(challenge.parameters_view)
    rows.set(key, {
      task_id: `${taskId}.${key}`,
      key,
      agent_type: existing?.agent_type || text(spec.agent_type),
      tool: existing?.tool || text(spec.tool),
      skill_id: existing?.skill_id || text(spec.skill_id),
      execution_mode: existing?.execution_mode || text(spec.execution_mode),
      status: 'waiting_confirmation',
      parameters: existing?.parameters || record(spec.parameters),
      output: existing?.output || {},
      error_code: '',
      dependencies: existing?.dependencies,
      dependencies_known: !!existing?.dependencies_known,
    })
  }
  return [...rows.values()]
}
export function taskControls(status: string) {
  return {
    run: ['queued', 'failed', 'interrupted'].includes(status),
    cancel: !!status && !['succeeded', 'cancelled'].includes(status),
  }
}
export function preparationCurrent(raw: unknown, now: number): boolean {
  const task = record(raw),
    challenge = record(task.confirmation)
  return (
    task.status === 'waiting_confirmation' &&
    challenge.confirmation_scope === 'PREPARE_ONLY' &&
    /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(text(challenge.node_key)) &&
    /^[0-9a-f]{64}$/.test(text(challenge.digest)) &&
    text(challenge.nonce).length >= 16 &&
    Date.parse(text(challenge.expires_at)) > now
  )
}
export function transferPreparation(
  source: string,
  target: string,
  amount: string,
  description: string,
) {
  if (!/^[1-9][0-9]*$/.test(source) || !/^[1-9][0-9]*$/.test(target) || source === target)
    throw new Error('请选择两个不同的模拟账户')
  const cents = minor(amount)
  if (cents <= 0) throw new Error('转账金额必须大于零')
  return {
    tasks: [
      {
        key: 'prepare_transfer',
        agent_type: 'supervisor',
        tool: 'require_gateway',
        skill_id: 'operation_preparation',
        skill_version: 1,
        execution_mode: 'intent',
        dependencies: [],
        required_permissions: ['READ', 'PREPARE'],
        parameters: {
          kind: 'transfer',
          parameters: {
            source_account_id: source,
            target_account_id: target,
            amount_minor: cents,
            currency: 'CNY',
            description: description.trim() || '用户提出的模拟转账准备',
          },
        },
      },
    ],
  }
}
export function preparedOperationId(raw: unknown): string {
  const output = record(raw),
    operation = record(output.result),
    id = text(operation.id)
  return output.skill_id === 'operation_preparation' &&
    output.financial_execution_enabled === false &&
    text(operation.kind) &&
    ['pending_authorization', 'authorized', 'succeeded', 'failed', 'expired', 'rejected'].includes(
      text(operation.status),
    ) &&
    /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(id)
    ? id
    : ''
}
export function agentTrace(rawEvents: unknown): TaskTrace {
  const events = Array.isArray(rawEvents) ? rawEvents.map(record) : []
  return {
    events: events.map((event) => ({
      id: String(event.seq),
      event: text(event.node_key) ? `${text(event.node_key)} · 任务状态变更` : '任务状态变更',
      status: text(event.status),
      task_id: text(event.node_key),
      created_at: Date.parse(text(event.created_at)),
      trace_id: text(event.trace_id),
      error_code: text(event.error_code),
    })),
  }
}
export const roleNames: Record<string, string> = {
  supervisor: '任务统筹',
  billing: '账单分析',
  subscription: '订阅管理',
  lifestyle: '生活目标',
  savings: '资金规划',
}
export const modeNames: Record<string, string> = {
  read_only: '只读分析',
  advice: '生成建议',
  intent: '准备操作意向',
  user_confirmation: '确认任务建议',
}
