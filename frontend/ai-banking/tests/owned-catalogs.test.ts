import { afterEach, expect, it, vi } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { createPinia } from 'pinia'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { createRouter, createMemoryHistory } from 'vue-router'
import LifestyleGoals from '../src/components/LifestyleGoals.vue'
import Planning from '../src/pages/Planning.vue'
import { useSession } from '../src/lib/store'

afterEach(()=>vi.unstubAllGlobals())

async function render(component: typeof LifestyleGoals|typeof Planning, path:string){
 // Test identities and cached SQL-shaped data only. Any accidental network is refused.
 vi.stubGlobal('fetch',vi.fn(async()=>{throw new Error('offline component test: network forbidden')}))
 const pinia=createPinia(),s=useSession(pinia)
 s.connected=true;s.accountId='1';s.user={id:'42',status:'active'} as typeof s.user
 expect(s.lifestyleGoalIds).toEqual([]);expect(s.planId).toBe('')
 const query=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity}}})
 query.setQueryData(['owned-lifestyle-goals',s.sessionEpoch],[{goal:{id:'17',user_id:'42',current_version:1},versions:[{text:'下月减少冲动购物',version:1}]}])
 query.setQueryData(['owned-financial-plans',s.sessionEpoch,0],[{id:'23',name:'已保存的生活资金计划',current_version:2,settings:{period:'2026-10',account_ids:[1]}}])
 const app=createSSRApp(component),router=createRouter({history:createMemoryHistory(),routes:[{path,component}]})
 await router.push(path);await router.isReady()
 app.use(pinia).use(router).use(VueQueryPlugin,{queryClient:query})
 try{return await renderToString(app)}finally{query.clear()}
}

it('shows server-owned lifestyle goals when this browser has no saved goal IDs',async()=>{
 const html=await render(LifestyleGoals,'/goals')
 expect(html).toContain('下月减少冲动购物');expect(html).toContain('value="17"')
})

it('shows persisted plans with an open action without requiring an ID lookup',async()=>{
 const html=await render(Planning,'/planning')
 expect(html).toContain('已保存的生活资金计划');expect(html).toContain('打开规划 #23')
})
