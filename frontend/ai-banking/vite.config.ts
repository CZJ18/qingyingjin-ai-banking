import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  return {
    plugins: [vue(), tailwindcss()],
    resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
    server: {
      port: 5178,
      strictPort: true,
      proxy: {
        '/product': {
          target: env.AGENT_PROXY_TARGET || 'http://127.0.0.1:8091',
          changeOrigin: true,
          rewrite: (path) => path.replace(/^\/product/, ''),
          timeout: 210000,
          proxyTimeout: 210000,
        },
        // Authentication compares Origin with Host, including the dev port.
        '/api': { target: env.BANKING_PROXY_TARGET || 'http://127.0.0.1:8090', changeOrigin: false },
        '/healthz': { target: env.BANKING_PROXY_TARGET || 'http://127.0.0.1:8090' },
        '/readyz': { target: env.BANKING_PROXY_TARGET || 'http://127.0.0.1:8090' },
      },
    },
  }
})
