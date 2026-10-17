import { describe, it, expect, vi, afterEach } from 'vitest'
import { request, ApiError, setToken, streamJob, api } from '../src/api/client'
import { minor } from '../src/lib/utils'
afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  setToken('')
})
describe('Financial amounts', () => {
  it('converts decimal strings to exact cents', () => {
    expect(minor('0.29')).toBe(29)
    expect(minor('100.1')).toBe(10010)
  })
  it('rejects negatives, exponent notation, excess precision and unsafe amounts', () => {
    for (const v of ['-1', '1e3', '0.001', '10000000001', 'abc']) expect(() => minor(v)).toThrow()
  })
})
describe('Banking API transport', () => {
  it('discards a body decoded after the authenticated user changes', async () => {
    let complete!: (value: unknown) => void
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        status: 200,
        json: () => new Promise((resolve) => (complete = resolve)),
      })),
    )
    setToken('first-user')
    const pending = request('/api/v1/accounts')
    await Promise.resolve()
    setToken('second-user')
    complete({ code: 'OK', data: [{ user_id: 'first' }] })
    await expect(pending).rejects.toMatchObject({ code: 'SESSION_CHANGED' })
  })
  it('discards responses from an earlier authenticated session', async () => {
    let complete!: (value: Response) => void
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise<Response>((resolve) => (complete = resolve))),
    )
    setToken('user-one')
    const pending = request('/api/v1/accounts')
    setToken('user-two')
    complete(new Response(JSON.stringify({ code: 'OK', data: [{ user_id: 'one' }] })))
    await expect(pending).rejects.toMatchObject({ code: 'SESSION_CHANGED' })
  })
  it('never forwards an authenticated request to an external origin', async () => {
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)
    setToken('user-token')
    await expect(request('https://example.invalid/api/v1/accounts')).rejects.toMatchObject({
      code: 'INVALID_REQUEST_PATH',
    })
    expect(fetch).not.toHaveBeenCalled()
  })
  it('requires the server OK envelope even for HTTP 200', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          new Response(
            JSON.stringify({ code: 'RISK_REJECTED', message: '拒绝', request_id: 'r1' }),
            { status: 200 },
          ),
        ),
    )
    await expect(request('/api/v1/accounts')).rejects.toMatchObject({
      code: 'RISK_REJECTED',
      requestId: 'r1',
    })
  })
  it('sends the same idempotency key and Bearer credential', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify({ code: 'OK', data: { status: 'executed' } })))
    vi.stubGlobal('fetch', fetch)
    setToken('user-token')
    await request('/api/v1/execution/operations/id/execute', {
      method: 'POST',
      body: { expected_version: 2 },
      key: 'stable-operation-key',
    })
    expect(fetch.mock.calls[0]![1].headers).toMatchObject({
      Authorization: 'Bearer user-token',
      'Idempotency-Key': 'stable-operation-key',
    })
  })
  it('preserves cancellation signals', async () => {
    const controller = new AbortController()
    controller.abort()
    vi.stubGlobal(
      'fetch',
      vi.fn((_url, init) => {
        expect(init.signal.aborted).toBe(true)
        return Promise.reject(new DOMException('Aborted', 'AbortError'))
      }),
    )
    await expect(request('/api/v1/accounts', { signal: controller.signal })).rejects.toMatchObject({
      name: 'AbortError',
    })
  })
  it('reports a network failure without claiming execution success', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Network failure')))
    await expect(
      request('/api/v1/execution/operations/id/execute', { method: 'POST', key: 'k' }),
    ).rejects.toBeInstanceOf(ApiError)
  })
  it('times out a stalled request and reports an unknown outcome', async () => {
    vi.useFakeTimers()
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_url, init) =>
          new Promise((_resolve, reject) =>
            init.signal.addEventListener('abort', () => reject(init.signal.reason)),
          ),
      ),
    )
    const result = expect(
      request('/api/v1/execution/operations/id/execute', { method: 'POST' }),
    ).rejects.toMatchObject({ code: 'TIMEOUT' })
    await vi.advanceTimersByTimeAsync(20001)
    await result
  })
})

