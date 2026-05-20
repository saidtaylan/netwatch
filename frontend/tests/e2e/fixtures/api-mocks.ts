/**
 * Shared Playwright route mocks — intercept API calls in page context.
 * Use with page.route() so tests don't depend on pinia persistence loading.
 */
import type { Page } from '@playwright/test'

const FLEET = {
  node_name: 'e2e-node', cluster_enabled: false,
  target_counts: { up: 2, hard_down: 1, soft_down: 0, soft_up: 0, unknown: 0 },
  down_targets: ['db-primary'],
  targets: {
    'api-gateway': {
      name: 'api-gateway', target: '127.0.0.1:8080', type: 'tcp',
      consensus_state: 'up', scope: 'STANDALONE', classification: 'AMBIGUOUS',
      confidence: 1, affected_apps: ['payment-app'],
      by_node: { 'e2e-node': { state: 'up', seq: 3, error_code: '', latency: 0.012 } },
    },
    'db-primary': {
      name: 'db-primary', target: '127.0.0.1:5432', type: 'tcp',
      consensus_state: 'hard_down', scope: 'STANDALONE', classification: 'REAL_OUTAGE',
      confidence: 1, affected_apps: [],
      by_node: { 'e2e-node': { state: 'hard_down', seq: 7, error_code: 'dial tcp: connection refused', latency: 0 } },
    },
    'web-server': {
      name: 'web-server', target: 'https://example.com', type: 'http',
      consensus_state: 'up', scope: 'STANDALONE', classification: 'AMBIGUOUS',
      confidence: 1, affected_apps: [],
      by_node: { 'e2e-node': { state: 'up', seq: 1, error_code: '', latency: 0.055 } },
    },
  },
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
  await page.route(`${base}/auth/whoami`,  r => r.fulfill({ json: { role: 'admin' } }))
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
