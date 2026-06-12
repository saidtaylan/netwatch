import { defineStore } from 'pinia'

// useUIStore holds browser-local UI preferences (poll interval, sidebar state)
// and the transient toast queue. The two preferences are persisted; toasts are
// not. Note: pollingIntervalMs only affects this browser tab.
export const useUIStore = defineStore('ui', {
  state: () => ({
    pollingIntervalMs: 5000,
    sidebarCollapsed:  false,
    // Toast queue — managed by useToast composable
    toasts: [] as Array<{ id: string; type: 'success'|'error'|'warning'|'info'; message: string }>,
  }),
  actions: {
    /** Sets the global poll interval, clamped to 1–60 s. */
    setPollingInterval(ms: number) {
      this.pollingIntervalMs = Math.max(1000, Math.min(60000, ms))
    },
    /** Toggles the sidebar collapsed/expanded state. */
    toggleSidebar() {
      this.sidebarCollapsed = !this.sidebarCollapsed
    },
    /** Pushes a toast and auto-removes it after durationMs. */
    addToast(type: 'success'|'error'|'warning'|'info', message: string, durationMs = 4000) {
      const id = `${Date.now()}-${Math.random()}`
      this.toasts.push({ id, type, message })
      setTimeout(() => this.removeToast(id), durationMs)
    },
    /** Removes a toast by id. */
    removeToast(id: string) {
      this.toasts = this.toasts.filter(t => t.id !== id)
    },
  },
  persist: {
    pick: ['pollingIntervalMs', 'sidebarCollapsed'],
  },
})
