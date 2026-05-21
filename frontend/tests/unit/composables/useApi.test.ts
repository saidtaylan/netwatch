/**
 * useApi tests — $fetch wrapper with auth + failover.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '~/stores/auth'
import { useNodesStore } from '~/stores/nodes'

describe('useApi.get', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked($fetch).mockReset()
  })

  it('calls the active node with given path', async () => {
    const nodes = useNodesStore()
    nodes.addNode('http://node-a:10240')
    nodes.setActive('http://node-a:10240')
    nodes.markHealthy('http://node-a:10240')

    vi.mocked($fetch).mockResolvedValueOnce({ status: 'ok' })

    const { useApi } = await import('~/composables/useApi')
    const api = useApi()
    await api.get('/health')

    expect(vi.mocked($fetch)).toHaveBeenCalledWith(
      'http://node-a:10240/health',
      expect.objectContaining({ method: 'GET' })
    )
  })

  it('injects Authorization Bearer header when token is set', async () => {
    const auth = useAuthStore()
    const nodes = useNodesStore()
    nodes.addNode('http://node-a:10240')
    nodes.setActive('http://node-a:10240')
    nodes.markHealthy('http://node-a:10240')
    auth.setToken('my-token', 'admin')

    vi.mocked($fetch).mockResolvedValueOnce({})

    const { useApi } = await import('~/composables/useApi')
    const api = useApi()
    await api.get('/fleet/status')

    const opts = vi.mocked($fetch).mock.calls[0][1] as any
    expect(opts.headers.Authorization).toBe('Bearer my-token')
  })

  it('omits Authorization header when no token', async () => {
    const nodes = useNodesStore()
    nodes.addNode('http://node-a:10240')
    nodes.setActive('http://node-a:10240')
    nodes.markHealthy('http://node-a:10240')

    vi.mocked($fetch).mockResolvedValueOnce({})

    const { useApi } = await import('~/composables/useApi')
    const api = useApi()
    await api.get('/fleet/status')

    const opts = vi.mocked($fetch).mock.calls[0][1] as any
    expect(opts.headers.Authorization).toBeUndefined()
  })

  it('throws when no backend node available', async () => {
    const { useApi } = await import('~/composables/useApi')
    const api = useApi()
    await expect(api.get('/anything')).rejects.toThrow(/No backend node available/)
  })

  it('returns response data on success', async () => {
    const nodes = useNodesStore()
    nodes.addNode('http://node-a:10240')
    nodes.setActive('http://node-a:10240')
    nodes.markHealthy('http://node-a:10240')

    const expected = { node_name: 'a', cluster_enabled: false }
    vi.mocked($fetch).mockResolvedValueOnce(expected)

    const { useApi } = await import('~/composables/useApi')
    const api = useApi()
    const result = await api.get('/fleet/status')
    expect(result).toEqual(expected)
  })
})

describe('useApi method helpers', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked($fetch).mockReset()

    const nodes = useNodesStore()
    nodes.addNode('http://node-a:10240')
    nodes.setActive('http://node-a:10240')
    nodes.markHealthy('http://node-a:10240')
  })

  it('post sends method=POST with body', async () => {
    vi.mocked($fetch).mockResolvedValueOnce({})
    const { useApi } = await import('~/composables/useApi')
    const api = useApi()
    await api.post('/x', { foo: 'bar' })
    const opts = vi.mocked($fetch).mock.calls[0][1] as any
    expect(opts.method).toBe('POST')
    expect(opts.body).toEqual({ foo: 'bar' })
  })

  it('put sends method=PUT with body', async () => {
    vi.mocked($fetch).mockResolvedValueOnce({})
    const { useApi } = await import('~/composables/useApi')
    const api = useApi()
    await api.put('/x', { foo: 'bar' })
    const opts = vi.mocked($fetch).mock.calls[0][1] as any
    expect(opts.method).toBe('PUT')
  })

  it('del sends method=DELETE', async () => {
    vi.mocked($fetch).mockResolvedValueOnce({})
    const { useApi } = await import('~/composables/useApi')
    const api = useApi()
    await api.del('/x/1')
    const opts = vi.mocked($fetch).mock.calls[0][1] as any
    expect(opts.method).toBe('DELETE')
  })
})

describe('useApi error handling', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked($fetch).mockReset()
  })

  it('marks node unhealthy and tries failover on network error', async () => {
    const nodes = useNodesStore()
    nodes.addNode('http://node-a:10240')
    nodes.addNode('http://node-b:10240')
    nodes.setActive('http://node-a:10240')
    nodes.markHealthy('http://node-a:10240')

    // First call (to A): network error (no response field).
    // Failover via selectActiveNode → /health races nodes → both might fail.
    // We expect the call to throw because failover can't find a replacement.
    vi.mocked($fetch)
      .mockRejectedValueOnce(Object.assign(new Error('network'), { response: undefined }))
      .mockRejectedValueOnce(new Error('A health failed'))   // selectActiveNode → A /health
      .mockRejectedValueOnce(new Error('B health failed'))   // selectActiveNode → B /health

    const { useApi } = await import('~/composables/useApi')
    const api = useApi()
    await expect(api.get('/fleet/status')).rejects.toThrow()

    expect(nodes.health['http://node-a:10240']).toBe('unhealthy')
  })
})
