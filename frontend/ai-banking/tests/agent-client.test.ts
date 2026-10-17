import { afterEach, describe, expect, it, vi } from 'vitest'
import { setToken } from '../src/api/client'
import { agentApi, streamAgentTask } from '../src/api/agent-client'

const id = 'lg-12345678-1234-4234-9234-123456789abc'
const task = (status = 'queued', version = 1) => ({
  task_id: id,
  owner_engine: 'langgraph',
  status,
  version,
  request: { tasks: [], instruction: '分析账单', bindings: {}, max_attempts: 2, timeout_ms: 90000 },
  result: null,
  error_code: '',
  confirmation: null,
  created_at: '2026-10-14T00:00:00Z',
  updated_at: '2026-10-14T00:00:00Z',
})
const reply = (data: unknown, status = 200) =>
  new Response(JSON.stringify({ code: 'OK', data }), { status })
afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  setToken('')
})

describe('LangGraph task contract', () => {
  it('submits once with a stable key to the product service and never starts execution implicitly', async () => {
    const fetch = vi.fn().mockResolvedValue(reply(task(), 202))
    vi.stubGlobal('fetch', fetch)
    setToken('memory-only-user')
    const result = await agentApi.submit({ instruction: '分析账单' }, 'stable-task-key')
    expect(result.task_id).toBe(id)
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(fetch.mock.calls[0][0]).toBe('/product/api/v1/agent-tasks')
    expect(fetch.mock.calls[0][1].headers).toMatchObject({
      Authorization: 'Bearer memory-only-user',
      'Idempotency-Key': 'stable-task-key',
    })
  })
  it('binds a PREPARE decision to version, node, digest and nonce without calling the funds gateway', async () => {
    const fetch = vi.fn().mockResolvedValue(reply(task('succeeded', 4)))
    vi.stubGlobal('fetch', fetch)
    const decision = {
      expected_version: 3,
      node_key: 'prepare',
      digest: 'a'.repeat(64),
      nonce: 'n'.repeat(20),
      decision: 'approve' as const,
    }
    await agentApi.resume(id, decision)
    expect(fetch.mock.calls[0][0]).toBe(`/product/api/v1/agent-tasks/${id}/resume`)
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual(decision)
    expect(fetch).toHaveBeenCalledTimes(1)
  })
  it('rejects Go job identifiers and mismatched engine responses', async () => {
    const fetch = vi.fn().mockResolvedValue(reply({ ...task(), owner_engine: 'go' }))
    vi.stubGlobal('fetch', fetch)
    await expect(agentApi.task('12345678-1234-4234-9234-123456789abc')).rejects.toMatchObject({
      code: 'INVALID_TASK_ID',
    })
    expect(fetch).not.toHaveBeenCalled()
    await expect(agentApi.task(id)).rejects.toMatchObject({ code: 'INVALID_RESPONSE' })
  })
  it('uses optimistic task versions for run and cancellation', async () => {
    const fetch = vi.fn().mockImplementation(() => Promise.resolve(reply(task('cancelled', 5))))
    vi.stubGlobal('fetch', fetch)
    await agentApi.run(id, 3)
    await agentApi.cancel(id, 4)
    expect(fetch.mock.calls.map(([url]) => url)).toEqual([
      `/product/api/v1/agent-tasks/${id}/run`,
      `/product/api/v1/agent-tasks/${id}/cancel`,
    ])
    expect(fetch.mock.calls.map(([, init]) => JSON.parse(init.body))).toEqual([
      { expected_version: 3 },
      { expected_version: 4 },
    ])
  })
  it('preserves profile revisions and integer safety reserves in the product service', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        reply({
          user_id: '7',
          currency: 'CNY',
          timezone: 'Asia/Shanghai',
          safety_reserve_minor: 20000,
          revision: 4,
        }),
      )
    vi.stubGlobal('fetch', fetch)
    const body = { expected_version: 3, timezone: 'Asia/Shanghai', safety_reserve_minor: 20000 }
    const profile = await agentApi.saveProfile(body)
    expect(profile.revision).toBe(4)
    expect(fetch.mock.calls[0][0]).toBe('/product/api/v1/profile')
    expect(fetch.mock.calls[0][1].method).toBe('PUT')
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual(body)
  })
  it('rejects unsafe task versions before any request reaches the backend', async () => {
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)
    await expect(agentApi.run(id, Number.MAX_SAFE_INTEGER + 1)).rejects.toMatchObject({
      code: 'INVALID_ARGUMENT',
    })
    expect(fetch).not.toHaveBeenCalled()
  })
})

