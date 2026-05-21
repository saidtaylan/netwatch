/**
 * useAuth — Simple single-admin-token auth.
 *
 * This is a self-hosted application. Auth is intentionally minimal:
 *   - One admin token (from config.yaml admin.token)
 *   - Token verified against GET /auth/whoami
 *   - Token stored in localStorage via Pinia persist
 *   - No user registration, no sessions, no RBAC
 *   - Future: LDAP integration may add multi-user when needed
 */
import type { WhoAmIResponse } from '~/types/api'

export const useAuth = () => {
  const store = useAuthStore()
  const { ensureActive } = useNodeConnection()

  /**
   * Verify token against /auth/whoami.
   * Returns 'admin' when token matches, 'anonymous' when no auth is configured.
   * Throws on wrong token (401) or network error.
   */
  async function checkToken(baseUrl: string, token: string): Promise<'admin' | 'anonymous'> {
    const headers: Record<string, string> = {}
    if (token) headers['Authorization'] = `Bearer ${token}`
    const data = await $fetch<WhoAmIResponse>(`${baseUrl}/auth/whoami`, {
      headers,
      timeout: 5000,
    })
    return data.role
  }

  /** Connect + verify: call after selectActiveNode resolves. */
  async function login(token: string): Promise<void> {
    const baseUrl = await ensureActive()
    if (!baseUrl) throw new Error('No backend node reachable. Check the URL(s) and try again.')
    const role = await checkToken(baseUrl, token)
    store.setToken(token, role)
  }

  function logout() {
    store.logout()
    return navigateTo({ name: 'setup' })  // go back to setup, not login — single-user app
  }

  return {
    login,
    logout,
    checkToken,
    isAuthenticated: computed(() => store.isAuthenticated),
    isAdmin:         computed(() => store.isAdmin),
  }
}
