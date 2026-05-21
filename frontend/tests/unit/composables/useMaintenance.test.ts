/**
 * useMaintenance tests — CRUD ops + toast feedback.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useNodesStore } from '~/stores/nodes'
import { useUIStore } from '~/stores/ui'

describe('useMaintenance.create', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked($fetch).mockReset()
    const nodes = useNodesStore()
    nodes.addNode('http://node-a:10240')
    nodes.setActive('http://node-a:10240')
    nodes.markHealthy('http://node-a:10240')
  })

  it('PUTs to /cluster/maintenance with payload', async () => {
    vi.mocked($fetch)
      .mockResolvedValueOnce({ id: 'w-1', target_id: 'db', reason: 'test', started_at: '2026-01-01T00:00:00Z', ends_at: '2026-01-01T01:00:00Z', created_by: 'ui-admin' })  // PUT
      .mockResolvedValueOnce([])  // refresh GET

    const { useMaintenance } = await import('~/composables/useMaintenance')
    const { create } = useMaintenance()
    await create('db-primary', 3600000, 'DB upgrade', 'admin')

    const putCall = vi.mocked($fetch).mock.calls[0]
    expect(putCall[0]).toBe('http://node-a:10240/cluster/maintenance')
    expect((putCall[1] as any).method).toBe('PUT')
    expect((putCall[1] as any).body).toEqual({
      target_id:   'db-primary',
      duration_ms: 3600000,
      reason:      'DB upgrade',
      created_by:  'admin',
    })
  })

  it('adds success toast on create', async () => {
    vi.mocked($fetch)
      .mockResolvedValueOnce({})
      .mockResolvedValueOnce([])

    const { useMaintenance } = await import('~/composables/useMaintenance')
    const { create } = useMaintenance()
    await create('db', 60000, 'reason')

    const ui = useUIStore()
    const successToast = ui.toasts.find(t => t.type === 'success')
    expect(successToast?.message).toContain('Maintenance set for db')
  })

  it('adds error toast and re-throws on PUT failure', async () => {
    vi.mocked($fetch).mockRejectedValueOnce(new Error('forbidden'))

    const { useMaintenance } = await import('~/composables/useMaintenance')
    const { create } = useMaintenance()
    await expect(create('db', 60000, 'reason')).rejects.toThrow()

    const ui = useUIStore()
    const errorToast = ui.toasts.find(t => t.type === 'error')
    expect(errorToast).toBeDefined()
  })

  it('uses default createdBy when not provided', async () => {
    vi.mocked($fetch)
      .mockResolvedValueOnce({})
      .mockResolvedValueOnce([])

    const { useMaintenance } = await import('~/composables/useMaintenance')
    const { create } = useMaintenance()
    await create('t', 60000, 'r')

    const putCall = vi.mocked($fetch).mock.calls[0]
    expect((putCall[1] as any).body.created_by).toBe('ui')
  })
})

describe('useMaintenance.cancel', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked($fetch).mockReset()
    const nodes = useNodesStore()
    nodes.addNode('http://node-a:10240')
    nodes.setActive('http://node-a:10240')
    nodes.markHealthy('http://node-a:10240')
  })

  it('DELETEs /cluster/maintenance/{id}', async () => {
    vi.mocked($fetch)
      .mockResolvedValueOnce({})
      .mockResolvedValueOnce([])

    const { useMaintenance } = await import('~/composables/useMaintenance')
    const { cancel } = useMaintenance()
    await cancel('window-abc')

    const delCall = vi.mocked($fetch).mock.calls[0]
    expect(delCall[0]).toBe('http://node-a:10240/cluster/maintenance/window-abc')
    expect((delCall[1] as any).method).toBe('DELETE')
  })

  it('adds success toast on cancel', async () => {
    vi.mocked($fetch)
      .mockResolvedValueOnce({})
      .mockResolvedValueOnce([])

    const { useMaintenance } = await import('~/composables/useMaintenance')
    const { cancel } = useMaintenance()
    await cancel('w1')

    const ui = useUIStore()
    expect(ui.toasts.some(t => t.type === 'success')).toBe(true)
  })
})

describe('useMaintenance.active', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked($fetch).mockReset()
  })

  it('filters out expired windows', async () => {
    const { useMaintenance } = await import('~/composables/useMaintenance')
    const m = useMaintenance()

    const past   = new Date(Date.now() - 60_000).toISOString()
    const future = new Date(Date.now() + 60_000).toISOString()

    // @ts-ignore — directly set data ref for test
    m.windows.data.value = [
      { id: 'w-past',   target_id: 't1', reason: 'r', started_at: past,   ends_at: past,   created_by: 'ui' },
      { id: 'w-future', target_id: 't2', reason: 'r', started_at: past,   ends_at: future, created_by: 'ui' },
    ]
    expect(m.active.value).toHaveLength(1)
    expect(m.active.value[0].id).toBe('w-future')
  })
})
