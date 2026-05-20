import { defineStore } from 'pinia'

export const useUIStore = defineStore('ui', {
  state: () => ({
    pollingIntervalMs: 5000,
    sidebarCollapsed:  false,
    // Toast queue — managed by useToast composable
    toasts: [] as Array<{ id: string; type: 'success'|'error'|'warning'|'info'; message: string }>,
  }),
  actions: {
    setPollingInterval(ms: number) {
      this.pollingIntervalMs = Math.max(1000, Math.min(60000, ms))
    },
    toggleSidebar() {
      this.sidebarCollapsed = !this.sidebarCollapsed
    },
    addToast(type: 'success'|'error'|'warning'|'info', message: string, durationMs = 4000) {
      const id = `${Date.now()}-${Math.random()}`
      this.toasts.push({ id, type, message })
      setTimeout(() => this.removeToast(id), durationMs)
    },
    removeToast(id: string) {
      this.toasts = this.toasts.filter(t => t.id !== id)
    },
  },
  persist: {
    pick: ['pollingIntervalMs', 'sidebarCollapsed'],
  },
})
