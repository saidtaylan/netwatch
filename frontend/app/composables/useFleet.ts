import type { FleetSnapshot, FleetTarget } from '~/types/api'
import type { AlertEntry } from '~/types/api'

/** Polls the cluster-wide fleet view (/fleet/status) and exposes it as reactive
 * helpers: the raw snapshot, a target list and id→target index, quorum/isolation
 * flags, summary counts, and the list of down target ids. It also synthesises
 * client-side alert-feed entries whenever a target's consensus state changes. */
export const useFleet = () => {
  const api    = useApi()
  const alerts = useAlertsStore()

  const prevStates = ref<Record<string, string>>({})

  const fleet = usePolling<FleetSnapshot>(
    async () => {
      const data = await api.get<FleetSnapshot>('/fleet/status')
      detectStateChanges(data)
      return data
    }
  )

  // detectStateChanges compares each target's consensus state against the
  // previous poll and pushes an in-memory alert-feed entry on any transition,
  // then records the new state. This is the UI's local alert feed (the backend
  // owns the real notifications).
  function detectStateChanges(snapshot: FleetSnapshot) {
    if (!snapshot.targets) return
    // targets is an array — iterate by id
    for (const t of snapshot.targets) {
      const id   = t.id
      const prev = prevStates.value[id]
      const curr = t.consensus_state
      if (prev && prev !== curr) {
        const alert: AlertEntry = {
          id:             `${id}-${Date.now()}`,
          target_id:      id,
          target_name:    t.name,
          target_type:    t.type,
          status:         (curr === 'up' || curr === 'soft_up') ? 'reachable' : 'unreachable',
          scope:          t.scope ?? 'STANDALONE',
          classification: t.classification ?? 'AMBIGUOUS',
          confidence:     t.confidence ?? 0,
          seq:            t.by_node ? Object.values(t.by_node)[0]?.seq ?? 0 : 0,
          error_code:     t.by_node ? Object.values(t.by_node)[0]?.error_code : undefined,
          affected_apps:  t.affected_apps,
          timestamp:      new Date().toISOString(),
        }
        alerts.push(alert)
      }
      prevStates.value[id] = curr
    }
  }

  // targets as flat list
  const targetList = computed<FleetTarget[]>(() => fleet.data.value?.targets ?? [])

  // targets as id→target index (for O(1) lookup in topology/detail pages)
  const targetIndex = computed<Record<string, FleetTarget>>(() => {
    const idx: Record<string, FleetTarget> = {}
    for (const t of (fleet.data.value?.targets ?? [])) idx[t.id] = t
    return idx
  })

  /** O(1) lookup of a fleet target by id, or null when not present. */
  const targetById = (id: string): FleetTarget | null => targetIndex.value[id] ?? null

  // Cluster info shortcuts (null in standalone mode)
  const quorumHealthy = computed(() => fleet.data.value?.cluster?.quorum_healthy ?? null)
  const isolated      = computed(() => fleet.data.value?.cluster?.isolated ?? false)
  const memberNames   = computed(() => fleet.data.value?.cluster?.members ?? [])

  // Summary counts
  const counts = computed(() => fleet.data.value?.summary ?? { up: 0, hard_down: 0, soft_down: 0, unknown: 0 })

  // Currently down target ids
  const downTargetIds = computed(() =>
    (fleet.data.value?.targets ?? [])
      .filter(t => t.consensus_state === 'hard_down' || t.consensus_state === 'soft_down')
      .map(t => t.id)
  )

  return { fleet, targetList, targetIndex, targetById, quorumHealthy, isolated, memberNames, counts, downTargetIds }
}
