import type { WhoAmIResponse } from '../../types/api'

export const useAuth = () => {
  const store   = useAuthStore()
  const nodes   = useNodesStore()
  const { ensureActive } = useNodeConnection()

  /** Check token against /auth/whoami. Returns role or throws. */
  async function checkToken(baseUrl: string, token: string): Promise<'admin' | 'anonymous'> {
    const data = await $fetch<WhoAmIResponse>(`${baseUrl}/auth/whoami`, {
      headers: { Authorization: `Bearer ${token}` },
      timeout: 5000,
    })
    return data.role
  }

  /** Full login: set token, verify, store role */
  async function login(token: string): Promise<void> {
    const baseUrl = await ensureActive()
    if (!baseUrl) throw new Error('No backend node reachable')
    const role = await checkToken(baseUrl, token)
    store.setToken(token, role)
  }

  function logout() {
    store.logout()
    return navigateTo('/login')
  }

  return { login, logout, checkToken, isAuthenticated: computed(() => store.isAuthenticated) }
}
