import type { MaintenanceWindow } from '~/types/api'

export const useMaintenance = () => {
  const api = useApi()
  const ui  = useUIStore()

  const windows = usePolling<MaintenanceWindow[]>(
    () => api.get<MaintenanceWindow[]>('/cluster/maintenance'),
    { intervalMs: 15000 }
  )

  async function create(targetId: string, durationMs: number, reason: string, createdBy = 'ui') {
    try {
      await api.put('/cluster/maintenance', {
        target_id:   targetId,
        duration_ms: durationMs,
        reason,
        created_by:  createdBy,
      })
      ui.addToast('success', `Maintenance set for ${targetId}`)
      await windows.refresh()
    } catch (e: any) {
      ui.addToast('error', `Failed to set maintenance: ${e?.message ?? 'unknown error'}`)
      throw e
    }
  }

  async function cancel(id: string) {
    try {
      await api.del(`/cluster/maintenance/${id}`)
      ui.addToast('success', 'Maintenance window cancelled')
      await windows.refresh()
    } catch (e: any) {
      ui.addToast('error', `Failed to cancel maintenance: ${e?.message ?? 'unknown error'}`)
      throw e
    }
  }

  const active = computed<MaintenanceWindow[]>(() => {
    const now = new Date()
    return (windows.data.value ?? []).filter(w => new Date(w.ends_at) > now)
  })

  return { windows, active, create, cancel }
}
