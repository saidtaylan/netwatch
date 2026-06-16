/**
 * Shared Playwright route mocks — intercept API calls in page context.
 * Use with page.route() so tests don't depend on pinia persistence loading.
 */
import type { Page } from '@playwright/test'

// Mock shape must match types/api.ts FleetSnapshot: `summary` (rollup) +
// `targets` (array, each element carries `id`). The earlier shape used
// `target_counts` + `targets` as an object map, which doesn't iterate
// in useFleet's `for (const t of snapshot.targets)` loop and silently
// rendered an empty Down Targets section in the e2e test.
// NOTE: UP targets intentionally omit scope / classification / confidence /
// affected_apps to mirror the REAL backend — /fleet/status only computes scope
// classification for targets that are down. The mock used to set these on every
// target, which hid a crash on the target-detail page for healthy targets.
const FLEET = {
  node_name: 'e2e-node',
  cluster_enabled: false,
  summary: { up: 2, hard_down: 1, soft_down: 0, soft_up: 0, unknown: 0 },
  targets: [
    {
      id: 'api-gateway', name: 'api-gateway', target: '127.0.0.1:8080', type: 'tcp',
      consensus_state: 'up',
      by_node: { 'e2e-node': { state: 'up', seq: 3 } },
    },
    {
      id: 'db-primary', name: 'db-primary', target: '127.0.0.1:5432', type: 'tcp',
      consensus_state: 'hard_down', scope: 'STANDALONE', classification: 'REAL_OUTAGE',
      confidence: 1, affected_apps: [],
      by_node: { 'e2e-node': { state: 'hard_down', seq: 7, error_code: 'dial tcp: connection refused', latency: 0 } },
    },
    {
      id: 'web-server', name: 'web-server', target: 'https://example.com', type: 'http',
      consensus_state: 'up',
      by_node: { 'e2e-node': { state: 'up', seq: 1 } },
    },
  ],
}

const MAINTENANCE_WINDOW = {
  id: 'e2e-win-1', target_id: 'db-primary',
  reason: 'E2E test', started_at: new Date().toISOString(),
  ends_at: new Date(Date.now() + 3600000).toISOString(), created_by: 'ui',
}

/** Install all API route mocks on a page. */
export async function mockAllRoutes(page: Page) {
  const base = 'http://localhost:19240'

  await page.route(`${base}/health`,       r => r.fulfill({ json: { status: 'ok' } }))
  await page.route(`${base}/version`,      r => r.fulfill({ json: { version: 'e2e', build_time: '' } }))
  // Legacy whoami (still mocked for older specs)
  await page.route(`${base}/auth/whoami`,  r => r.fulfill({ json: { role: 'admin' } }))

  // ── B28 JWT auth endpoints ────────────────────────────────────────────────
  const adminUser = {
    id: 'u-e2e-1', username: 'admin', role: 'admin', display_name: 'E2E Admin',
    created_at: new Date().toISOString(),
  }
  // setup_completed starts false; flipped after successful /auth/setup
  let setupCompleted = false
  await page.route(`${base}/auth/status`, r => r.fulfill({
    json: { setup_completed: setupCompleted, user_count: setupCompleted ? 1 : 0 },
  }))
  await page.route(`${base}/auth/setup`, async r => {
    if (r.request().method() !== 'POST') return r.continue()
    setupCompleted = true
    r.fulfill({ json: { token: 'jwt-e2e', user: adminUser, cluster_nodes: [base] } })
  })
  await page.route(`${base}/auth/login`, async r => {
    if (r.request().method() !== 'POST') return r.continue()
    r.fulfill({ json: { token: 'jwt-e2e', user: adminUser, cluster_nodes: [base] } })
  })
  await page.route(`${base}/auth/me`, r => r.fulfill({ json: adminUser }))
  await page.route(`${base}/auth/cluster-nodes`, r => r.fulfill({ json: { urls: [base] } }))
  await page.route(`${base}/users`, r => r.fulfill({ json: [adminUser] }))
  await page.route(`${base}/fleet/status`, r => r.fulfill({ json: FLEET }))
  await page.route(`${base}/topology`,     r => r.fulfill({ json: { targets: {} } }))
  await page.route(`${base}/slo`,          r => r.fulfill({ status: 503, json: { error: 'disabled' } }))
  await page.route(`${base}/cluster/state`,   r => r.fulfill({ status: 503, json: { error: 'disabled' } }))
  await page.route(`${base}/cluster/config`,  r => r.fulfill({ status: 503, json: { error: 'disabled' } }))
  await page.route(`${base}/cluster/probers`, r => r.fulfill({ status: 503, json: { error: 'disabled' } }))
  await page.route(`${base}/cluster/maintenance`, async r => {
    if (r.request().method() === 'GET') {
      r.fulfill({ json: [] })
    } else if (r.request().method() === 'PUT') {
      r.fulfill({ json: MAINTENANCE_WINDOW })
    }
  })
  await page.route(`${base}/cluster/maintenance/**`, async r => {
    r.fulfill({ json: {} })
  })
  await page.route(`${base}/geo/latency/**`, r => r.fulfill({ json: { target_id: 'test', anomaly: false, by_node: [] } }))
}

/** Seed localStorage with minimal pinia state so app can connect */
export async function seedAuth(page: Page) {
  await page.addInitScript(() => {
    const nodes = {
      configured:    [{ url: 'http://localhost:19240' }],
      active:        'http://localhost:19240',
      health:        { 'http://localhost:19240': 'healthy' },
      lastCheckedAt: Date.now(),
    }
    const auth = { token: 'test-token', role: 'admin' }
    localStorage.setItem('nodes', JSON.stringify(nodes))
    localStorage.setItem('auth',  JSON.stringify(auth))
  })
}
