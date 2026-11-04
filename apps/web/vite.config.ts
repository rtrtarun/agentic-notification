import react from '@vitejs/plugin-react'
import { loadEnv } from 'vite'
import { defineConfig } from 'vitest/config'

export default defineConfig(({ mode }) => ({
  // GitHub Pages serves a project site from /<repo>/, so the hosted build sets VITE_BASE.
  base: loadEnv(mode, '.', 'VITE_').VITE_BASE || '/',
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    port: 3000,
  },
  test: {
    environment: 'node',
  },
}))
