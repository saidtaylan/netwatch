/**
 * useAuth — JWT-based multi-user auth (B28).
 *
 * Auth flow:
 *   1. /connect → enter node URLs
 *   2. GET /auth/status → setup_completed?
 *      - false → /setup (setup_token + create admin user)
 *      - true  → /login (username + password)
 *   3. JWT stored in auth store, sent as Bearer token
 */
import type { AuthStatusResponse, AuthLoginResponse } from '~/types/api'

export const useAuth = () => {
  const store = useAuthStore()
  const nodes = useNodesStore()
  const { ensureActive } = useNodeConnection()

  /** Check if backend has completed initial setup */
  async function checkStatus(baseUrl: string): Promise<AuthStatusResponse> {
    return $fetch<AuthStatusResponse>(`${baseUrl}/auth/status`, { timeout: 5000 })
  }

  /** POST /auth/setup — create first admin user */
  async function setup(setupToken: string, username: string, password: string, displayName?: string): Promise<AuthLoginResponse> {
    const baseUrl = await ensureActive()
    if (!baseUrl) throw new Error('No backend node reachable.')

    const nodeUrls = nodes.configured.map(n => n.url)
    const resp = await $fetch<AuthLoginResponse>(`${baseUrl}/auth/setup`, {
      method: 'POST',
      body: {
        setup_token: setupToken,
        username,
        password,
        display_name: displayName || undefined,
        node_urls: nodeUrls,
      },
      timeout: 10000,
    })
    store.setAuth(resp.token, resp.user)
    return resp
  }

  /** POST /auth/login — username + password → JWT */
  async function login(username: string, password: string): Promise<AuthLoginResponse> {
    const baseUrl = await ensureActive()
    if (!baseUrl) throw new Error('No backend node reachable.')

    const resp = await $fetch<AuthLoginResponse>(`${baseUrl}/auth/login`, {
      method: 'POST',
      body: { username, password },
      timeout: 10000,
    })
    store.setAuth(resp.token, resp.user)

    // If the backend returned cluster nodes, update the nodes store
    if (resp.cluster_nodes?.length) {
      for (const url of resp.cluster_nodes) {
        nodes.addNode(url)
      }
    }

    return resp
  }

  function logout() {
    store.logout()
    return navigateTo({ name: 'connect' })
  }

  return {
    checkStatus,
    setup,
    login,
    logout,
    isAuthenticated: computed(() => store.isAuthenticated),
    isAdmin: computed(() => store.isAdmin),
    role: computed(() => store.role),
    username: computed(() => store.username),
  }
}
