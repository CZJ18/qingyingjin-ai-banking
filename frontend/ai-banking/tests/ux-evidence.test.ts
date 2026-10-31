import { expect, it } from 'vitest'
import { createSSRApp, h } from 'vue'
import { renderToString } from '@vue/server-renderer'
import TaskDetails from '../src/components/assistant/TaskDetails.vue'
import ModelAdmission from '../src/components/assistant/ModelAdmission.vue'

it('does not turn missing independent invocation evidence into zero tool calls', async () => {
  const app = createSSRApp(TaskDetails, { tasks: [], trace: { events: [] }, jobId: 'isolated-test', connection: '任务已结束' })
  app.component('RouterLink', { setup(_, { slots }) { return () => h('a', slots.default?.()) } })
  const html = await renderToString(app)
  expect(html).toContain('未提供独立调用记录')
  expect(html).not.toMatch(/工具执行记录\s*<span>0<\/span>/)
})

it('shows the active simulated connection while keeping detailed quotas collapsed', async () => {
  const snapshot={currency:'USD',frozen:false,approval_window:{next_request_maximum_cost:'0.00512000',remaining_approval_budget:'0.98976000',remaining_requests:0,remaining_concurrent_slots:0},approval_limits:{max_requests:2,max_concurrent:1,simulation:true}}
  const app=createSSRApp(ModelAdmission,{snapshot,loading:false,error:''})
  app.component('RouterLink',{setup(_, {slots}){return()=>h('a',slots.default?.())}})
  const html=await renderToString(app)
  expect(html).toContain('当前为模拟模式 · 非真实模型')
  expect(html).toContain('真实模型审批和额度不会自动切换到这里')
  expect(html).toContain('AI 分析暂不可用')
  expect(html).toContain('剩余请求 0 / 2')
  expect(html).not.toMatch(/<details[^>]*\sopen(?:\s|>)/)
  expect(html).not.toContain('MODEL_APPROVAL_LIMIT_EXCEEDED')
})
