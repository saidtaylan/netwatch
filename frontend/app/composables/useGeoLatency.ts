import type { GeoLatencySnapshot } from '~/types/api'

/** Polls per-node geo latency for one target (/geo/latency/{id}) every 10 s,
 * returning a reactive snapshot with per-region latency and the anomaly flag. */
export const useGeoLatency = (targetId: string) => {
  const api = useApi()

  const geo = usePolling<GeoLatencySnapshot>(
    () => api.get<GeoLatencySnapshot>(`/geo/latency/${encodeURIComponent(targetId)}`),
    { intervalMs: 10000 }
  )

  return { geo }
}
