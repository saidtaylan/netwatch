import type { FleetSnapshot, FleetTarget } from '~/types/api'
import type { AlertEntry } from '~/types/api'

export const useFleet = () => {
  const api    = useApi()
  const alerts = useAlertsStore()

  // Track previous state for change detection (alert generation)
  const prevStates = ref<Record<string, string>>({})

  const fleet = usePolling<FleetSnapshot>(
    async () => {
      const data = await api.get<FleetSnapshot>('/fleet/status')
      detectStateChanges(data)
      return data
    }
  )

  function detectStateChanges(snapshot: FleetSnapshot) {
    if (!snapshot.targets) return
    for (const [id, t] of Object.entries(snapshot.targets)) {
      const prev = prevStates.value[id]
      const curr = t.consensus_state
      if (prev && prev !== curr) {
        // State changed → push to alert store
        const alert: AlertEntry = {
          id:             `${id}-${t.by_node ? Object.values(t.by_node)[0]?.seq ?? Date.now() : Date.now()}`,
          target_id:      id,
          target_name:    t.name,
          target_type:    t.type,
          status:         (curr === 'up' || curr === 'soft_up') ? 'reachable' : 'unreachable',
          scope:          t.scope,
          classification: t.classification,
          confidence:     t.confidence,
          seq:            Object.values(t.by_node ?? {})[0]?.seq ?? 0,
          error_code:     Object.values(t.by_node ?? {})[0]?.error_code,
          affected_apps:  t.affected_apps,
          timestamp:      new Date().toISOString(),
        }
        alerts.push(alert)
      }
      prevStates.value[id] = curr
    }
  }

  const targetList = computed<FleetTarget[]>(() => {
    if (!fleet.data.value?.targets) return []
    return Object.values(fleet.data.value.targets)
  })

  const targetById = (id: string): FleetTarget | null => {
    return fleet.data.value?.targets?.[id] ?? null
  }

  return { fleet, targetList, targetById }
}
