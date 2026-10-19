import { expect, it } from 'vitest'
import { createSSRApp, h } from 'vue'
import { createPinia } from 'pinia'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { renderToString } from '@vue/server-renderer'
import Assistant from '../src/pages/Assistant.vue'

it('opens in monthly goal mode with an explicit general task alternative', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const app = createSSRApp(Assistant)
  app.use(createPinia())
  app.use(VueQueryPlugin, { queryClient: client })
  app.component('RouterLink', { setup(_, { slots }) { return () => h('a', slots.default?.()) } })
  const html = await renderToString(app)
  expect(html).toMatch(/aria-pressed="true"[^>]*>本月目标/)
  expect(html).toContain('一般任务')
  expect(html).toContain('这个月，你想实现什么目标？')
  expect(html).toContain('AI 提出建议后，由你最终确认方案')
  client.clear()
})
