import type { TopologySnapshot } from '~/types/api'

/** Polls the dependency graph (/topology) every 30 s, returning a reactive
 * snapshot of each target's deps, reverse deps and cascading impact. */
export const useTopology = () => {
  const api = useApi()

  const topology = usePolling<TopologySnapshot>(
    () => api.get<TopologySnapshot>('/topology'),
    { intervalMs: 30000 }
  )

  return { topology }
}
