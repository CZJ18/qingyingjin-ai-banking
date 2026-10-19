import type { components } from './agent-schema'
import { ApiError, authorizationHeaders, request, sessionVersion } from './client'
import type { MonthlyGoal } from '../lib/monthly-goal'

export type AgentSchema<K extends keyof components['schemas']> = components['schemas'][K]
export type AgentConfirmation = Omit<AgentSchema<'PreparationChallenge'>, 'confirmation_scope'> & {
  confirmation_scope: 'PREPARE_ONLY' | 'PLAN_ACCEPTANCE'
}
export type AgentTask = Omit<AgentSchema<'AgentTaskView'>, 'confirmation' | 'request'> & {
  confirmation: AgentConfirmation | null
  request: AgentSchema<'AgentSubmit'> & { monthly_goal?: MonthlyGoal | null }
}
export type AgentSubmit = Omit<
  AgentSchema<'AgentSubmit'>,
  'instruction' | 'max_attempts' | 'timeout_ms' | 'tasks'
> &
  Partial<Pick<AgentSchema<'AgentSubmit'>, 'instruction' | 'max_attempts' | 'timeout_ms'>> & {
    tasks?: AgentTaskSpec[]
    monthly_goal?: MonthlyGoal
  }
export type AgentTaskSpec = Omit<
  AgentSchema<'AgentTaskSpec'>,
  'priority' | 'use_coze' | 'blocked'
> &
  Partial<Pick<AgentSchema<'AgentTaskSpec'>, 'priority' | 'use_coze' | 'blocked'>>
export type AgentResume = Omit<AgentSchema<'AgentResume'>, 'decision'> & {
  decision: 'approve' | 'reject'
}
export type ProjectionKind =
  | 'account'
  | 'transaction'
  | 'subscription'
  | 'budget'
  | 'lifestyle_goal'
  | 'lifestyle_plan'
  | 'operation'
