/**
 * useNodeConnection — Multi-node race + failover
 *
 * On mount or active-node failure:
 *   1. Race all configured nodes with GET /health
 *   2. Fastest healthy response wins
 *   3. Result stored in nodesStore.active
 *   4. On API error → markUnhealthy(active) → selectActiveNode() → retry once
 */
export const useNodeConnection = () => {
  const nodes  = useNodesStore()
  const config = useRuntimeConfig()

  // selectActiveNode races a /health request against all configured nodes and
  // picks the first to respond (Promise.any), marking it active. Returns its URL,
  // or null when no node is configured or reachable. This is the multi-node
  // failover entry point.
  async function selectActiveNode(): Promise<string | null> {
    const candidates = nodes.configured
    if (candidates.length === 0) return null

    const races = candidates.map(({ url }) =>
      $fetch<{ status: string }>(`${url}/health`, {
        timeout: 3000,
        onResponseError: () => { nodes.markUnhealthy(url) },
      })
        .then(() => { nodes.markHealthy(url); return url })
        .catch(() => { nodes.markUnhealthy(url); return Promise.reject(url) })
    )

    try {
      const winner = await Promise.any(races)
      nodes.setActive(winner)
      return winner
    } catch {
      return null
    }
  }

  /** Ensure active node is set; if not, run race */
  async function ensureActive(): Promise<string | null> {
    if (nodes.active && nodes.health[nodes.active] !== 'unhealthy') {
      return nodes.active
    }
    return selectActiveNode()
  }

  /** Add discovered cluster members to node list (opt-in) */
  function suggestDiscoveredNodes(addrs: string[]) {
    for (const addr of addrs) {
      // Infer port — backend defaults to 10240
      const url = addr.startsWith('http') ? addr : `http://${addr}`
      nodes.addNode(url)
    }
  }

  /** Seed default backend URL from env/config if no nodes configured yet */
  function seedFromEnv() {
    const defaultUrl = config.public.defaultBackendUrl as string
    if (defaultUrl && nodes.configured.length === 0) {
      nodes.addNode(defaultUrl)
    }
  }

  return { selectActiveNode, ensureActive, suggestDiscoveredNodes, seedFromEnv }
}
