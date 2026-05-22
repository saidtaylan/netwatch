/**
 * useSLO — Poll /slo with breach detection.
 *
 * On every poll, compares current snapshot to previous and pushes AlertEntries
 * to the alerts store for:
 *   - SLO breach started (slo_breached: false → true)
 *   - SLO recovered  (slo_breached: true → false)
 *
 * This is the B17 frontend-only quick fix. The proper B25 backend solution
 * will replace this with a persistent /alerts endpoint that includes SLO
 * events, state changes, and maintenance events in a unified stream.
 */
import type { SLOSnapshot, AlertEntry } from '~/types/api'

export const useSLO = () => {
  const api    = useApi()
  const alerts = useAlertsStore()

  // Track previous breach state per target id
  const prevBreached = ref<Record<string, boolean>>({})

  const slo = usePolling<SLOSnapshot>(
    async () => {
      const data = await api.get<SLOSnapshot>('/slo')
      detectBreaches(data)
      return data
    },
    { intervalMs: 60000 }
  )

  function detectBreaches(snapshot: SLOSnapshot) {
    if (!snapshot.targets) return
    for (const [id, t] of Object.entries(snapshot.targets)) {
      const prev = prevBreached.value[id]
      const curr = t.slo_breached

      if (prev !== undefined && prev !== curr) {
        // State transition — generate AlertEntry
        const alert: AlertEntry = {
          id:             `slo-${id}-${Date.now()}`,
          target_id:      id,
          target_name:    t.target_id,   // SLO snapshot doesn't carry display name
          target_type:    'http',         // unknown at this layer — placeholder
          // Re-use existing AlertEntry status field with semantic SLO meaning:
          status:         curr ? 'unreachable' : 'reachable',
          scope:          'STANDALONE',
          classification: 'AMBIGUOUS',
          confidence:     1,
          seq:            0,
          error_code:     curr
            ? `SLO breached: ${(t.actual_uptime * 100).toFixed(2)}% < ${(t.target_uptime * 100).toFixed(2)}% (window=${t.window})`
            : `SLO recovered: ${(t.actual_uptime * 100).toFixed(2)}% ≥ ${(t.target_uptime * 100).toFixed(2)}%`,
          timestamp:      new Date().toISOString(),
        }
        alerts.push(alert)
      }
      prevBreached.value[id] = curr
    }
  }

  return { slo }
}
