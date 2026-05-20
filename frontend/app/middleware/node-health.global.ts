export default defineNuxtRouteMiddleware(async (to) => {
  const publicRoutes = ['/setup', '/login']
  if (publicRoutes.includes(to.path)) return

  const nodes = useNodesStore()
  if (nodes.configured.length === 0) {
    const { seedFromEnv } = useNodeConnection()
    seedFromEnv()
  }

  if (nodes.configured.length === 0) {
    return navigateTo('/setup')
  }

  // Ensure active node is reachable (fast path — only re-race if no active)
  if (!nodes.active) {
    const { selectActiveNode } = useNodeConnection()
    const winner = await selectActiveNode()
    if (!winner) {
      // All nodes unreachable — let page handle gracefully, don't redirect
    }
  }
})
