import type { TopologySnapshot } from '~/types/api'

export const useTopology = () => {
  const api = useApi()

  const topology = usePolling<TopologySnapshot>(
    () => api.get<TopologySnapshot>('/topology'),
    { intervalMs: 30000 }
  )

  return { topology }
}
