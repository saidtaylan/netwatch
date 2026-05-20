/**
 * usePolling — Reactive interval fetcher.
 * - Pauses when tab is hidden (visibilitychange)
 * - Respects global pollingIntervalMs from UIStore
 * - Custom interval override per call-site
 * - Exponential back-off on consecutive errors (max 3× interval)
 */
export function usePolling<T>(
  fetcher: () => Promise<T>,
  options?: {
    intervalMs?: number   // override global polling interval
    immediate?: boolean   // fetch on first call (default: true)
  }
) {
  const ui      = useUIStore()
  const data    = ref<T | null>(null)
  const error   = ref<Error | null>(null)
  const loading = ref(false)

  let timer:        ReturnType<typeof setTimeout> | null = null
  let errorStreak = 0
  let visibilityHandler: (() => void) | null = null

  const intervalMs = computed(() =>
    options?.intervalMs ?? ui.pollingIntervalMs
  )

  async function fetch() {
    loading.value = true
    try {
      data.value   = await fetcher()
      error.value  = null
      errorStreak  = 0
    } catch (e) {
      error.value = e as Error
      errorStreak++
    } finally {
      loading.value = false
    }
  }

  function nextDelay(): number {
    // Back-off: base × min(2^errorStreak, 3) — caps at 3× base interval
    const factor = Math.min(Math.pow(2, errorStreak), 3)
    return intervalMs.value * (errorStreak > 0 ? factor : 1)
  }

  function schedule() {
    timer = setTimeout(async () => {
      if (import.meta.client && document.visibilityState !== 'hidden') {
        await fetch()
      }
      schedule()
    }, nextDelay())
  }

  function stop() {
    if (timer) { clearTimeout(timer); timer = null }
  }

  onMounted(async () => {
    if (options?.immediate !== false) await fetch()
    schedule()

    if (import.meta.client) {
      visibilityHandler = async () => {
        if (document.visibilityState === 'visible') {
          stop()
          await fetch()
          schedule()
        }
      }
      document.addEventListener('visibilitychange', visibilityHandler)
    }
  })

  onUnmounted(() => {
    stop()
    if (import.meta.client && visibilityHandler) {
      document.removeEventListener('visibilitychange', visibilityHandler)
      visibilityHandler = null
    }
  })

  return { data, error, loading, refresh: fetch }
}
