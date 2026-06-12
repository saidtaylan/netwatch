/**
 * node-health.global — runs before every non-public route. It seeds backend
 * node URLs from env on first load, redirects to /connect when none are
 * configured, and (when there is no active node yet) races the configured nodes
 * to pick a reachable one. If all are unreachable it lets the page render and
 * handle the error rather than redirecting.
 */
export default defineNuxtRouteMiddleware(async (to) => {
  const publicRoutes = ['connect', 'setup', 'login']
  if (publicRoutes.includes(to.name as string)) return

  const nodes = useNodesStore()
  if (nodes.configured.length === 0) {
    const { seedFromEnv } = useNodeConnection()
    seedFromEnv()
  }

  if (nodes.configured.length === 0) {
    return navigateTo({ name: 'connect' })
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
