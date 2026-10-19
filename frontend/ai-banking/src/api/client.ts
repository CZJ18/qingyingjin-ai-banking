import type { paths, components } from './schema'
export type Schema<K extends keyof components['schemas']> = components['schemas'][K]
let token = ''
let tokenVersion = 0
export function setToken(value: string) {
  const next = value.trim()
  if (next !== token) tokenVersion++
  token = next
}
// Only the store may renew a session after checking the returned user identity.
// Preserving the epoch lets existing requests and SSE streams finish normally.
export function renewToken(value: string) {
  const next = value.trim()
  if (!token || !next) throw new ApiError('SESSION_CHANGED', '登录状态已变化，请重新登录')
  token = next
}
// Credentials remain in this module; streaming callers only use transient headers.
export const sessionVersion = () => tokenVersion
export const authorizationHeaders = (): Record<string, string> =>
  token ? { Authorization: `Bearer ${token}` } : {}
export class ApiError extends Error {
  constructor(
    public code: string,
    message: string,
    public requestId = '',
    public status = 0,
  ) {
    super(message)
  }
}
export async function request<T>(
  path: string,
  options: {
    method?: string
    body?: unknown
    signal?: AbortSignal
    key?: string
    timeoutMs?: number
  } = {},
): Promise<T> {
  if (
    !/^\/(?:api\/v[12]\/|product\/(?:api\/v1\/|healthz(?:\?|$)|readyz(?:\?|$))|healthz(?:\?|$)|readyz(?:\?|$))/.test(
      path,
    ) ||
    path.includes('\\')
  )
    throw new ApiError('INVALID_REQUEST_PATH', '请求地址必须是本应用的业务接口')
  const epoch = sessionVersion()
  const controller = new AbortController(),
    timer = setTimeout(
      () => controller.abort(new Error('请求超时，请查询服务器状态后重试')),
      Math.min(210000, Math.max(1000, options.timeoutMs ?? 20000)),
    )
  const abort = () => controller.abort(options.signal?.reason)
  options.signal?.addEventListener('abort', abort, { once: true })
  if (options.signal?.aborted) abort()
  try {
    const response = await fetch(path, {
      method: options.method ?? 'GET',
      redirect: 'error',
      credentials: 'omit',
      cache: 'no-store',
      signal: controller.signal,
      headers: {
        Accept: 'application/json',
        ...(options.body !== undefined ? { 'Content-Type': 'application/json' } : {}),
        ...authorizationHeaders(),
        ...(options.key ? { 'Idempotency-Key': options.key } : {}),
      },
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
    })
    if (epoch !== sessionVersion())
      throw new ApiError('SESSION_CHANGED', '账户连接已变化，已丢弃此前会话的响应')
    const envelope = await response.json().catch(() => {
      throw new ApiError(
        response.status >= 500 ? 'DEPENDENCY_UNAVAILABLE' : 'INVALID_RESPONSE',
        response.status >= 500
          ? 'Banking Service 暂不可用，请检查服务连接'
          : '服务返回了无法识别的响应',
        '',
        response.status,
      )
    })
    if (epoch !== sessionVersion())
      throw new ApiError('SESSION_CHANGED', '账户连接已变化，已丢弃此前会话的响应')
    if (!response.ok || envelope.code !== 'OK')
      throw new ApiError(envelope.code, envelope.message, envelope.request_id, response.status)
    return envelope.data as T
  } catch (e) {
    if (e instanceof ApiError) throw e
    if (options.signal?.aborted) throw e
    throw new ApiError(
      controller.signal.aborted ? 'TIMEOUT' : 'NETWORK_ERROR',
      controller.signal.aborted
        ? '请求超时，操作结果请重新查询'
        : '无法连接 Banking Service，请检查服务是否启动',
    )
  } finally {
    clearTimeout(timer)
    options.signal?.removeEventListener('abort', abort)
  }
}
type PostPath = { [P in keyof paths]: paths[P] extends { post: unknown } ? P : never }[keyof paths]
type Body<P extends PostPath> = paths[P] extends {
  post: { requestBody?: { content: { 'application/json': infer B } } }
}
  ? B
  : never
