import { createApp } from 'vue'
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
  .use(createRouter({ history: createWebHistory(), routes, scrollBehavior: () => ({ top: 0 }) }))
  .use(VueQueryPlugin, { queryClient })
  .mount('#app')