// The events route uses Envelope in OpenAPI. Fields below match TaskRepository.events SELECT.
export interface AgentTaskEvent {
  seq: number
  status: AgentTask['status']
  node_key: string
  error_code: string
  trace_id: string
  created_at: string
}
const statuses = new Set([
  'queued',
  'running',
  'interrupted',
  'waiting_confirmation',
  'succeeded',
  'failed',
  'cancelled',
])
const stopped = new Set(['interrupted', 'waiting_confirmation', 'succeeded', 'failed', 'cancelled'])
const taskPattern = /^lg-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
const base = '/product/api/v1'
function taskPath(id: string) {
  if (!taskPattern.test(id))
    throw new ApiError('INVALID_TASK_ID', '请选择 LangGraph 任务；旧版任务仅供查询')
  return `${base}/agent-tasks/${id}`
}
function integer(value: number, minimum: number) {
  if (!Number.isSafeInteger(value) || value < minimum)
    throw new ApiError('INVALID_ARGUMENT', '版本和事件序号必须是可安全表示的整数')
  return value
}
function invalidResponse(): never {
  throw new ApiError('INVALID_RESPONSE', '任务服务响应不符合接口契约')
}
function validateTask(value: AgentTask, id?: string): AgentTask {
  if (
    !value ||
    !taskPattern.test(value.task_id) ||
    (id && value.task_id !== id) ||
    value.owner_engine !== 'langgraph' ||
    !statuses.has(value.status) ||
    !Number.isSafeInteger(value.version) ||
    value.version < 1 ||
    (value.financial_execution_enabled !== undefined && value.financial_execution_enabled !== false)
  )
    invalidResponse()
  const c = value.confirmation
  if (
    c &&
    (c.status !== 'pending' ||
      !/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(c.node_key) ||
      !/^[0-9a-f]{64}$/.test(c.digest) ||
      typeof c.nonce !== 'string' ||
      c.nonce.length < 16 ||
      !Number.isFinite(Date.parse(c.expires_at)) ||
      (c.confirmation_scope !== undefined &&
        !['PREPARE_ONLY', 'PLAN_ACCEPTANCE'].includes(c.confirmation_scope)))
  )
    invalidResponse()
  // FastAPI's actual endpoint omits the schema defaults; apply only the documented literals.
  return {
    ...value,
    financial_execution_enabled: false,
    confirmation: c ? { ...c, confirmation_scope: c.confirmation_scope ?? 'PREPARE_ONLY' } : null,
  }
}
function validateEvent(value: AgentTaskEvent): AgentTaskEvent {
  if (
    !value ||
    !Number.isSafeInteger(value.seq) ||
    value.seq < 1 ||
    !statuses.has(value.status) ||
    !['node_key', 'error_code', 'trace_id', 'created_at'].every(
      (k) => typeof value[k as keyof AgentTaskEvent] === 'string',
    )
  )
    invalidResponse()
  return value
}
export function requestProduct<T>(path: string, options: Parameters<typeof request>[1] = {}) {
  if (!path.startsWith('/api/v1/'))
    return Promise.reject(new ApiError('INVALID_REQUEST_PATH', '产品接口地址无效'))
  return request<T>('/product' + path, options)
}
async function taskRequest(path: string, options: Parameters<typeof request>[1] = {}, id?: string) {
  return validateTask(await request<AgentTask>(path, options), id)
}
export const agentApi = {
  submit: (body: AgentSubmit, key: string) =>
    taskRequest(`${base}/agent-tasks`, { method: 'POST', body, key }),
  task: async (id: string, signal?: AbortSignal) => taskRequest(taskPath(id), { signal }, id),
  run: async (id: string, expectedVersion: number) =>
    taskRequest(
      taskPath(id) + '/run',
      {
        method: 'POST',
        body: { expected_version: integer(expectedVersion, 1) },
        timeoutMs: 200000,
      },
      id,
    ),
  resume: async (id: string, body: AgentResume) =>
    taskRequest(
      taskPath(id) + '/resume',
      {
        method: 'POST',
        timeoutMs: 200000,
        body: {
          expected_version: integer(body.expected_version, 1),
          node_key: body.node_key,
          digest: body.digest,
          nonce: body.nonce,
          decision: body.decision,
        },
      },
      id,
    ),
  cancel: async (id: string, expectedVersion: number) =>
    taskRequest(
      taskPath(id) + '/cancel',
      {
        method: 'POST',
        body: { expected_version: integer(expectedVersion, 1) },
      },
      id,
    ),
  events: async (id: string, after = 0, signal?: AbortSignal) => {
    const result = await request<AgentTaskEvent[]>(
      taskPath(id) + `/events?after=${integer(after, 0)}&limit=200`,
      { signal },
    )
    if (!Array.isArray(result)) invalidResponse()
    return result.map(validateEvent)
  },
  identity: (signal?: AbortSignal) =>
    requestProduct<{
      user_id: string
      coze_user_id: string | null
      identity_provider: string
      financial_execution_enabled: false
    }>('/api/v1/me', { signal }),
  profile: (signal?: AbortSignal) =>
    requestProduct<AgentSchema<'ProfileView'>>('/api/v1/profile', { signal }),
  saveProfile: (body: AgentSchema<'ProfileInput'>) =>
    requestProduct<AgentSchema<'ProfileView'>>('/api/v1/profile', { method: 'PUT', body }),
  projections: (kind: ProjectionKind, signal?: AbortSignal) =>
    requestProduct<AgentSchema<'ProjectionView'>[]>(`/api/v1/projections/${kind}?limit=100`, {
      signal,
    }),
  projection: (kind: ProjectionKind, id: string, signal?: AbortSignal) =>
    requestProduct<AgentSchema<'ProjectionView'>>(
      `/api/v1/projections/${kind}/${encodeURIComponent(id)}`,
      { signal },
    ),
  refresh: (body: AgentSchema<'RefreshInput'>, key: string) =>
    requestProduct<AgentSchema<'RefreshView'>>('/api/v1/projections/refresh', {
      method: 'POST',
      body,
      key,
      timeoutMs: 90000,
    }),
  recommendations: (signal?: AbortSignal) =>
    requestProduct<AgentSchema<'ProjectionView'>[]>('/api/v1/recommendations?limit=100', {
      signal,
    }),
  evidence: (id: string, signal?: AbortSignal) =>
    requestProduct<AgentSchema<'EvidenceView'>>(
      `/api/v1/recommendations/${encodeURIComponent(id)}/evidence`,
      { signal },
    ),
}

