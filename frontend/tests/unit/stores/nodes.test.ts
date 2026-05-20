import { describe, it, expect, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useNodesStore } from '~/stores/nodes'

describe('useNodesStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('starts empty', () => {
    const store = useNodesStore()
    expect(store.configured).toHaveLength(0)
    expect(store.active).toBeNull()
    expect(store.activeUrl).toBeNull()
  })

  it('addNode adds a node and normalises trailing slash', () => {
    const store = useNodesStore()
    store.addNode('http://localhost:10240/')
    expect(store.configured).toHaveLength(1)
    expect(store.configured[0].url).toBe('http://localhost:10240')
  })

  it('addNode does not add duplicates', () => {
    const store = useNodesStore()
    store.addNode('http://localhost:10240')
    store.addNode('http://localhost:10240')
    expect(store.configured).toHaveLength(1)
  })

  it('addNode sets label when provided', () => {
    const store = useNodesStore()
    store.addNode('http://localhost:10240', 'node-1')
    expect(store.configured[0].label).toBe('node-1')
  })

  it('setActive sets active URL and timestamp', () => {
    const store = useNodesStore()
    store.addNode('http://a:10240')
    store.setActive('http://a:10240')
    expect(store.active).toBe('http://a:10240')
    expect(store.lastCheckedAt).toBeGreaterThan(0)
  })

  it('markHealthy and markUnhealthy update health map', () => {
    const store = useNodesStore()
    store.addNode('http://a:10240')
    expect(store.health['http://a:10240']).toBe('unknown')
    store.markHealthy('http://a:10240')
    expect(store.health['http://a:10240']).toBe('healthy')
    store.markUnhealthy('http://a:10240')
    expect(store.health['http://a:10240']).toBe('unhealthy')
  })

  it('markUnhealthy clears active if it was the active node', () => {
    const store = useNodesStore()
    store.addNode('http://a:10240')
    store.addNode('http://b:10240')
    store.setActive('http://a:10240')
    store.markUnhealthy('http://a:10240')
    // active should switch to first healthy node (b)
    expect(store.active).toBe('http://b:10240')
  })

  it('markUnhealthy with no fallback sets active to null', () => {
    const store = useNodesStore()
    store.addNode('http://a:10240')
    store.setActive('http://a:10240')
    store.markUnhealthy('http://a:10240')
    expect(store.active).toBeNull()
  })

  it('removeNode removes from configured and clears active if removed', () => {
    const store = useNodesStore()
    store.addNode('http://a:10240')
    store.setActive('http://a:10240')
    store.removeNode('http://a:10240')
    expect(store.configured).toHaveLength(0)
    expect(store.active).toBeNull()
  })

  it('healthyNodes filters out unhealthy entries', () => {
    const store = useNodesStore()
    store.addNode('http://a:10240')
    store.addNode('http://b:10240')
    store.markUnhealthy('http://a:10240')
    expect(store.healthyNodes).toHaveLength(1)
    expect(store.healthyNodes[0].url).toBe('http://b:10240')
  })

  it('reset clears everything', () => {
    const store = useNodesStore()
    store.addNode('http://a:10240')
    store.setActive('http://a:10240')
    store.reset()
    expect(store.configured).toHaveLength(0)
    expect(store.active).toBeNull()
    expect(store.health).toEqual({})
  })
})
