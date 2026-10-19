import type { AgentResume, AgentSchema, AgentSubmit } from '../api/agent-client'
import type { TaskRow } from '../components/agents/task-view'

export type MonthlyGoal = { period: string; currency: 'CNY'; timezone: 'Asia/Shanghai' }
type FullTaskSpec = AgentSchema<'AgentTaskSpec'> & {
  dependencies: string[]
  required_permissions: string[]
}
export type MonthlyPlanView = {
  confirmation_scope: 'PLAN_ACCEPTANCE'
  monthly_goal: MonthlyGoal
  goal_text: string
  plan_tasks: FullTaskSpec[]
  suggestions: Record<string, Record<string, unknown>>
  source_digest: string
}
export type MonthlyConfirmationBinding = {
  task_id: string
  version: number
  node_key: string
  digest: string
  nonce: string
  epoch: number
}
const nodePattern = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/
const taskPattern = /^lg-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
const digestPattern = /^[0-9a-f]{64}$/
const object = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value)
const record = (value: unknown): Record<string, unknown> => object(value) ? value : {}
const text = (value: unknown) => typeof value === 'string' ? value : ''
const secrets = new Set([
  'token', 'access_token', 'refresh_token', 'agent_token', 'bearer', 'authorization',
  'authorization_id', 'authorizations', 'confirmation_nonce', 'nonce', 'signature',
  'service_key', 'lease_token', 'api_key', 'secret', 'password', 'x_banking_agent_key',
])
function safeJson(value: unknown, parameters = false, depth = 0): boolean {
  if (depth > 20) return false
  if (Array.isArray(value)) return value.every((v) => safeJson(v, parameters, depth + 1))
  if (object(value)) return Object.entries(value).every(([key, item]) => {
    if (secrets.has(key.toLowerCase().replaceAll('-', '_'))) return false
    if (key.endsWith('_minor') && !Number.isSafeInteger(item) && item !== null &&
      !(parameters && object(item) && Object.keys(item).length === 1 && typeof item.$ref === 'string')) return false
    return safeJson(item, parameters, depth + 1)
  })
  if (typeof value === 'number') return Number.isFinite(value) && (!parameters || Number.isSafeInteger(value))
  return value === null || typeof value === 'string' || typeof value === 'boolean'
}
function isMonthlyGoal(value: unknown): value is MonthlyGoal {
  const goal = record(value)
  return /^\d{4}-(0[1-9]|1[0-2])$/.test(text(goal.period)) &&
    goal.currency === 'CNY' && goal.timezone === 'Asia/Shanghai'
}
export function currentMonthlyGoal(now = new Date()): MonthlyGoal {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit',
  }).formatToParts(now)
  return {
    period: `${parts.find((p) => p.type === 'year')!.value}-${parts.find((p) => p.type === 'month')!.value}`,
    currency: 'CNY', timezone: 'Asia/Shanghai',
  }
}
export function monthlyGoalSubmission(instruction: string, bindings: Record<string, unknown>, now = new Date()): AgentSubmit {
  const monthly_goal = currentMonthlyGoal(now)
  const [year, month] = monthly_goal.period.split('-').map(Number)
  return {
    instruction, monthly_goal,
    bindings: {
      ...bindings,
      from: new Date(Date.UTC(year!, month! - 1, 1, -8)).toISOString(),
      to: new Date(Date.UTC(year!, month!, 1, -8)).toISOString(),
    },
    timeout_ms: 180000,
  }
}
export function monthlyGoalError(error: unknown): string {
  if (record(error).code === 'PLAN_ADVICE_REQUIRED')
    return '当前数据还不足以形成可确认的本月方案，请补充目标或账户信息后重新生成。'
  return error instanceof Error ? error.message : '任务暂时无法完成，请重新查询状态。'
}
function fullSpec(value: unknown): value is FullTaskSpec {
  const spec = record(value)
  const permissions: Record<string, string[]> = {
    read_only: ['READ'], advice: ['READ', 'PLAN'], intent: ['READ', 'PREPARE'], user_confirmation: [],
  }
  return nodePattern.test(text(spec.key)) &&
    ['billing', 'subscription', 'lifestyle', 'savings', 'supervisor'].includes(text(spec.agent_type)) &&
    !!text(spec.tool) && !!text(spec.skill_id) && spec.skill_version === 1 &&
    Object.hasOwn(permissions, text(spec.execution_mode)) && object(spec.parameters) && safeJson(spec.parameters, true) &&
    Array.isArray(spec.dependencies) && spec.dependencies.every((key) => nodePattern.test(text(key))) &&
    spec.dependencies.length === new Set(spec.dependencies).size &&
    Number.isSafeInteger(spec.priority) && Number(spec.priority) >= 0 && Number(spec.priority) <= 1000 &&
    typeof spec.use_coze === 'boolean' && typeof spec.blocked === 'boolean' &&
    JSON.stringify(spec.required_permissions) === JSON.stringify(permissions[text(spec.execution_mode)])
}
export function monthlyPlanView(raw: unknown): MonthlyPlanView | null {
  const task = record(raw), request = record(task.request), challenge = record(task.confirmation)
  const view = record(challenge.parameters_view)
  if (challenge.confirmation_scope !== 'PLAN_ACCEPTANCE' || view.confirmation_scope !== 'PLAN_ACCEPTANCE' ||
    Object.keys(view).sort().join(',') !== 'confirmation_scope,goal_text,monthly_goal,plan_tasks,source_digest,suggestions' ||
    !isMonthlyGoal(view.monthly_goal) || !isMonthlyGoal(request.monthly_goal) ||
    view.monthly_goal.period !== request.monthly_goal.period ||
    typeof view.goal_text !== 'string' || !view.goal_text.trim() || view.goal_text !== request.instruction ||
    !digestPattern.test(text(view.source_digest)) ||
    !Array.isArray(view.plan_tasks) || !view.plan_tasks.length || view.plan_tasks.length > 32 ||
    !view.plan_tasks.every(fullSpec) || !object(view.suggestions) || !safeJson(view.suggestions)) return null
  const specs = new Map(view.plan_tasks.map((spec) => [spec.key, spec]))
  const suggestions = view.suggestions
  if (specs.size !== view.plan_tasks.length || view.plan_tasks.some((spec) =>
    spec.blocked || spec.dependencies.some((key) => !specs.has(key) || key === spec.key)) ||
    !Object.keys(suggestions).length || view.plan_tasks.some((spec) =>
      ['read_only', 'advice'].includes(spec.execution_mode) && !Object.hasOwn(suggestions, spec.key))) return null
  const visited = new Set<string>()
  while (visited.size < specs.size) {
    const ready = view.plan_tasks.filter((spec) => !visited.has(spec.key) && spec.dependencies.every((key) => visited.has(key)))
    if (!ready.length) return null
    ready.forEach((spec) => visited.add(spec.key))
  }
  for (const [key, value] of Object.entries(view.suggestions)) {
    const spec = specs.get(key), output = record(value)
    if (!spec || !['read_only', 'advice'].includes(spec.execution_mode) ||
      output.task_id !== `${text(task.task_id)}.${key}` || output.agent_type !== spec.agent_type ||
      output.skill_id !== spec.skill_id || output.skill_version !== spec.skill_version ||
      output.status !== 'succeeded' || output.financial_execution_enabled !== false ||
      !(object(output.result) || Array.isArray(output.result))) return null
  }
  return view as MonthlyPlanView
}
export function monthlyPlanCurrent(raw: unknown, now: number): boolean {
  const task = record(raw), challenge = record(task.confirmation)
  const view = monthlyPlanView(raw)
  return task.status === 'waiting_confirmation' && task.owner_engine === 'langgraph' &&
    taskPattern.test(text(task.task_id)) && Number.isSafeInteger(task.version) && Number(task.version) >= 1 &&
    task.financial_execution_enabled === false && challenge.status === 'pending' &&
    nodePattern.test(text(challenge.node_key)) && digestPattern.test(text(challenge.digest)) &&
    text(challenge.nonce).length >= 16 && text(challenge.nonce).length <= 128 &&
    Date.parse(text(challenge.expires_at)) > now && Number.isFinite(new Date(now).getTime()) &&
    view !== null && view.monthly_goal.period === currentMonthlyGoal(new Date(now)).period
}
export function monthlyDecision(value: string): 'approve' | 'reject' | null {
  const phrase = value.trim()
  if (['确认方案', '确认这个方案', '确认'].includes(phrase)) return 'approve'
  if (['拒绝方案', '取消方案', '拒绝', '取消'].includes(phrase)) return 'reject'
  return null
}
export function monthlyConfirmationBinding(raw: unknown, epoch: number): MonthlyConfirmationBinding | null {
  const task = record(raw), challenge = record(task.confirmation)
  if (!monthlyPlanView(raw) || !Number.isSafeInteger(task.version)) return null
  return { task_id: text(task.task_id), version: Number(task.version), node_key: text(challenge.node_key),
    digest: text(challenge.digest), nonce: text(challenge.nonce), epoch }
}
export function monthlyResume(raw: unknown, decision: 'approve' | 'reject', binding: unknown,
  epoch: number, now: number): AgentResume | null {
  const current = monthlyConfirmationBinding(raw, epoch), previous = record(binding)
  if (!current || !monthlyPlanCurrent(raw, now) ||
    Object.entries(current).some(([key, value]) => previous[key] !== value)) return null
  return { expected_version: current.version, node_key: current.node_key, digest: current.digest,
    nonce: current.nonce, decision }
}
export function monthlyDialogueResume(raw: unknown, value: string, authenticated: boolean,
  binding: unknown, epoch: number, now: number): AgentResume | null {
  const decision = monthlyDecision(value)
  return authenticated && decision ? monthlyResume(raw, decision, binding, epoch, now) : null
}
export function monthlySuggestionRows(raw: unknown): TaskRow[] {
  const task = record(raw), view = monthlyPlanView(raw)
  if (!view) return []
  return view.plan_tasks.map((spec) => ({
    task_id: `${text(task.task_id)}.${spec.key}`, key: spec.key, agent_type: spec.agent_type,
    tool: spec.tool, skill_id: spec.skill_id, skill_version: spec.skill_version,
    execution_mode: spec.execution_mode, parameters: spec.parameters, output: view.suggestions[spec.key] || {},
    status: spec.key in view.suggestions ? 'succeeded' : spec.blocked ? 'blocked' : 'pending',
    error_code: '', dependencies: spec.dependencies, dependencies_known: true,
  }))
}
