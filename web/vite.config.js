import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { svelteTesting } from '@testing-library/svelte/vite'

const backend = process.env.FLIMMERKISTE_BACKEND || 'http://localhost:8089'

// Tests give the same result on every computer and on the CI runner: the test
// workers start with a fixed system language (read once at their start, so it
// is set here, before they are spawned). Code must not depend on it anyway:
// src/tests/locale.test.js fails on formatting without an explicit language.
if (process.env.VITEST) {
  process.env.LANG = process.env.LC_ALL = process.env.LANGUAGE = 'en_US.UTF-8'
}

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
