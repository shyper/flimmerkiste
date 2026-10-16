import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { svelteTesting } from '@testing-library/svelte/vite'

const backend = process.env.FLIMMERKISTE_BACKEND || 'http://localhost:8089'

export default defineConfig({
  plugins: [svelte(), svelteTesting()],
  build: { outDir: 'dist', emptyOutDir: true, chunkSizeWarningLimit: 600 },
  server: {
    proxy: {
      '/api': backend, '/img': backend, '/logos': backend,
    },
  },
  test: { environment: 'jsdom', include: ['src/**/*.test.js'], env: { TZ: 'Europe/Berlin' } },
})
