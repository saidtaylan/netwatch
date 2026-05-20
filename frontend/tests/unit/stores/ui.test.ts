import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useUIStore } from '~/stores/ui'

describe('useUIStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('defaults: pollingIntervalMs=5000, sidebarCollapsed=false', () => {
    const store = useUIStore()
    expect(store.pollingIntervalMs).toBe(5000)
    expect(store.sidebarCollapsed).toBe(false)
  })

  it('setPollingInterval clamps to [1000, 60000]', () => {
    const store = useUIStore()
    store.setPollingInterval(500)
    expect(store.pollingIntervalMs).toBe(1000)
    store.setPollingInterval(999999)
    expect(store.pollingIntervalMs).toBe(60000)
    store.setPollingInterval(10000)
    expect(store.pollingIntervalMs).toBe(10000)
  })

  it('toggleSidebar flips sidebarCollapsed', () => {
    const store = useUIStore()
    store.toggleSidebar()
    expect(store.sidebarCollapsed).toBe(true)
    store.toggleSidebar()
    expect(store.sidebarCollapsed).toBe(false)
  })

  it('addToast adds and auto-removes toast after duration', () => {
    const store = useUIStore()
    store.addToast('success', 'Hello', 1000)
    expect(store.toasts).toHaveLength(1)
    expect(store.toasts[0].type).toBe('success')
    expect(store.toasts[0].message).toBe('Hello')
    vi.advanceTimersByTime(1001)
    expect(store.toasts).toHaveLength(0)
  })

  it('removeToast removes by id', () => {
    const store = useUIStore()
    store.addToast('error', 'Error msg', 60000)
    const id = store.toasts[0].id
    store.removeToast(id)
    expect(store.toasts).toHaveLength(0)
  })

  it('multiple toasts coexist', () => {
    const store = useUIStore()
    store.addToast('info', 'A', 60000)
    store.addToast('warning', 'B', 60000)
    expect(store.toasts).toHaveLength(2)
  })
})
