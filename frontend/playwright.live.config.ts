/**
 * Playwright config for LIVE backend tests.
 * Runs against real 5-node cluster at localhost:10241-10245.
 *
 * Usage:
 *   pnpm exec playwright test --config=playwright.live.config.ts
 *
 * Prerequisites:
 *   - 5-node cluster running: scripts/cluster-demo-start.sh
 *   - Nuxt preview built: pnpm build && pnpm preview (port 3000)
 *   - Fresh cluster (no prior setup): setup_completed=false
 *     OR existing admin with LIVE_ADMIN_USER / LIVE_ADMIN_PASS env vars
 */
import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir:       './tests/e2e/live',
  fullyParallel: false,
  retries:       0,
  workers:       1,
  timeout:       30000,

  reporter: [
    ['list'],
    ['html', { open: 'never', outputFolder: 'playwright-report-live' }],
    ['json', { outputFile: 'playwright-results-live.json' }],
  ],

  use: {
    baseURL:    process.env.NUXT_URL ?? 'http://localhost:3000',
    trace:      'on-first-retry',
    screenshot: 'on',
    video:      'retain-on-failure',
    headless:   true,
  },

  projects: [
    {
      name: 'live-setup',
      testMatch: /live\/setup\.ts/,
    },
    {
      name: 'live-chromium',
      use: {
        ...devices['Desktop Chrome'],
        storageState: './tests/e2e/.auth/live-state.json',
        viewport: { width: 1440, height: 900 },
      },
      dependencies: ['live-setup'],
    },
  ],

  webServer: {
    command:             'pnpm preview',
    url:                 'http://localhost:3000',
    reuseExistingServer: true,
    timeout:             30000,
    env: {
      PORT:                                '3000',
      NODE_ENV:                            'production',
      NUXT_PUBLIC_DEFAULT_BACKEND_URL:     'http://localhost:10241',
    },
  },
})