describe('Versioned lifestyle product APIs', () => {
  it('uses the v2 compiler with the precise saved evidence and stable key', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify({ code: 'OK', data: { plan: { id: '17' } } })))
    vi.stubGlobal('fetch', fetch)
    const body = {
      expected_version: 2,
      budget_id: '9',
      billing_report_id: 'report-id',
      financial_plan_id: '0',
    }
    await api.lifestyle.compile('14', body, 'stable-compile-key')
    expect(fetch.mock.calls[0][0]).toBe('/api/v2/lifestyle-goals/14/compile')
    expect(fetch.mock.calls[0][1].headers['Idempotency-Key']).toBe('stable-compile-key')
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual(body)
  })
  it('queries a persisted v2 goal including its version history', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ code: 'OK', data: [] })))
    vi.stubGlobal('fetch', fetch)
    await api.lifestyle.goal('14')
    expect(fetch.mock.calls[0][0]).toBe('/api/v2/lifestyle-goals/14?limit=100&offset=0')
  })
})
describe('Authenticated task events', () => {
  it('discards an open legacy stream and never reconnects it into a new user session', async () => {
    vi.useFakeTimers()
    setToken('first-user')
    const abort = new AbortController(),
      seen: unknown[] = []
    let source!: ReadableStreamDefaultController<Uint8Array>, ready!: () => void
    let streams = 0
    const opened = new Promise<void>((resolve) => (ready = resolve))
    const encoder = new TextEncoder()
    const fetch = vi.fn(async (url: string) => {
      if (!url.endsWith('/events'))
        return new Response(JSON.stringify({ code: 'OK', data: { job: { id: 'legacy' } } }))
      if (++streams > 1) return new Response('')
      return new Response(
        new ReadableStream({
          start(controller) {
            source = controller
            ready()
          },
        }),
      )
    })
    vi.stubGlobal('fetch', fetch)
    const stream = streamJob(
      'legacy',
      abort.signal,
      (v) => seen.push(v),
      () => {},
    )
    await opened
    await Promise.resolve()
    setToken('second-user')
    source.enqueue(
      encoder.encode('id: 8\nevent: task\ndata: {"user_id":"first-user","status":"running"}\n\n'),
    )
    source.close()
    await vi.advanceTimersByTimeAsync(1500)
    abort.abort()
    await stream
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(seen).toHaveLength(1)
    expect(seen).not.toContainEqual({ user_id: 'first-user', status: 'running' })
  })
  it('restores snapshots and resumes with the last event cursor, including split CRLF packets', async () => {
    vi.useFakeTimers()
    setToken('test-token')
    const abort = new AbortController(),
      seen: unknown[] = []
    let streams = 0,
      snapshots = 0
    const encoder = new TextEncoder()
    const fetch = vi.fn(async (url: string, init: RequestInit) => {
      if (!url.endsWith('/events')) {
        snapshots++
        return new Response(
          JSON.stringify({ code: 'OK', data: { job: { id: 'job', version: snapshots } } }),
        )
      }
      streams++
      expect((init.headers as Record<string, string>).Authorization).toBe('Bearer test-token')
      if (streams === 2) {
        expect((init.headers as Record<string, string>)['Last-Event-ID']).toBe('7')
        abort.abort()
        return new Response('')
      }
      return new Response(
        new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode('id: 7\r'))
            controller.enqueue(
              encoder.encode('\nevent: task\r\ndata: {"status":"running"}\r\n\r\n'),
            )
            controller.close()
          },
        }),
      )
    })
    vi.stubGlobal('fetch', fetch)
    const stream = streamJob(
      'job',
      abort.signal,
      (v) => seen.push(v),
      () => {},
    )
    await vi.advanceTimersByTimeAsync(1001)
    await stream
    expect(snapshots).toBe(2)
    expect(seen).toContainEqual({ status: 'running' })
  })
})
