/**
 * usePolling — Reactive interval fetcher.
 * - Pauses when tab is hidden (visibilitychange)
 * - Respects global pollingIntervalMs from UIStore
 * - Custom interval override per call-site
 */
export function usePolling<T>(
  fetcher: () => Promise<T>,
  options?: {
    intervalMs?: number   // override global polling interval
    immediate?: boolean   // fetch on first call (default: true)
  }
) {
  const ui     = useUIStore()
  const data   = ref<T | null>(null)
  const error  = ref<Error | null>(null)
  const loading = ref(false)

  let timer: ReturnType<typeof setTimeout> | null = null

  const intervalMs = computed(() =>
    options?.intervalMs ?? ui.pollingIntervalMs
  )

  async function fetch() {
    loading.value = true
    try {
      data.value  = await fetcher()
      error.value = null
    } catch (e) {
      error.value = e as Error
    } finally {
      loading.value = false
    }
  }

  function schedule() {
    timer = setTimeout(async () => {
      if (document.visibilityState !== 'hidden') await fetch()
      schedule()
    }, intervalMs.value)
  }

  function stop() {
    if (timer) { clearTimeout(timer); timer = null }
  }

  onMounted(async () => {
    if (options?.immediate !== false) await fetch()
    schedule()
  })

  onUnmounted(() => stop())

  // Visibility-aware: resume immediately on tab focus
  if (import.meta.client) {
    document.addEventListener('visibilitychange', async () => {
      if (document.visibilityState === 'visible') {
        stop()
        await fetch()
        schedule()
      }
    })
  }

  return { data, error, loading, refresh: fetch }
}
