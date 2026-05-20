/**
 * Mock netwatch backend server for e2e tests.
 *
 * Listens on port 19240 (same as playwright.config.ts BACKEND_URL default).
 * Provides minimal realistic responses so the UI can render.
 */
import http from 'node:http'

const ADMIN_TOKEN = process.env.ADMIN_TOKEN ?? 'test-token'
const PORT        = parseInt(process.env.MOCK_PORT ?? '19240')

const FLEET = {
  node_name:       'e2e-node',
  cluster_enabled: false,
  target_counts:   { up: 2, hard_down: 1, soft_down: 0, soft_up: 0, unknown: 0 },
  down_targets:    ['db-primary'],
  targets: {
    'api-gateway': {
      name: 'api-gateway', target: '127.0.0.1:8080', type: 'tcp',
      consensus_state: 'up', scope: 'STANDALONE', classification: 'AMBIGUOUS',
      confidence: 1, affected_apps: ['payment-app'], by_node: {
        'e2e-node': { state: 'up', seq: 3, error_code: '', latency: 0.012 }
      }
    },
    'db-primary': {
      name: 'db-primary', target: '127.0.0.1:5432', type: 'tcp',
      consensus_state: 'hard_down', scope: 'STANDALONE', classification: 'REAL_OUTAGE',
      confidence: 1, affected_apps: [], by_node: {
        'e2e-node': { state: 'hard_down', seq: 7, error_code: 'dial tcp: connection refused', latency: 0 }
      }
    },
    'web-server': {
      name: 'web-server', target: 'https://example.com', type: 'http',
      consensus_state: 'up', scope: 'STANDALONE', classification: 'AMBIGUOUS',
      confidence: 1, affected_apps: [], by_node: {
        'e2e-node': { state: 'up', seq: 1, error_code: '', latency: 0.055 }
      }
    },
  }
}

const ROUTES: Record<string, object | ((token: string | null) => object)> = {
  '/health':         { status: 'ok' },
  '/version':        { version: 'e2e-test', build_time: '' },
  '/auth/whoami':    (token) => token === ADMIN_TOKEN ? { role: 'admin' } : { role: 'anonymous' },
  '/status':         [{ name: 'api-gateway', target: '127.0.0.1:8080', type: 'tcp', status: 'up', seq: 3, error_code: '' }],
  '/fleet/status':   FLEET,
  '/topology':       { targets: {} },
  '/slo':            { targets: [] },
  '/cluster/state':  null,          // → 503
  '/cluster/config': null,          // → 503
  '/cluster/maintenance': [],
}

function extractToken(req: http.IncomingMessage): string | null {
  const auth = req.headers['authorization'] ?? ''
  return auth.startsWith('Bearer ') ? auth.slice(7) : null
}

export const mockBackend = http.createServer((req, res) => {
  const url  = req.url?.split('?')[0] ?? '/'
  const token = extractToken(req)

  // CORS preflight
  res.setHeader('Access-Control-Allow-Origin', '*')
  res.setHeader('Access-Control-Allow-Headers', 'Authorization, Content-Type')
  res.setHeader('Access-Control-Allow-Methods', 'GET, POST, PUT, DELETE, OPTIONS')
  if (req.method === 'OPTIONS') {
    res.writeHead(204); res.end(); return
  }

  res.setHeader('Content-Type', 'application/json')

  // /auth/whoami — dynamic based on token
  if (url === '/auth/whoami') {
    res.writeHead(200)
    res.end(JSON.stringify({ role: token === ADMIN_TOKEN ? 'admin' : 'anonymous' }))
    return
  }

  // Cluster endpoints → 503 (standalone mode)
  if (['/cluster/state', '/cluster/config', '/cluster/probers'].includes(url)) {
    res.writeHead(503)
    res.end(JSON.stringify({ error: 'cluster disabled' }))
    return
  }

  // PUT /cluster/maintenance → 200
  if (url === '/cluster/maintenance' && req.method === 'PUT') {
    let body = ''
    req.on('data', d => { body += d })
    req.on('end', () => {
      const payload = JSON.parse(body || '{}')
      res.writeHead(200)
      res.end(JSON.stringify({
        id:         'e2e-window-1',
        target_id:  payload.target_id ?? 'test',
        reason:     payload.reason ?? '',
        started_at: new Date().toISOString(),
        ends_at:    new Date(Date.now() + (payload.duration_ms ?? 3600000)).toISOString(),
        created_by: payload.created_by ?? 'ui',
      }))
    })
    return
  }

  // DELETE /cluster/maintenance/:id → 200
  if (url.startsWith('/cluster/maintenance/') && req.method === 'DELETE') {
    res.writeHead(200); res.end('{}'); return
  }

  const route = ROUTES[url]
  if (route !== undefined) {
    if (route === null) { res.writeHead(503); res.end('{"error":"disabled"}'); return }
    res.writeHead(200)
    res.end(JSON.stringify(typeof route === 'function' ? route(token) : route))
    return
  }

  res.writeHead(404)
  res.end(JSON.stringify({ error: 'not found' }))
})

// When run directly (not imported): node mock-backend.ts
// ESM: no require.main — check argv instead
if (process.argv[1]?.endsWith('mock-backend.ts') || process.argv[1]?.endsWith('mock-backend.js')) {
  mockBackend.listen(PORT, () => {
    console.log(`Mock netwatch backend running on http://localhost:${PORT}`)
  })
}
