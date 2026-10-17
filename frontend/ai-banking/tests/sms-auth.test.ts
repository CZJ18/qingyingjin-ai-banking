import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { auth } from '../src/api/auth'
import { authorizationHeaders, setToken } from '../src/api/client'
import { useSession } from '../src/lib/store'

const ok = (data: unknown) => new Response(JSON.stringify({ code: 'OK', data }))
const profile = { id: '42', display_name: '开发短信用户' }
const credentials = { phone: '13800138000', password: 'private password' }
const session = (verified = false) => ({
  access_token: 'private-token',
  expires_in: 900,
  user: profile,
  phone_masked: '138****8000',
  phone_verified: verified,
  phone_verification_method: verified ? 'development' : '',
})
const challenge = { challenge_id: 'challenge-123', expires_in: 300, retry_after: 60 }
let values: Map<string, string>
beforeEach(() => {
  setActivePinia(createPinia())
  values = new Map()
  vi.useFakeTimers()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
  })
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  setToken('')
})

describe('Development SMS authentication contract', () => {
  it('reads anonymous capabilities without sending a cookie or access token', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        ok({
          sms_enabled: true,
          sms_mode: 'development',
          registration_verification_required: true,
          code_expires_in: 300,
          resend_after: 60,
        }),
      )
    vi.stubGlobal('fetch', fetch)
    setToken('existing-private-token')
    expect((await auth.capabilities()).sms_mode).toBe('development')
    expect(fetch.mock.calls[0]![0]).toBe('/api/v1/auth/capabilities')
    expect(fetch.mock.calls[0]![1]).toMatchObject({ method: 'GET', credentials: 'omit' })
    expect(fetch.mock.calls[0]![1].headers.Authorization).toBeUndefined()
    expect(fetch.mock.calls[0]![1].body).toBeUndefined()
  })
  it('accepts SMS-disabled capabilities without inventing a challenge or code', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          ok({
            sms_enabled: false,
            sms_mode: 'disabled',
            registration_verification_required: false,
            code_expires_in: 300,
            resend_after: 60,
          }),
        ),
    )
    expect((await auth.capabilities()).sms_enabled).toBe(false)
  })
  it('sends registration and reset challenges without expecting a browser session', async () => {
    const fetch = vi.fn().mockImplementation(async () => ok(challenge))
    vi.stubGlobal('fetch', fetch)
    expect(await auth.sendRegistrationCode(credentials.phone)).toEqual(challenge)
    expect(await auth.sendPasswordResetCode(credentials.phone)).toEqual(challenge)
    expect(fetch.mock.calls.map((call) => call[0])).toEqual([
      '/api/v1/auth/sms/registration',
      '/api/v1/auth/sms/password-reset',
    ])
    expect(JSON.parse(fetch.mock.calls[0]![1].body)).toEqual({ phone: credentials.phone })
    expect(fetch.mock.calls[0]![1].headers.Authorization).toBeUndefined()
  })
  it('uses the current Bearer for bound-phone verification and never lets the caller choose a phone', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(ok(challenge)).mockResolvedValueOnce(ok(null))
    vi.stubGlobal('fetch', fetch)
    setToken('current-user-token')
    await auth.sendPhoneVerificationCode()
    await auth.verifyPhone({
      challenge_id: challenge.challenge_id,
      code: '123456',
      current_password: 'private password',
    })
    expect(fetch.mock.calls[0]![0]).toBe('/api/v1/auth/sms/phone-verification')
    expect(JSON.parse(fetch.mock.calls[0]![1].body)).toEqual({})
    expect(fetch.mock.calls[1]![0]).toBe('/api/v1/auth/phone/verify')
    expect(fetch.mock.calls[1]![1].headers.Authorization).toBe('Bearer current-user-token')
  })
  it('passes a registration challenge to the server and keeps verification status from the session', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(ok(session(true)))
      .mockResolvedValueOnce(ok(profile))
    vi.stubGlobal('fetch', fetch)
    const s = useSession()
    await s.register({ ...credentials, challenge_id: challenge.challenge_id, code: '123456' })
    expect(JSON.parse(fetch.mock.calls[0]![1].body).code).toBe('123456')
    expect(s.phoneVerified).toBe(true)
    expect(s.phoneVerificationMethod).toBe('development')
    s.saveGoal('17')
    expect(JSON.stringify([...values])).not.toMatch(
      /123456|private password|private-token|13800138000/,
    )
  })
  it('clears the authenticated identity after successful reset and does not automatically log in', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(ok(session()))
      .mockResolvedValueOnce(ok(profile))
      .mockResolvedValueOnce(ok(null))
    vi.stubGlobal('fetch', fetch)
    const s = useSession()
    await s.login(credentials)
    await s.resetPassword({
      phone: credentials.phone,
      challenge_id: challenge.challenge_id,
      code: '123456',
      new_password: 'new private password',
    })
    expect(fetch.mock.calls[2]![0]).toBe('/api/v1/auth/password/reset')
    expect(s.connected).toBe(false)
    expect(s.phoneVerified).toBe(false)
    expect(authorizationHeaders()).toEqual({})
    expect(vi.getTimerCount()).toBe(0)
    expect(fetch).toHaveBeenCalledTimes(3)
  })
  it('retains the existing identity after a rejected reset challenge', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(ok(session()))
        .mockResolvedValueOnce(ok(profile))
        .mockResolvedValueOnce(
          new Response(JSON.stringify({ code: 'AUTH_INVALID_CODE', message: '验证码错误或失效' }), {
            status: 400,
          }),
        ),
    )
    const s = useSession()
    await s.login(credentials)
    await expect(
      s.resetPassword({
        phone: credentials.phone,
        challenge_id: 'wrong',
        code: '999999',
        new_password: 'new password',
      }),
    ).rejects.toMatchObject({ code: 'AUTH_INVALID_CODE' })
    expect(s.connected).toBe(true)
  })
  it('refreshes verification status for the same owner after bound-phone verification', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(ok(session()))
        .mockResolvedValueOnce(ok(profile))
        .mockResolvedValueOnce(ok(null))
        .mockResolvedValueOnce(ok(session(true)))
        .mockResolvedValueOnce(ok(profile)),
    )
    const s = useSession()
    await s.login(credentials)
    const epoch = s.sessionEpoch
    await s.verifyPhone({
      challenge_id: challenge.challenge_id,
      code: '123456',
      current_password: credentials.password,
    })
    expect(s.phoneVerified).toBe(true)
    expect(s.phoneVerificationMethod).toBe('development')
    expect(s.sessionEpoch).toBe(epoch)
  })
  it('rejects an HTTP challenge response that leaks a verification code', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(ok({ ...challenge, code: '123456' })))
    await expect(auth.sendRegistrationCode(credentials.phone)).rejects.toMatchObject({
      code: 'INVALID_RESPONSE',
    })
  })
  it('rejects contradictory capabilities instead of silently enabling a fake verification flow', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(
          ok({
            sms_enabled: true,
            sms_mode: 'disabled',
            registration_verification_required: true,
            code_expires_in: 300,
            resend_after: 60,
          }),
        ),
    )
    await expect(auth.capabilities()).rejects.toMatchObject({ code: 'INVALID_RESPONSE' })
  })
})