export function post<P extends PostPath, T>(path: P, body: Body<P>, key?: string) {
  return request<T>(path, { method: 'POST', body, key })
}
export const api = {
  lifestyle: {
    parse: (body: Schema<'LifestyleParseInput'>) =>
      request<Schema<'LifestyleParseResult'>>('/api/v2/lifestyle-goals/parse', {
        method: 'POST',
        body,
        timeoutMs: 180000,
      }),
    create: (body: Schema<'LifestyleGoalInput'>, key: string) =>
      request<Schema<'LifestyleGoalView'>>('/api/v2/lifestyle-goals', {
        method: 'POST',
        body,
        key,
      }),
    update: (id: string, body: Schema<'LifestyleGoalInput'>, key: string) =>
      request<Schema<'LifestyleGoalView'>>(`/api/v2/lifestyle-goals/${encodeURIComponent(id)}`, {
        method: 'PUT',
        body,
        key,
      }),
    goal: (id: string, signal?: AbortSignal) =>
      request<Schema<'LifestyleGoalView'>>(
        `/api/v2/lifestyle-goals/${encodeURIComponent(id)}?limit=100&offset=0`,
        { signal },
      ),
    compile: (id: string, body: Schema<'LifestyleCompileInput'>, key: string) =>
      request<Schema<'LifestylePlanView'>>(
        `/api/v2/lifestyle-goals/${encodeURIComponent(id)}/compile`,
        { method: 'POST', body, key },
      ),
    plans: (id: string, signal?: AbortSignal) =>
      request<Schema<'LifestylePlanView'>[]>(
        `/api/v2/lifestyle-goals/${encodeURIComponent(id)}/plans?limit=100&offset=0`,
        { signal },
      ),
    plan: (id: string, signal?: AbortSignal) =>
      request<Schema<'LifestylePlanView'>>(`/api/v2/lifestyle-plans/${encodeURIComponent(id)}`, {
        signal,
      }),
    decide: (id: string, body: Schema<'LifestyleDecisionInput'>, key: string) =>
      request<Schema<'LifestyleDecision'>>(
        `/api/v2/recommendations/${encodeURIComponent(id)}/decisions`,
        { method: 'POST', body, key },
      ),
  },
  accounts: (signal?: AbortSignal) =>
    request<Schema<'BankAccount'>[]>('/api/v1/accounts?limit=100', { signal }),
  savings: (signal?: AbortSignal) =>
    request<Schema<'SavingsGoal'>[]>('/api/v1/savings/goals?limit=100', { signal }),
  subscriptions: async (signal?: AbortSignal) =>
    (
      await request<Schema<'SubscriptionReport'>>('/api/v1/subscriptions', { signal })
    ).subscriptions.map((item) => item.subscription),
  detectSubscriptions: (signal?: AbortSignal) =>
    request<Schema<'SubscriptionReport'>>('/api/v1/subscriptions/detect_subscriptions', {
      method: 'POST', body: {}, signal,
    }),
  operations: (signal?: AbortSignal) =>
    request<Schema<'ExecutionOperation'>[]>('/api/v1/execution/operations', { signal }),
  billing: (body: Omit<Schema<'BillingInput'>, 'limit' | 'offset'>) =>
    post<'/api/v1/billing/generate_billing_report', Schema<'BillingReport'>>(
      '/api/v1/billing/generate_billing_report',
      { limit: 100, offset: 0, ...body },
    ),
  prepare: (body: Schema<'ExecutionPrepareInput'>, key: string) =>
    post<'/api/v1/execution/operations', Schema<'ExecutionView'>>(
      '/api/v1/execution/operations',
      body,
      key,
    ),
  job: (id: string, signal?: AbortSignal) =>
    request<Schema<'OrchestrationView'>>(`/api/v1/orchestrations/${encodeURIComponent(id)}`, {
      signal,
    }),
}
// Authenticated fetch streaming: native EventSource cannot supply a Bearer header.
export async function streamJob(
  id: string,
  signal: AbortSignal,
  onEvent: (event: unknown) => void,
  onState: (state: string) => void,
) {
  const epoch = sessionVersion()
  let cursor = '0',
    backoff = 1000
  while (!signal.aborted && epoch === sessionVersion()) {
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
    try {
      onState('连接中')
      // Recover the canonical snapshot on every reconnect before consuming later events.
      const snapshot = await api.job(id, signal)
      if (signal.aborted || epoch !== sessionVersion()) return
      onEvent(snapshot)
      const response = await fetch(`/api/v1/orchestrations/${encodeURIComponent(id)}/events`, {
        headers: {
          ...authorizationHeaders(),
          'Last-Event-ID': cursor,
          Accept: 'text/event-stream',
        },
        signal,
        redirect: 'error',
        credentials: 'omit',
        cache: 'no-store',
      })
      if (signal.aborted || epoch !== sessionVersion()) return
      if (!response.ok) {
        throw new ApiError('STREAM_ERROR', '任务事件连接失败', '', response.status)
      }
      reader = response.body?.getReader()
      if (!reader) throw new Error('No stream')
      onState('实时连接')
      backoff = 1000
      let buffer = '',
        decoder = new TextDecoder()
      while (!signal.aborted && epoch === sessionVersion()) {
        const { value, done } = await reader.read()
        if (signal.aborted || epoch !== sessionVersion()) return
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        buffer = buffer.replace(/\r\n/g, '\n')
        let index: number
        while ((index = buffer.indexOf('\n\n')) >= 0) {
          const frame = buffer.slice(0, index)
          buffer = buffer.slice(index + 2)
          const lines = frame.split('\n')
          const next = lines
            .find((l) => l.startsWith('id:'))
            ?.slice(3)
            .trim()
          const data = lines
            .filter((l) => l.startsWith('data:'))
            .map((l) => l.slice(5).trimStart())
            .join('\n')
          if (data) {
            onEvent(JSON.parse(data))
            if (next) cursor = next
          }
        }
      }
    } catch (e) {
      if (signal.aborted || epoch !== sessionVersion()) return
      onState('连接中断 · 正在恢复')
      if (e instanceof ApiError && [401, 403, 404].includes(e.status)) {
        onState('连接失败 · 请重新认证或检查任务')
        return
      }
    } finally {
      await reader?.cancel().catch(() => {})
    }
    if (signal.aborted || epoch !== sessionVersion()) return
    await new Promise<void>((resolve) => {
      const finish = () => {
        clearTimeout(timer)
        signal.removeEventListener('abort', finish)
        resolve()
      }
      const timer = setTimeout(finish, backoff)
      signal.addEventListener('abort', finish, { once: true })
    })
    backoff = Math.min(backoff * 2, 10000)
  }
}
