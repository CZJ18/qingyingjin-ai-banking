import { afterEach, expect, it, vi } from 'vitest'
import { createApp, effectScope, nextTick, ref } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { useAgentJob } from '../src/lib/agent-job'
import { setToken } from '../src/api/client'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  setToken('')
})
it('reopens task events when an interrupted task starts running again', async () => {
  vi.useFakeTimers()
  const id = 'lg-12345678-1234-4234-9234-123456789abc'
  let status = 'interrupted',
    finish!: (r: Response) => void,
    streams = 0
  const reply = (data: unknown) => new Response(JSON.stringify({ code: 'OK', data }))
  const task = () => ({
    task_id: id,
    owner_engine: 'langgraph',
    status,
    version: 3,
    request: { instruction: '分析账单', tasks: [], max_attempts: 2, timeout_ms: 90000 },
    result: null,
    confirmation: null,
    error_code: '',
    created_at: '2026-10-14T00:00:00Z',
    updated_at: '2026-10-14T00:00:00Z',
  })
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string) => {
      if (url.endsWith('/run')) {
        status = 'running'
        return new Promise<Response>((resolve) => (finish = resolve))
      }
      if (url.includes('/events/stream')) {
        streams++
        return Promise.resolve(
          new Response(': heartbeat\n\n', { headers: { 'Content-Type': 'text/event-stream' } }),
        )
      }
      return Promise.resolve(reply(url.includes('/events?') ? [] : task()))
    }),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const app = createApp({}),
    scope = effectScope(),
    enabled = ref(true)
  app.use(VueQueryPlugin, { queryClient: client })
  const graph = app.runWithContext(() => scope.run(() => useAgentJob(ref(id), enabled))!)
  await vi.advanceTimersByTimeAsync(1)
  await nextTick()
  expect(graph.task.data.value?.status).toBe('interrupted')
  expect(streams).toBe(0)
  const running = graph.run(3)
  await graph.task.refetch()
  await nextTick()
  await vi.advanceTimersByTimeAsync(1)
  expect(streams).toBeGreaterThan(0)
  status = 'succeeded'
  finish(reply(task()))
  await running
  enabled.value = false
  await nextTick()
  scope.stop()
  client.clear()
})