describe('Owner authenticated LangGraph SSE', () => {
  it('stops an invalid JSON tool event without repeatedly reconnecting', async () => {
    vi.useFakeTimers()
    const abort = new AbortController(),
      states: string[] = []
    const fetch = vi.fn(async (url: string) =>
      url.includes('/events/stream')
        ? new Response('id: 1\nevent: task\ndata: {bad json}\n\n', {
            headers: { 'Content-Type': 'text/event-stream' },
          })
        : reply(task('running')),
    )
    vi.stubGlobal('fetch', fetch)
    const stream = streamAgentTask(
      id,
      abort.signal,
      () => {},
      () => {},
      (s) => states.push(s),
    )
    await vi.advanceTimersByTimeAsync(1500)
    abort.abort()
    await stream
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(states.at(-1)).toContain('连接失败')
  })
  it('resumes with the durable after cursor, handles split CRLF and recovers snapshots', async () => {
    vi.useFakeTimers()
    setToken('stream-user')
    const abort = new AbortController(),
      seen: unknown[] = [],
      snapshots: unknown[] = []
    let streams = 0
    const encoder = new TextEncoder()
    const fetch = vi.fn(async (url: string, init: RequestInit) => {
      expect(url).not.toContain('stream-user')
      if (!url.includes('/events/stream')) return reply(task('running'))
      expect((init.headers as Record<string, string>).Authorization).toBe('Bearer stream-user')
      if (++streams === 2) {
        expect(url).toBe(`/product/api/v1/agent-tasks/${id}/events/stream?after=7`)
        abort.abort()
        return new Response('')
      }
      return new Response(
        new ReadableStream({
          start(c) {
            c.enqueue(encoder.encode('id: 7\r'))
            c.enqueue(
              encoder.encode(
                '\nevent: task\r\ndata: {"seq":7,"status":"running","node_key":"billing","error_code":"","trace_id":"trace","created_at":"2026-10-14T00:00:00Z"}\r\n\r\n',
              ),
            )
            c.close()
          },
        }),
        { headers: { 'Content-Type': 'text/event-stream' } },
      )
    })
    vi.stubGlobal('fetch', fetch)
    const stream = streamAgentTask(
      id,
      abort.signal,
      (v) => snapshots.push(v),
      (v) => seen.push(v),
      () => {},
    )
    await vi.advanceTimersByTimeAsync(1001)
    await stream
    expect(snapshots).toHaveLength(2)
    expect(seen).toHaveLength(1)
    expect(seen[0]).toMatchObject({ seq: 7, node_key: 'billing' })
  })
  it('stops reconnecting when the server reports an expired user identity', async () => {
    const states: string[] = [],
      fetch = vi.fn(async (url: string) =>
        url.includes('/events/stream')
          ? new Response('event: auth_expired\ndata: {}\n\n', {
              headers: { 'Content-Type': 'text/event-stream' },
            })
          : reply(task('running')),
      )
    vi.stubGlobal('fetch', fetch)
    await streamAgentTask(
      id,
      new AbortController().signal,
      () => {},
      () => {},
      (s) => states.push(s),
    )
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(states.at(-1)).toContain('认证已失效')
  })
  it('does not retain a task challenge or reconnect into a newly authenticated user', async () => {
    const fetch = vi.fn(async () => {
      setToken('another-user')
      return reply(task('running'))
    })
    vi.stubGlobal('fetch', fetch)
    setToken('original-user')
    const snapshots: unknown[] = []
    await streamAgentTask(
      id,
      new AbortController().signal,
      (v) => snapshots.push(v),
      () => {},
      () => {},
    )
    expect(snapshots).toEqual([])
    expect(fetch).toHaveBeenCalledTimes(1)
  })
})
