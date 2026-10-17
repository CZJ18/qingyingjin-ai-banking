import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useSession } from '../src/lib/store'
import { authorizationHeaders, sessionVersion, setToken } from '../src/api/client'

const profile = { id: '42', display_name: '生活规划用户' }
const credentials = { phone: '13800138000', password: 'a private password' }
const session = (token = 'private-access-token', user = profile) => ({
  access_token: token,
  expires_in: 900,
  user,
  phone_masked: '138****8000',
})
const ok = (data: unknown) => new Response(JSON.stringify({ code: 'OK', data }))
let saved: Map<string, string>
beforeEach(() => {
  setActivePinia(createPinia())
  saved = new Map()
  vi.useFakeTimers()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => saved.get(key) ?? null,
    setItem: (key: string, value: string) => saved.set(key, value),
  })
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  setToken('')
})
describe('Phone and password browser sessions', () => {
  it('logs in with a same-origin JSON request and verifies the returned banking identity', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(ok(session())).mockResolvedValueOnce(ok(profile))
    vi.stubGlobal('fetch', fetch)
    const s = useSession()
    await s.login(credentials)
    expect(fetch.mock.calls[0]![0]).toBe('/api/v1/auth/login')
    expect(fetch.mock.calls[0]![1]).toMatchObject({
      method: 'POST',
      credentials: 'same-origin',
      redirect: 'error',
      body: JSON.stringify(credentials),
    })
    expect(fetch.mock.calls[0]![1].headers.Authorization).toBeUndefined()
    expect(fetch.mock.calls[1]![1].headers.Authorization).toBe('Bearer private-access-token')
    expect(s.connected).toBe(true)
    expect(s.phoneMasked).toBe('138****8000')
    s.saveGoal('17')
    expect(JSON.stringify([...saved])).not.toMatch(
      /private-access-token|private password|13800138000/,
    )
  })
  it('registers through the existing server identity verification path', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(ok(session())).mockResolvedValueOnce(ok(profile))
    vi.stubGlobal('fetch', fetch)
    const s = useSession()
    await s.register({ ...credentials, display_name: '生活规划用户' })
    expect(fetch.mock.calls[0]![0]).toBe('/api/v1/auth/register')
    expect(s.user?.id).toBe('42')
  })
  it('restores the cookie session and the verified owner references after page reload', async () => {
    saved.set('qingrongjin.refs.v1.42', JSON.stringify({ goals: ['17'], agents: [] }))
    const fetch = vi.fn().mockResolvedValueOnce(ok(session())).mockResolvedValueOnce(ok(profile))
    vi.stubGlobal('fetch', fetch)
    const s = useSession()
    expect(await s.restoreSession()).toBe(true)
    expect(fetch.mock.calls[0]![0]).toBe('/api/v1/auth/refresh')
    expect(fetch.mock.calls[0]![1]).toMatchObject({ credentials: 'same-origin', body: '{}' })
    expect(s.goalIds).toEqual(['17'])
  })
  it('keeps a missing or expired cookie anonymous without claiming a login error', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            code: 'AUTH_INVALID_CREDENTIALS',
            message: '请重新登录',
          }),
          { status: 401 },
        ),
      ),
    )
    const s = useSession()
    expect(await s.restoreSession()).toBe(false)
    expect(s.connected).toBe(false)
    expect(authorizationHeaders()).toEqual({})
  })
  it('refreshes before access expiry without clearing the selected account or task epoch', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(ok(session()))
      .mockResolvedValueOnce(ok(profile))
      .mockResolvedValueOnce(ok(session('renewed-token')))
      .mockResolvedValueOnce(ok(profile))
    vi.stubGlobal('fetch', fetch)
    const s = useSession()
    await s.login(credentials)
    s.accountId = '101'
    s.saveGoal('17')
    const epoch = s.sessionEpoch,
      requestEpoch = sessionVersion()
    await vi.advanceTimersByTimeAsync(840000)
    expect(fetch.mock.calls[2]![0]).toBe('/api/v1/auth/refresh')
    expect(s.sessionEpoch).toBe(epoch)
    expect(sessionVersion()).toBe(requestEpoch)
    expect(s.accountId).toBe('101')
    expect(s.goalIds).toEqual(['17'])
    expect(authorizationHeaders()).toEqual({ Authorization: 'Bearer renewed-token' })
  })
  it('revokes on the server before clearing the local identity and active requests', async () => {
    let complete!: (value: Response) => void
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(ok(session()))
      .mockResolvedValueOnce(ok(profile))
      .mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            complete = resolve
          }),
      )
    vi.stubGlobal('fetch', fetch)
    const s = useSession()
    await s.login(credentials)
    const epoch = sessionVersion()
    const pending = s.logout()
    expect(s.connected).toBe(true)
    complete(ok(null))
    await pending
    expect(fetch.mock.calls[2]![0]).toBe('/api/v1/auth/logout')
    expect(s.connected).toBe(false)
    expect(sessionVersion()).toBeGreaterThan(epoch)
    expect(authorizationHeaders()).toEqual({})
    expect(vi.getTimerCount()).toBe(0)
  })
  it('reports logout transport failure while retaining the still-unrevoked local identity', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(ok(session()))
        .mockResolvedValueOnce(ok(profile))
        .mockRejectedValueOnce(new TypeError('offline')),
    )
    const s = useSession()
    await s.login(credentials)
    await expect(s.logout()).rejects.toMatchObject({ code: 'NETWORK_ERROR' })
    expect(s.connected).toBe(true)
  })
  it('discards a delayed login after local logout or a newer connection', async () => {
    let complete!: (value: Response) => void
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            complete = resolve
          }),
      ),
    )
    const s = useSession()
    const pending = s.login(credentials)
    s.disconnect()
    complete(ok(session()))
    await expect(pending).rejects.toThrow()
    expect(s.connected).toBe(false)
    expect(authorizationHeaders()).toEqual({})
  })
  it('fails closed if a background refresh returns a different user', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(ok(session()))
        .mockResolvedValueOnce(ok(profile))
        .mockResolvedValueOnce(
          ok(session('foreign-token', { id: '43', display_name: '其他用户' })),
        ),
    )
    const s = useSession()
    await s.login(credentials)
    await vi.advanceTimersByTimeAsync(840000)
    expect(s.connected).toBe(false)
    expect(authorizationHeaders()).toEqual({})
    expect(s.authError).toContain('登录')
  })
  it('does not restore a delayed refresh into a newer login identity', async () => {
    let complete!: (value: Response) => void
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(ok(session()))
      .mockResolvedValueOnce(ok(profile))
      .mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            complete = resolve
          }),
      )
      .mockResolvedValueOnce(ok(session('new-owner-token', { id: '43', display_name: '另一用户' })))
      .mockResolvedValueOnce(ok({ id: '43', display_name: '另一用户' }))
    vi.stubGlobal('fetch', fetch)
    const s = useSession()
    await s.login(credentials)
    await vi.advanceTimersByTimeAsync(840000)
    await s.login({ phone: '13900139000', password: 'different password' })
    complete(ok(session('stale-refreshed-token')))
    await vi.advanceTimersByTimeAsync(0)
    expect(s.user?.id).toBe('43')
    expect(authorizationHeaders()).toEqual({ Authorization: 'Bearer new-owner-token' })
  })
  it('rejects malformed session responses before sending any financial request', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(ok({ ...session(), expires_in: '900' }))
    vi.stubGlobal('fetch', fetch)
    const s = useSession()
    await expect(s.login(credentials)).rejects.toMatchObject({ code: 'INVALID_RESPONSE' })
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(s.connected).toBe(false)
    expect(authorizationHeaders()).toEqual({})
  })
  it('identifies an invalid JSON envelope as a server response error', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(new Response('null')))
    await expect(useSession().login(credentials)).rejects.toMatchObject({
      code: 'INVALID_RESPONSE',
    })
  })
  it('clears the previous user on incorrect-password failure without persisting the submitted password', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(ok(session()))
      .mockResolvedValueOnce(ok(profile))
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ code: 'AUTH_INVALID_CREDENTIALS', message: '手机号或密码错误' }),
          { status: 401 },
        ),
      )
    vi.stubGlobal('fetch', fetch)
    const s = useSession()
    await s.login(credentials)
    s.accountId = '101'
    await expect(
      s.login({ ...credentials, password: 'incorrect private password' }),
    ).rejects.toMatchObject({ status: 401 })
    expect(s.connected).toBe(false)
    expect(s.accountId).toBe('')
    expect(JSON.stringify([...saved])).not.toContain('private password')
    expect(authorizationHeaders()).toEqual({})
  })
})
