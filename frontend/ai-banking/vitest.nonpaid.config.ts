// Deliberately does not call loadEnv or read frontend .env files.
import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'
export default defineConfig({ envDir: fileURLToPath(new URL('../../output/banking-ai-completion-20261027/empty-env', import.meta.url)), plugins: [vue(), tailwindcss()], resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } } })
