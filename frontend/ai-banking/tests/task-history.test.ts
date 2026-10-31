import { afterEach, expect, it, vi } from 'vitest'
import { createApp, effectScope, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { useTaskHistory } from '../src/lib/task-history'
import { useSession } from '../src/lib/store'

afterEach(() => { vi.unstubAllGlobals() })
it('loads server-owned history without local references or running any task and isolates sessions', async () => {
  setActivePinia(createPinia())
  const session = useSession()
  session.connected = true
  session.user = { id: '42', display_name: 'Synthetic history user' } as typeof session.user
  const fetch = vi.fn().mockImplementation(async () => new Response(JSON.stringify({ code: 'OK', data: {
    items: [{ task_id: 'lg-12345678-1234-4234-9234-123456789abc', status: 'failed', version: 2,
      instruction: 'SQL-backed task', error_code: 'MODEL_APPROVAL_REQUIRED',
      created_at: '2026-10-22T00:00:00Z', updated_at: '2026-10-22T00:00:00Z' }],
    limit: 20, offset: 0, total: 1,
  } })))
  vi.stubGlobal('fetch', fetch)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const app = createApp({}), scope = effectScope()
  app.use(VueQueryPlugin, { queryClient: client })
  const history = app.runWithContext(() => scope.run(() => useTaskHistory())!)
  try {
    await history.refetch()
    expect(session.agentTaskIds).toEqual([])
    expect(history.items.value[0]?.instruction).toBe('SQL-backed task')
    expect(history.hasNextPage.value).toBe(false)
    expect(fetch.mock.calls.every(([path]) => path === '/product/api/v1/agent-tasks?limit=20&offset=0')).toBe(true)
    session.disconnect()
    await nextTick()
    expect(history.items.value).toEqual([])
    expect(history.total.value).toBeUndefined()
  } finally { scope.stop(); client.clear() }
})
