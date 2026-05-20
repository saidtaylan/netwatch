import { defineVitestConfig } from '@nuxt/test-utils/config'

export default defineVitestConfig({
  test: {
    environment: 'nuxt',
    globals: true,
    setupFiles: ['tests/setup.ts'],
    include: ['tests/unit/**/*.{test,spec}.ts'],
    exclude: ['tests/e2e/**', 'node_modules', 'dist', '.nuxt', '.output'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'json'],
      include: ['app/utils/**', 'app/stores/**', 'app/composables/**'],
    },
  },
})
