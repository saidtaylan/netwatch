export default defineNuxtRouteMiddleware((to) => {
  // Skip auth check on setup/login pages
  const publicRoutes = ['/setup', '/login']
  if (publicRoutes.includes(to.path)) return

  const auth = useAuthStore()
  if (!auth.isAuthenticated) {
    return navigateTo('/setup')
  }
})
