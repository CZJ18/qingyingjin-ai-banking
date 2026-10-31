import { afterEach, expect, it, vi } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { createMemoryHistory, createRouter } from 'vue-router'
import { buildScenarioQuery, scenarioQueries } from '../src/lib/scenario-query'
import ScenarioOverview from '../src/components/assistant/ScenarioOverview.vue'

afterEach(() => vi.unstubAllGlobals())

it('builds all six existing scoped read-only tools without a planner or model request', () => {
  vi.stubGlobal('fetch', () => { throw new Error('network prohibited') })
  expect(scenarioQueries.map(s => s.id)).toEqual(['billing','transfer','investment','cards','subscriptions','birthday'])
  for (const entry of scenarioQueries) {
    const body=buildScenarioQuery(entry.id,'23','2026-09')
    expect(body.instruction).toBeUndefined()
    expect(body.max_attempts).toBe(1)
    expect(body.bindings).toEqual({account_id:'23',from:'2026-08-31T16:00:00.000Z',to:'2026-09-30T16:00:00.000Z'})
    expect(body.tasks).toHaveLength(1)
    const spec=body.tasks![0]!
    expect(spec.execution_mode).toBe('read_only')
    expect(spec.use_coze).toBe(false)
    expect(spec.required_permissions).toEqual(['READ'])
    expect(spec.parameters.account_id).toBe('23')
    if (['billing','subscriptions'].includes(entry.id)) {
      expect(spec.parameters.from).toBe(body.bindings.from)
      expect(spec.parameters.to).toBe(body.bindings.to)
      expect(spec.parameters.timezone).toBe('Asia/Shanghai')
    } else {
      expect(spec.tool).toBe('get_scenario_overview')
      expect(spec.parameters).toEqual({account_id:'23',scenario:entry.id})
    }
  }
})

it('refuses arbitrary tools, missing or invalid accounts and invalid months before submission', () => {
  for (const scenario of ['execute','require_gateway','model_smoke','__proto__'])
    expect(() => buildScenarioQuery(scenario,'23','2026-09')).toThrow()
  for (const account of ['','0','-1','1.5','23,24','9223372036854775808'])
    expect(() => buildScenarioQuery('cards',account,'2026-09')).toThrow()
  for (const month of ['','2026-13','2026-2','2026-09-01','2099-01'])
    expect(() => buildScenarioQuery('billing','23',month)).toThrow()
})

it.each([
  ['transfer','/security#recipient-heading'],['cards','/security#cards-title'],
  ['investment','/scenarios#investment'],['birthday','/scenarios#birthday'],['subscriptions','/subscriptions'],
])('takes %s results to their own form instead of a shared page top', async (scenario, destination) => {
  const router=createRouter({history:createMemoryHistory(),routes:[{path:'/:pathMatch(.*)*',component:{render:()=>null}}]})
  await router.push('/assistant');await router.isReady()
  const result={scenario,user_id:'7',account_id:'23',items:[],limitations:['仅查看已记录范围'],is_simulated:true,truncated:false}
  const html=await renderToString(createSSRApp(ScenarioOverview,{result}).use(router))
  expect(html).toContain(`href="${destination}"`)
  expect(html).toContain('不会创建操作意向')
})
