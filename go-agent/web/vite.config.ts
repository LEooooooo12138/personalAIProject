import { defineConfig } from 'vitest/config'
import { loadEnv } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig(({ mode }) => {
  const target = loadEnv(mode, '.', 'CONSOLE_DEV_').CONSOLE_DEV_TARGET
  if (target) {
    const url = new URL(target)
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || url.pathname !== '/') throw new Error('CONSOLE_DEV_TARGET must be an HTTP(S) origin without credentials or a path')
  }
  const proxy = target ? {
    '/api/console/v1': { target, ws: true, changeOrigin: false },
    '/app/auth-worker.js': { target, changeOrigin: false },
  } : undefined
  return {
    base: '/app/', plugins: [react()],
    server: { proxy },
    build: { outDir: '../static/console', emptyOutDir: true },
    test: { environment: 'jsdom', setupFiles: ['./src/test/setup.ts'], css: true, exclude: ['e2e/**', 'node_modules/**'] },
  }
})