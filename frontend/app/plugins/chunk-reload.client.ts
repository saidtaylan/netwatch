/**
 * Recover from stale-bundle white pages after a redeploy.
 *
 * This SPA lazy-loads a JS chunk per route. When the served bundle changes
 * (new content-hashed filenames — e.g. after `docker compose` pulls a new UI
 * release), a tab still running the OLD app shell will request a chunk hash that
 * no longer exists on the server and get a 404. Without handling, navigating to
 * such a route renders a blank page.
 *
 * Nuxt emits `app:chunkError` in that situation. We reload once to fetch the
 * current index.html + chunks. A short sessionStorage guard prevents a reload
 * loop if the chunk is genuinely missing rather than just stale.
 */
export default defineNuxtPlugin((nuxtApp) => {
  const GUARD_KEY = 'nw-chunk-reload-at'
  const GUARD_MS = 10000

  nuxtApp.hook('app:chunkError', () => {
    try {
      const last = Number(sessionStorage.getItem(GUARD_KEY) || 0)
      if (Date.now() - last < GUARD_MS) return // already reloaded recently — don't loop
      sessionStorage.setItem(GUARD_KEY, String(Date.now()))
    } catch {
      // sessionStorage unavailable (private mode etc.) — fall through and reload
    }
    window.location.reload()
  })
})
