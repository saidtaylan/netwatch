import { defineStore } from 'pinia'

export type NodeHealth = 'healthy' | 'unhealthy' | 'unknown'

export interface BackendNode {
  url:    string
  label?: string
}

// useNodesStore holds the list of configured backend node URLs, which one is
// currently active, and each node's health. It is persisted to localStorage so
// the user doesn't re-enter URLs, and drives multi-node failover.
export const useNodesStore = defineStore('nodes', {
  state: () => ({
    configured:    [] as BackendNode[],
    active:        null as string | null,   // URL of currently active node
    health:        {} as Record<string, NodeHealth>,
    lastCheckedAt: null as number | null,
  }),
  getters: {
    /** The active node URL, falling back to the first configured node. */
    activeUrl:    (state) => state.active ?? state.configured[0]?.url ?? null,
    /** Configured nodes excluding those marked unhealthy. */
    healthyNodes: (state) => state.configured.filter(n => state.health[n.url] !== 'unhealthy'),
  },
  actions: {
    /** Adds a node (trailing slash stripped) if not already present, marking it unknown-health. */
    addNode(url: string, label?: string) {
      const normalized = url.replace(/\/$/, '')
      if (!this.configured.find(n => n.url === normalized)) {
        this.configured.push({ url: normalized, label })
        this.health[normalized] = 'unknown'
      }
    },
    /** Removes a node; if it was active, fails over to the first remaining node. */
    removeNode(url: string) {
      this.configured = this.configured.filter(n => n.url !== url)
      delete this.health[url]
      if (this.active === url) {
        this.active = this.configured[0]?.url ?? null
      }
    },
    /** Marks a node as the active one and records the check time. */
    setActive(url: string) {
      this.active = url
      this.lastCheckedAt = Date.now()
    },
    /** Marks a node healthy. */
    markHealthy(url: string) {
      this.health[url] = 'healthy'
    },
    /** Marks a node unhealthy; if it was active, fails over to the next healthy node. */
    markUnhealthy(url: string) {
      this.health[url] = 'unhealthy'
      if (this.active === url) {
        this.active = this.configured.find(n => this.health[n.url] !== 'unhealthy')?.url ?? null
      }
    },
    /** Clears all nodes and health (used on full disconnect/logout). */
    reset() {
      this.configured = []
      this.active = null
      this.health = {}
      this.lastCheckedAt = null
    },
  },
  persist: true,
})
