/**
 * auth.global — Route guard for JWT-based auth (B28).
 *
 * Public pages: /connect, /setup, /login, /reset-password
 * Everything else requires authentication + non-expired token.
 */

/** Decode JWT payload and return exp (unix seconds), or 0 if unparseable. */
function jwtExp(token: string): number {
  try {
    const parts = token.split('.')
    if (parts.length !== 3 || !parts[1]) return 0
    const payload = JSON.parse(atob(parts[1].replace(/-/g, '+').replace(/_/g, '/')))
    return typeof payload.exp === 'number' ? payload.exp : 0
  } catch {
    return 0
  }
}

export default defineNuxtRouteMiddleware((to) => {
  const publicPages = ['connect', 'setup', 'login', 'reset-password']
  if (publicPages.includes(to.name as string)) return

  const auth = useAuthStore()

  if (!auth.isAuthenticated) {
    return navigateTo({ name: 'connect' })
  }

  // Client-side expiry guard: if token is expired, logout immediately
  // instead of waiting for the first 401 from the backend.
  const exp = jwtExp(auth.token!)
  if (exp > 0 && Date.now() / 1000 > exp) {
    auth.logout()
    return navigateTo({ name: 'connect' })
  }
})
