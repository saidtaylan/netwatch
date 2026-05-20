import type { GeoLatencySnapshot } from '~/types/api'

export const useGeoLatency = (targetId: string) => {
  const api = useApi()

  const geo = usePolling<GeoLatencySnapshot>(
    () => api.get<GeoLatencySnapshot>(`/geo/latency/${encodeURIComponent(targetId)}`),
    { intervalMs: 10000 }
  )

  return { geo }
}
