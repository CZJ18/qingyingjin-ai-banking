import { afterEach, expect, it, vi } from 'vitest'
import { renewalApi } from '../src/api/renewals'
import { setToken } from '../src/api/client'
afterEach(() => { vi.unstubAllGlobals(); setToken('') })
it('queries reminders with user authorization and no cookies', async () => {
  const fetch = vi.fn(async () => new Response(JSON.stringify({ code: 'OK', data: { items: [] } })))
  vi.stubGlobal('fetch', fetch); setToken('user-session')
  await renewalApi.list()
  expect(fetch.mock.calls[0]?.[0]).toBe('/api/v1/notifications/renewals')
  expect((fetch.mock.calls as unknown[][])[0]?.[1]).toMatchObject({ credentials: 'omit', headers: { Authorization: 'Bearer user-session' } })
})
it('sends settings version and accepts zero-day reminders', async () => {
  const fetch = vi.fn(async () => new Response(JSON.stringify({ code: 'OK', data: {} })))
  vi.stubGlobal('fetch', fetch)
  await renewalApi.configure({ enabled: true, lead_days: 0, expected_version: 2 })
  const call = (fetch.mock.calls as unknown[][])[0]![1] as RequestInit
  expect(call.method).toBe('PUT')
  expect(JSON.parse(call.body as string)).toEqual({ enabled: true, lead_days: 0, expected_version: 2 })
})
it('surfaces stale-notice conflict without fabricating a read', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ code: 'VERSION_CONFLICT', message: 'Refresh' }), { status: 409 })))
  await expect(renewalApi.read('123', 2)).rejects.toMatchObject({ code: 'VERSION_CONFLICT', status: 409 })
})
