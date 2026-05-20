import type { SLOSnapshot } from '~/types/api'

export const useSLO = () => {
  const api = useApi()

  const slo = usePolling<SLOSnapshot>(
    () => api.get<SLOSnapshot>('/slo'),
    { intervalMs: 60000 }
  )

  return { slo }
}
