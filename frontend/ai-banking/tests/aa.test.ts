import { afterEach, expect, it, vi } from 'vitest'
import { aaApi } from '../src/api/aa'
import { setToken } from '../src/api/client'
afterEach(() => { vi.unstubAllGlobals(); setToken('') })
it('binds manual confirmation to reviewed intent, purpose, nonce and server version without financial execution', async () => {
  const fetch = vi.fn(async (_url: string, _options: RequestInit) => new Response(JSON.stringify({ code: 'OK', data: {} })))
  vi.stubGlobal('fetch', fetch)
  const body={participant_id:'slot',purpose:'report_paid' as const,expected_version:7,intent_id:'intent',digest:'a'.repeat(64),nonce:'n'.repeat(64)}
  await aaApi.confirmManual('42','bill',body)
  expect(fetch.mock.calls[0]![0]).toBe('/api/v1/aa/wechat/42/bill/confirm')
  expect(JSON.parse(fetch.mock.calls[0]![1].body as string)).toEqual(body)
  expect(fetch).toHaveBeenCalledTimes(1)
})
it('keeps private invitation in a request body, never the URL', async () => {
  const fetch = vi.fn(async (_url: string, _options: RequestInit) => new Response(JSON.stringify({ code: 'OK', data: {} })))
  vi.stubGlobal('fetch', fetch); setToken('owner-session')
  await aaApi.preview('private-fixture-invitation')
  const [url, options] = fetch.mock.calls[0]!
  expect(url).toBe('/api/v1/aa/invitations/preview')
  expect(JSON.parse(options.body as string)).toEqual({ invitation: 'private-fixture-invitation' })
  expect(options.headers).toMatchObject({ Authorization: 'Bearer owner-session' })
})
it('join claims a slot without an execute call or confirmed flag', async () => {
  const fetch = vi.fn(async (_url: string, _options: RequestInit) => new Response(JSON.stringify({ code: 'OK', data: {} })))
  vi.stubGlobal('fetch', fetch)
  await aaApi.join('private-fixture', 'slot-id')
  expect(fetch).toHaveBeenCalledTimes(1)
  expect(fetch.mock.calls[0]![0]).toBe('/api/v1/aa/join')
  expect(JSON.parse(fetch.mock.calls[0]![1].body as string)).toEqual({ invitation: 'private-fixture', participant_id: 'slot-id' })
})
it('surfaces cancellation version conflicts', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ code: 'VERSION_CONFLICT', message: 'Bill changed' }), { status: 409 })))
  await expect(aaApi.cancel('bill', 2)).rejects.toMatchObject({ code: 'VERSION_CONFLICT' })
})