function waitForReconnect(signal: AbortSignal, ms: number) {
  return new Promise<void>((resolve) => {
    const finish = () => {
      clearTimeout(timer)
      signal.removeEventListener('abort', finish)
      resolve()
    }
    const timer = setTimeout(finish, ms)
    signal.addEventListener('abort', finish, { once: true })
    if (signal.aborted) finish()
  })
}

// Native EventSource cannot set Bearer. Only durable event sequence travels in the URL.
export async function streamAgentTask(
  id: string,
  signal: AbortSignal,
  onSnapshot: (task: AgentTask) => void,
  onEvent: (event: AgentTaskEvent) => void,
  onState: (state: string) => void,
) {
  const path = taskPath(id),
    epoch = sessionVersion()
  let after = 0,
    backoff = 1000
  while (!signal.aborted && sessionVersion() === epoch) {
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
    try {
      onState('连接中')
      const current = await agentApi.task(id, signal)
      if (signal.aborted || sessionVersion() !== epoch) return
      onSnapshot(current)
      if (stopped.has(current.status)) {
        onState('任务已暂停或结束')
        return
      }
      const response = await fetch(path + `/events/stream?after=${after}`, {
        headers: { ...authorizationHeaders(), Accept: 'text/event-stream' },
        signal,
        redirect: 'error',
        credentials: 'omit',
        cache: 'no-store',
      })
      if (signal.aborted || sessionVersion() !== epoch) return
      if (!response.ok) throw new ApiError('STREAM_ERROR', '任务事件连接失败', '', response.status)
      if (!response.headers.get('content-type')?.startsWith('text/event-stream')) invalidResponse()
      reader = response.body?.getReader()
      if (!reader) invalidResponse()
      onState('实时连接')
      backoff = 1000
      const decoder = new TextDecoder()
      let buffer = ''
      while (!signal.aborted && sessionVersion() === epoch) {
        const { value, done } = await reader.read()
        if (signal.aborted || sessionVersion() !== epoch) return
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        buffer = buffer.replace(/\r\n/g, '\n')
        if (buffer.length > 65536) invalidResponse()
        let index: number
        while ((index = buffer.indexOf('\n\n')) >= 0) {
          const frame = buffer.slice(0, index)
          buffer = buffer.slice(index + 2)
          const lines = frame.split('\n')
          const kind = lines
            .find((v) => v.startsWith('event:'))
            ?.slice(6)
            .trim()
          if (kind === 'auth_expired') {
            onState('认证已失效 · 请重新登录')
            return
          }
          const data = lines
            .filter((v) => v.startsWith('data:'))
            .map((v) => v.slice(5).trimStart())
            .join('\n')
          if (!data || kind !== 'task') continue
          let parsed: AgentTaskEvent
          try {
            parsed = JSON.parse(data)
          } catch {
            invalidResponse()
          }
          const event = validateEvent(parsed)
          const seq = lines
            .find((v) => v.startsWith('id:'))
            ?.slice(3)
            .trim()
          if (seq !== String(event.seq)) invalidResponse()
          if (event.seq <= after) continue
          onEvent(event)
          after = event.seq
        }
      }
    } catch (error) {
      if (signal.aborted || sessionVersion() !== epoch) return
      if (
        error instanceof ApiError &&
        ([401, 403, 404].includes(error.status) || error.code === 'INVALID_RESPONSE')
      ) {
        onState('连接失败 · 请重新认证或检查任务')
        return
      }
      onState('连接中断 · 正在恢复')
    } finally {
      await reader?.cancel().catch(() => {})
    }
    if (signal.aborted || sessionVersion() !== epoch) return
    await waitForReconnect(signal, backoff)
    backoff = Math.min(backoff * 2, 10000)
  }
}
