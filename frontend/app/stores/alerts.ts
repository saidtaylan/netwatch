import { defineStore } from 'pinia'
import type { AlertEntry } from '~/types/api'

const MAX_ALERTS = 100

export const useAlertsStore = defineStore('alerts', {
  state: () => ({
    items: [] as AlertEntry[],
  }),
  getters: {
    recent: (state) => state.items.slice(0, 20),
    unresolvedCount: (state) => state.items.filter(a => a.status === 'unreachable' && !a.acked).length,
  },
  actions: {
    push(alert: AlertEntry) {
      // Deduplicate by target_id + seq
      const exists = this.items.some(a => a.target_id === alert.target_id && a.seq === alert.seq)
      if (!exists) {
        this.items.unshift(alert)
        if (this.items.length > MAX_ALERTS) this.items.pop()
      }
    },
    ack(id: string) {
      const a = this.items.find(x => x.id === id)
      if (a) a.acked = true
    },
    mute(id: string) {
      const a = this.items.find(x => x.id === id)
      if (a) a.muted = true
    },
    clear() {
      this.items = []
    },
  },
  // Not persisted — in-memory only until B7 alert history backend
})
