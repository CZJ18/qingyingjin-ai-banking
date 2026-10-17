import { afterEach, expect, it, vi } from 'vitest'
import { recipientApi } from '../src/api/recipients'
import { setToken } from '../src/api/client'
afterEach(() => { vi.unstubAllGlobals(); setToken('') })
it('keeps the full phone out of URLs and sends the human bearer', async () => {
  const fetch = vi.fn(async (_url: string, _options: RequestInit) => new Response(JSON.stringify({ code: 'OK', data: { items: [], limited: false } })))
  vi.stubGlobal('fetch', fetch); setToken('human-session')
  await recipientApi.search({ kind: 'phone', query: '13800138000' })
  expect(fetch.mock.calls[0]![0]).toBe('/api/v1/recipients/search')
  const options = fetch.mock.calls[0]![1]
  expect(options.headers).toMatchObject({ Authorization: 'Bearer human-session' })
  expect(JSON.parse(options.body as string)).toEqual({ kind: 'phone', query: '13800138000' })
})
it('sends versioned opt-in without a payment request', async () => {
  const fetch = vi.fn(async (_url: string, _options: RequestInit) => new Response(JSON.stringify({ code: 'OK', data: { enabled: true, account_id: '4', version: 2 } })))
  vi.stubGlobal('fetch', fetch)
  await recipientApi.configure({ enabled: true, account_id: '4', expected_version: 1 })
  expect(fetch).toHaveBeenCalledTimes(1)
  expect(fetch.mock.calls[0]![0]).toBe('/api/v1/recipients/settings')
  expect(fetch.mock.calls[0]![1].method).toBe('PUT')
})
it('rejects malformed directory results instead of selecting a fake recipient', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ code: 'OK', data: { items: [{ id: 123 }], limited: false } }))))
  await expect(recipientApi.search({ kind: 'name', query: '张三' })).rejects.toMatchObject({ code: 'INVALID_RESPONSE' })
})
it('surfaces rate limits and invalid settings honestly', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ code: 'RECIPIENT_RATE_LIMITED', message: 'Try later' }), { status: 429 })).mockResolvedValueOnce(new Response(JSON.stringify({ code: 'OK', data: [] }))))
  await expect(recipientApi.search({ kind: 'name', query: '张三' })).rejects.toMatchObject({ code: 'RECIPIENT_RATE_LIMITED' })
  await expect(recipientApi.settings()).rejects.toMatchObject({ code: 'INVALID_RESPONSE' })
})
