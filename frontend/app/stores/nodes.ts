import { defineStore } from 'pinia'

export type NodeHealth = 'healthy' | 'unhealthy' | 'unknown'

export interface BackendNode {
  url:    string
  label?: string
}

export const useNodesStore = defineStore('nodes', {
  state: () => ({
    configured:    [] as BackendNode[],
    active:        null as string | null,   // URL of currently active node
    health:        {} as Record<string, NodeHealth>,
    lastCheckedAt: null as number | null,
  }),
  getters: {
    activeUrl:    (state) => state.active ?? state.configured[0]?.url ?? null,
    healthyNodes: (state) => state.configured.filter(n => state.health[n.url] !== 'unhealthy'),
  },
  actions: {
    addNode(url: string, label?: string) {
      const normalized = url.replace(/\/$/, '')
      if (!this.configured.find(n => n.url === normalized)) {
        this.configured.push({ url: normalized, label })
        this.health[normalized] = 'unknown'
      }
    },
    removeNode(url: string) {
      this.configured = this.configured.filter(n => n.url !== url)
      delete this.health[url]
      if (this.active === url) {
        this.active = this.configured[0]?.url ?? null
      }
    },
    setActive(url: string) {
      this.active = url
      this.lastCheckedAt = Date.now()
    },
    markHealthy(url: string) {
      this.health[url] = 'healthy'
    },
    markUnhealthy(url: string) {
      this.health[url] = 'unhealthy'
      if (this.active === url) {
        this.active = this.configured.find(n => this.health[n.url] !== 'unhealthy')?.url ?? null
      }
    },
    reset() {
      this.configured = []
      this.active = null
      this.health = {}
      this.lastCheckedAt = null
    },
  },
  persist: true,
})
