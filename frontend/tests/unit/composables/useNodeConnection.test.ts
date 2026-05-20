/**
 * useNodeConnection tests.
 *
 * The composable uses Nuxt's $fetch, useRuntimeConfig, and Pinia stores.
 * We stub the globals in tests/setup.ts and set up a real Pinia instance
 * for each test.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useNodesStore } from '~/stores/nodes'

// The composable calls $fetch internally — we control it via the global stub.
// tests/setup.ts already stubs $fetch globally; we just reconfigure it here.

describe('selectActiveNode', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked($fetch).mockReset()
  })

  it('returns null when no nodes configured', async () => {
    // Dynamically import to pick up fresh module after Pinia is set
    const { useNodeConnection } = await import('~/composables/useNodeConnection')
    const { selectActiveNode } = useNodeConnection()
    const result = await selectActiveNode()
    expect(result).toBeNull()
  })

  it('selects responding node', async () => {
    const nodes = useNodesStore()
    nodes.addNode('http://a:10240')
    vi.mocked($fetch).mockResolvedValueOnce({ status: 'ok' })
    const { useNodeConnection } = await import('~/composables/useNodeConnection')
    const { selectActiveNode } = useNodeConnection()
    const result = await selectActiveNode()
    expect(result).toBe('http://a:10240')
    expect(nodes.active).toBe('http://a:10240')
  })

  it('marks node unhealthy on failure', async () => {
    const nodes = useNodesStore()
    nodes.addNode('http://a:10240')
    vi.mocked($fetch).mockRejectedValueOnce(new Error('down'))
    const { useNodeConnection } = await import('~/composables/useNodeConnection')
    const { selectActiveNode } = useNodeConnection()
    await selectActiveNode()
    expect(nodes.health['http://a:10240']).toBe('unhealthy')
  })

  it('falls back to second node when first fails', async () => {
    const nodes = useNodesStore()
    nodes.addNode('http://a:10240')
    nodes.addNode('http://b:10240')
    vi.mocked($fetch)
      .mockRejectedValueOnce(new Error('down'))   // a → fail
      .mockResolvedValueOnce({ status: 'ok' })    // b → ok
    const { useNodeConnection } = await import('~/composables/useNodeConnection')
    const { selectActiveNode } = useNodeConnection()
    const result = await selectActiveNode()
    expect(result).toBe('http://b:10240')
  })
})

describe('ensureActive', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked($fetch).mockReset()
  })

  it('returns cached active node without re-racing when healthy', async () => {
    const nodes = useNodesStore()
    nodes.addNode('http://a:10240')
    nodes.setActive('http://a:10240')
    nodes.markHealthy('http://a:10240')
    const { useNodeConnection } = await import('~/composables/useNodeConnection')
    const { ensureActive } = useNodeConnection()
    const result = await ensureActive()
    expect(result).toBe('http://a:10240')
    expect(vi.mocked($fetch)).not.toHaveBeenCalled()
  })

  it('triggers re-race when active is null', async () => {
    const nodes = useNodesStore()
    nodes.addNode('http://b:10240')
    vi.mocked($fetch).mockResolvedValueOnce({ status: 'ok' })
    const { useNodeConnection } = await import('~/composables/useNodeConnection')
    const { ensureActive } = useNodeConnection()
    const result = await ensureActive()
    expect(result).toBe('http://b:10240')
  })
})

describe('seedFromEnv', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('does not add node if one already exists', async () => {
    const nodes = useNodesStore()
    nodes.addNode('http://existing:10240')
    const { useNodeConnection } = await import('~/composables/useNodeConnection')
    const { seedFromEnv } = useNodeConnection()
    seedFromEnv()
    expect(nodes.configured).toHaveLength(1)
  })
})
