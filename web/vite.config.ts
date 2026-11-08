import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'
import process from 'node:process'

// The dev server sends /api to the dev stack's Caddy, which sends it on to the
// API, so the SPA calls it the same relative way it does in production.
const stack = process.env.PROJECTS_WEB_STACK_URL || 'http://localhost:8096'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  css: {
    preprocessorOptions: {
      scss: {
        loadPaths: [fileURLToPath(new URL('./src/assets/sass', import.meta.url))],
      },
    },
  },
  server: {
    port: 3002,
    proxy: {
      '/api': { target: stack, changeOrigin: true },
    },
  },
})
