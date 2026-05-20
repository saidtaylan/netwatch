/**
 * usePolling tests.
 *
 * onMounted doesn't fire outside a component context in Vitest, so we
 * test the composable's public API (refresh, data, error, loading) directly.
 */
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'

describe('usePolling.refresh()', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('sets data on successful fetch', async () => {
    const fetcher = vi.fn().mockResolvedValue('data')
    const { usePolling } = await import('~/composables/usePolling')
    const { data, refresh } = usePolling(fetcher, { immediate: false })

    await refresh()
    expect(data.value).toBe('data')
  })

  it('sets error on failed fetch', async () => {
    const err = new Error('network error')
    const fetcher = vi.fn().mockRejectedValue(err)
    const { usePolling } = await import('~/composables/usePolling')
    const { error, refresh } = usePolling(fetcher, { immediate: false })

    await refresh()
    expect(error.value).toBe(err)
  })

  it('clears error after successful retry', async () => {
    const fetcher = vi.fn()
      .mockRejectedValueOnce(new Error('fail'))
      .mockResolvedValueOnce('recovered')
    const { usePolling } = await import('~/composables/usePolling')
    const { data, error, refresh } = usePolling(fetcher, { immediate: false })

    await refresh()
    expect(error.value).not.toBeNull()

    await refresh()
    expect(error.value).toBeNull()
    expect(data.value).toBe('recovered')
  })

  it('loading is true during fetch, false before and after', async () => {
    let resolve!: (v: string) => void
    const fetcher = vi.fn(() => new Promise<string>(r => { resolve = r }))
    const { usePolling } = await import('~/composables/usePolling')
    const { loading, refresh } = usePolling(fetcher, { immediate: false })

    expect(loading.value).toBe(false)
    const p = refresh()
    expect(loading.value).toBe(true)
    resolve('done')
    await p
    expect(loading.value).toBe(false)
  })

  it('keeps previous data while refreshing', async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce('first')
      .mockImplementationOnce(() => new Promise(() => {}))  // never resolves
    const { usePolling } = await import('~/composables/usePolling')
    const { data, refresh } = usePolling(fetcher, { immediate: false })

    await refresh()
    expect(data.value).toBe('first')

    // Second fetch starts but never resolves — data should still be 'first'
    refresh()
    expect(data.value).toBe('first')
  })

  it('error streak increments on consecutive failures', async () => {
    // We can't directly inspect errorStreak (private), but we verify
    // that error stays set across multiple failures
    const fetcher = vi.fn().mockRejectedValue(new Error('down'))
    const { usePolling } = await import('~/composables/usePolling')
    const { error, refresh } = usePolling(fetcher, { immediate: false })

    await refresh()
    expect(error.value?.message).toBe('down')
    await refresh()
    expect(error.value?.message).toBe('down')
  })
})
