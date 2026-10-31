import { createApp, nextTick } from 'vue'
import { createPinia } from 'pinia'
import { VueQueryPlugin, QueryClient } from '@tanstack/vue-query'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import './style.css'
import './refinement.css'
const routes = [
  { path: '/', component: () => import('./pages/Overview.vue') },
  { path: '/assistant', component: () => import('./pages/Assistant.vue') },
  { path: '/agents', component: () => import('./pages/Agents.vue') },
  { path: '/billing', component: () => import('./pages/Billing.vue') },
  { path: '/subscriptions', component: () => import('./pages/Subscriptions.vue') },
  { path: '/goals', component: () => import('./pages/Goals.vue') },
  { path: '/planning', component: () => import('./pages/Planning.vue') },
  { path: '/security', component: () => import('./pages/Security.vue') },
  { path: '/scenarios', component: () => import('./pages/Scenarios.vue') },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30000,
      retry: (count, error) =>
        !('status' in error && [401, 403, 404].includes(Number(error.status))) && count < 1,
      refetchOnWindowFocus: false,
    },
  },
})
createApp(App)
  .use(createPinia())
  .use(createRouter({ history: createWebHistory(), routes, scrollBehavior: async(to,_from,saved) => {
    if(saved)return saved
    if(!to.hash)return {top:0}
    // Await the actual page queries before locating a section: cards/AA content
    // above it otherwise expands after the initial router scroll.
    if(queryClient.isFetching())await new Promise<void>(resolve=>{
      let unsubscribe=()=>{}
      const finish=()=>{clearTimeout(timer);unsubscribe();resolve()}
      const timer=setTimeout(finish,2500)
      unsubscribe=queryClient.getQueryCache().subscribe(()=>{if(!queryClient.isFetching())finish()})
    })
    await nextTick()
    return document.getElementById(to.hash.slice(1)) ? {el:to.hash,top:24} : {top:0}
  } }))
  .use(VueQueryPlugin, { queryClient })
  .mount('#app')
