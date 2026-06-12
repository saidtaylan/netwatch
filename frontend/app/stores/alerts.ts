import { defineStore } from 'pinia'
import type { AlertEntry } from '~/types/api'

const MAX_ALERTS = 100

// useAlertsStore is the in-memory, capped (MAX_ALERTS) ring buffer that backs the
// UI alert feed. Entries are synthesised client-side from fleet/SLO state changes
// (the backend owns the real notifications); not persisted across reloads.
export const useAlertsStore = defineStore('alerts', {
  state: () => ({
    items: [] as AlertEntry[],
  }),
  getters: {
    /** The 20 most recent alerts. */
    recent: (state) => state.items.slice(0, 20),
    /** Count of unreachable, un-acked alerts (the "needs attention" badge). */
    unresolvedCount: (state) => state.items.filter(a => a.status === 'unreachable' && !a.acked).length,
  },
  actions: {
    /** Prepends an alert, de-duplicating by (target_id, seq) and trimming to MAX_ALERTS. */
    push(alert: AlertEntry) {
      // Deduplicate by target_id + seq
      const exists = this.items.some(a => a.target_id === alert.target_id && a.seq === alert.seq)
      if (!exists) {
        this.items.unshift(alert)
        if (this.items.length > MAX_ALERTS) this.items.pop()
      }
    },
    /** Marks an alert acknowledged by id. */
    ack(id: string) {
      const a = this.items.find(x => x.id === id)
      if (a) a.acked = true
    },
    /** Marks an alert muted by id. */
    mute(id: string) {
      const a = this.items.find(x => x.id === id)
      if (a) a.muted = true
    },
    /** Empties the alert feed. */
    clear() {
      this.items = []
    },
  },
  // Not persisted — in-memory only until B7 alert history backend
})
