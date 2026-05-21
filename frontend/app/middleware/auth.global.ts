/**
 * auth.global — Redirect unauthenticated users to /setup.
 *
 * Single-token self-hosted app: no login page separate from setup.
 * /setup handles both first-time connection and re-authentication.
 */
export default defineNuxtRouteMiddleware((to) => {
  if (to.name === 'setup') return

  const auth = useAuthStore()
  if (!auth.isAuthenticated) {
    return navigateTo({ name: 'setup' })
  }
})
