import { defineConfig, devices } from '@playwright/test'

/**
 * E2E tests against the Nuxt dev/preview server.
 *
 * Run against a live backend:
 *   BACKEND_URL=http://localhost:10240 ADMIN_TOKEN=your-token pnpm test:e2e
 *
 * Run with mock backend (default, used in CI):
 *   pnpm test:e2e
 *   The fixtures/mock-backend.ts server intercepts requests.
 */
export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: false,         // sequential — mock server is shared
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: [['list'], ['html', { open: 'never' }]],

  use: {
    baseURL:           process.env.NUXT_URL ?? 'http://localhost:3000',
    trace:             'on-first-retry',
    screenshot:        'only-on-failure',
    video:             'off',
    // Store auth state between tests
    storageStatePath:  './tests/e2e/.auth/state.json',
  },

  globalSetup:    './tests/e2e/global-setup.ts',
  globalTeardown: './tests/e2e/global-setup.ts',

  projects: [
    // Setup project: login once, save state
    {
      name: 'setup',
      testMatch: /.*\.setup\.ts/,
    },
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        storageState: './tests/e2e/.auth/state.json',
      },
      dependencies: ['setup'],
    },
  ],

  // Start the Nuxt preview server for tests
  webServer: {
    command:   'pnpm preview',
    url:       'http://localhost:3000',
    reuseExistingServer: !process.env.CI,
    timeout:   30000,
    env: {
      PORT: '3000',
      NODE_ENV: 'production',
      NUXT_PUBLIC_DEFAULT_BACKEND_URL: process.env.BACKEND_URL ?? 'http://localhost:19240',
    },
  },
})
